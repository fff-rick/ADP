// Package agent implements the bounded, auditable tool-calling loop.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"adp/internal/domain/model"
	"adp/internal/infrastructure/llm"
)

const SystemPrompt = `You are ADP, a controlled operations agent for infrastructure diagnostics and repair.

## Rules
1. Always use list_workers FIRST to discover available Workers. Never guess worker IDs.
2. Use list_capabilities to see what operations are available for each worker type.
3. Use only registered modules through create_module_operation. Shell commands, SQL, YAML and arbitrary files are never available.
4. Never invent worker IDs, module codes, commands, YAML, credentials, or skip approvals.
5. Observe each tool result before deciding the next step.
6. For diagnosis requests, search_incident_cases before proposing a cause when historical cases may help. Historical cases are suggestions, never current facts.
7. The final report MUST have two headings: "本次工具证据" (only facts obtained from this run's live tools) and "历史参考案例" (only historical case IDs and recommendations). Do not describe historical results as current observations.
8. Be concise. Summarize findings, don't repeat raw JSON.

## Failure handling
- If a command fails due to permissions (polkit, EACCES, "permission denied"), do NOT retry with variations like sudo or alternative syntax. Tell the user what failed and suggest they configure sudoers on the Worker.
- After 2 consecutive failures for the same goal, STOP and tell the user. Do not keep retrying.
- Before running a risky command, first check if a simpler read-only diagnostic can confirm the state.

## Workflow
1. list_workers → discover targets
2. get_worker_facts → check host state
3. create_module_operation → act
4. get_job_result → verify
5. Answer concisely with evidence.`

type Tool interface {
	Definition() llm.ToolDefinition
	Execute(context.Context, json.RawMessage) (any, error)
}

type Event struct {
	Step int    `json:"step"`
	Type string `json:"type"`
	Name string `json:"name,omitempty"`
	Data any    `json:"data,omitempty"`
}
type Result struct {
	Answer string  `json:"answer"`
	Events []Event `json:"events"`
	Steps  int     `json:"steps"`
	Paused bool    `json:"paused,omitempty"`
}

// Observer receives durable protocol boundaries. Returning an error stops the run.
type Observer struct {
	OnModelComplete func(step int, latency time.Duration, usage llm.Usage) error
	OnAssistant     func(step int, message llm.Message) error
	OnToolCall      func(step int, call llm.ToolCall) error
	OnToolResult    func(step int, call llm.ToolCall, payload map[string]any) error
	OnToolComplete  func(step int, call llm.ToolCall, latency time.Duration, err error) error
	PauseAfterTool  func(call llm.ToolCall, payload map[string]any) bool
}

type runIDContextKey struct{}
type toolCallIDContextKey struct{}

func WithRunID(ctx context.Context, runID string) context.Context {
	return context.WithValue(ctx, runIDContextKey{}, runID)
}
func RunID(ctx context.Context) string {
	value, _ := ctx.Value(runIDContextKey{}).(string)
	return value
}
func ToolCallID(ctx context.Context) string {
	value, _ := ctx.Value(toolCallIDContextKey{}).(string)
	return value
}

type Runtime struct {
	client   llm.Client
	tools    map[string]Tool
	maxSteps int
	timeout  time.Duration
}

func New(client llm.Client, tools []Tool, maxSteps int, timeout time.Duration) *Runtime {
	if maxSteps <= 0 {
		maxSteps = 20
	}
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	registered := make(map[string]Tool, len(tools))
	for _, tool := range tools {
		registered[tool.Definition().Name] = tool
	}
	return &Runtime{client: client, tools: registered, maxSteps: maxSteps, timeout: timeout}
}

func (r *Runtime) Run(ctx context.Context, input string, history []llm.Message) (Result, error) {
	return r.RunStreaming(ctx, input, history, nil)
}

// RunStreaming runs the agent loop and emits events to the channel as they happen.
// Pass nil for stream to disable streaming (same as Run).
func (r *Runtime) RunStreaming(ctx context.Context, input string, history []llm.Message, stream chan<- Event) (Result, error) {
	if strings.TrimSpace(input) == "" {
		return Result{}, fmt.Errorf("input is required")
	}
	messages := []llm.Message{{Role: "system", Content: SystemPrompt}}
	messages = append(messages, history...)
	messages = append(messages, llm.Message{Role: "user", Content: input})
	return r.RunMessages(ctx, messages, 1, stream, Observer{})
}

// RunMessages continues a previously persisted LLM transcript. Messages must
// already include the system and initial user messages.
func (r *Runtime) RunMessages(ctx context.Context, messages []llm.Message, startStep int, stream chan<- Event, observer Observer) (Result, error) {
	if r.client == nil {
		return Result{}, fmt.Errorf("agent model is not configured")
	}
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	definitions := make([]llm.ToolDefinition, 0, len(r.tools))
	for _, tool := range r.tools {
		definitions = append(definitions, tool.Definition())
	}
	result := Result{}
	discoveredWorkers := map[string]struct{}{}
	if startStep < 1 {
		startStep = 1
	}
	for step := startStep; step <= r.maxSteps; step++ {
		started := time.Now()
		completion, err := r.client.Complete(ctx, llm.CompletionRequest{Messages: messages, Tools: definitions})
		if err != nil {
			return result, fmt.Errorf("agent step %d: %w", step, err)
		}
		if observer.OnModelComplete != nil {
			if err := observer.OnModelComplete(step, time.Since(started), completion.Usage); err != nil {
				return result, err
			}
		}
		result.Steps = step
		messages = append(messages, completion.Message)
		if observer.OnAssistant != nil {
			if err := observer.OnAssistant(step, completion.Message); err != nil {
				return result, err
			}
		}
		ev := Event{Step: step, Type: "assistant", Data: completion.Message.Content}
		result.Events = append(result.Events, ev)
		if stream != nil {
			select {
			case stream <- ev:
			default:
			}
		}
		if len(completion.Message.ToolCalls) == 0 {
			result.Answer = ensureEvidenceSections(completion.Message.Content, result.Events)
			return result, nil
		}
		for _, call := range completion.Message.ToolCalls {
			tool, ok := r.tools[call.Name]
			if !ok {
				return result, fmt.Errorf("agent requested undeclared tool %q", call.Name)
			}
			if call.Name != "list_workers" {
				if err := validateDiscoveredWorkers(call.Arguments, discoveredWorkers); err != nil {
					return result, fmt.Errorf("agent requested unauthorized target: %w", err)
				}
			}
			if observer.OnToolCall != nil {
				if err := observer.OnToolCall(step, call); err != nil {
					return result, err
				}
			}
			callCtx := context.WithValue(ctx, toolCallIDContextKey{}, call.ID)
			toolStarted := time.Now()
			output, toolErr := tool.Execute(callCtx, call.Arguments)
			// Tool output is untrusted input to the model (and later to the SSE
			// view). Bound and redact it before it crosses either boundary.
			payload := model.SanitizeMap(map[string]any{"ok": toolErr == nil, "result": output})
			if toolErr != nil {
				payload["error"] = model.SanitizeText(toolErr.Error())
			}
			if observer.OnToolComplete != nil {
				if err := observer.OnToolComplete(step, call, time.Since(toolStarted), toolErr); err != nil {
					return result, err
				}
			}
			if call.Name == "list_workers" && toolErr == nil {
				recordDiscoveredWorkers(payload, discoveredWorkers)
			}
			encoded, _ := json.Marshal(payload)
			messages = append(messages, llm.Message{Role: "tool", ToolCallID: call.ID, Content: string(encoded)})
			if observer.OnToolResult != nil {
				if err := observer.OnToolResult(step, call, payload); err != nil {
					return result, err
				}
			}
			tev := Event{Step: step, Type: "tool", Name: call.Name, Data: payload}
			result.Events = append(result.Events, tev)
			if stream != nil {
				select {
				case stream <- tev:
				default:
				}
			}
			if observer.PauseAfterTool != nil && observer.PauseAfterTool(call, payload) {
				result.Paused = true
				return result, nil
			}
		}
	}
	return result, fmt.Errorf("agent exceeded maximum of %d steps", r.maxSteps)
}

// ensureEvidenceSections makes the provenance boundary non-optional even if a
// model misses the prompt instruction. It does not manufacture observations;
// the model's answer remains the only interpretation of tool output.
func ensureEvidenceSections(answer string, events []Event) string {
	if !strings.Contains(answer, "本次工具证据") {
		answer += "\n\n本次工具证据\n- 请以本次运行中 get_worker_facts、get_job_result 或受控操作的工具结果为准。"
	}
	if !strings.Contains(answer, "历史参考案例") {
		var ids []string
		for _, event := range events {
			if event.Type != "tool" || event.Name != "search_incident_cases" {
				continue
			}
			payload, _ := event.Data.(map[string]any)
			result, _ := payload["result"].(map[string]any)
			items, _ := result["cases"].([]map[string]any)
			for _, item := range items {
				if id, _ := item["source_id"].(string); id != "" {
					ids = append(ids, id)
				}
			}
		}
		answer += "\n\n历史参考案例\n"
		if len(ids) == 0 {
			answer += "- 本次未检索到可引用的历史案例；不要将此视为实时检查结论。"
		} else {
			answer += "- 历史案例来源：" + strings.Join(ids, "、") + "。仅作排查与处置参考，不代表当前环境事实。"
		}
	}
	return answer
}

func validateDiscoveredWorkers(raw json.RawMessage, discovered map[string]struct{}) error {
	var input struct {
		WorkerID  string   `json:"worker_id"`
		WorkerIDs []string `json:"worker_ids"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return err
	}
	ids := input.WorkerIDs
	if input.WorkerID != "" {
		ids = append(ids, input.WorkerID)
	}
	for _, id := range ids {
		if _, ok := discovered[id]; !ok {
			return fmt.Errorf("worker %q was not returned by list_workers", id)
		}
	}
	return nil
}

func recordDiscoveredWorkers(payload map[string]any, discovered map[string]struct{}) {
	result, _ := payload["result"].(map[string]any)
	workers, _ := result["workers"].([]map[string]any)
	for _, worker := range workers {
		if id, _ := worker["id"].(string); id != "" {
			discovered[id] = struct{}{}
		}
	}
}
