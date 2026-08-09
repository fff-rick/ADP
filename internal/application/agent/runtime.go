// Package agent implements the bounded, auditable tool-calling loop.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"adp/internal/infrastructure/llm"
)

const SystemPrompt = `You are ADP, a controlled operations agent for infrastructure diagnostics and repair.

## Rules
1. Always use list_workers FIRST to discover available Workers. Never guess worker IDs.
2. Use list_capabilities to see what operations are available for each worker type.
3. Prefer registered modules (create_module_operation). Use execute_shell_command only when no module fits.
4. Never invent worker IDs, commands, YAML, credentials, or skip approvals.
5. Observe each tool result before deciding the next step.
6. Be concise. Summarize findings, don't repeat raw JSON.

## Failure handling
- If a command fails due to permissions (polkit, EACCES, "permission denied"), do NOT retry with variations like sudo or alternative syntax. Tell the user what failed and suggest they configure sudoers on the Worker.
- After 2 consecutive failures for the same goal, STOP and tell the user. Do not keep retrying.
- Before running a risky command, first check if a simpler read-only diagnostic can confirm the state.

## Workflow
1. list_workers → discover targets
2. get_worker_facts → check host state
3. create_module_operation or execute_shell_command → act
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
	if r.client == nil {
		return Result{}, fmt.Errorf("agent model is not configured")
	}
	if strings.TrimSpace(input) == "" {
		return Result{}, fmt.Errorf("input is required")
	}
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	definitions := make([]llm.ToolDefinition, 0, len(r.tools))
	for _, tool := range r.tools {
		definitions = append(definitions, tool.Definition())
	}
	messages := []llm.Message{{Role: "system", Content: SystemPrompt}}
	messages = append(messages, history...)
	messages = append(messages, llm.Message{Role: "user", Content: input})
	result := Result{}
	for step := 1; step <= r.maxSteps; step++ {
		completion, err := r.client.Complete(ctx, llm.CompletionRequest{Messages: messages, Tools: definitions})
		if err != nil {
			return result, fmt.Errorf("agent step %d: %w", step, err)
		}
		result.Steps = step
		messages = append(messages, completion.Message)
		ev := Event{Step: step, Type: "assistant", Data: completion.Message.Content}
		result.Events = append(result.Events, ev)
		if stream != nil {
			select {
			case stream <- ev:
			default:
			}
		}
		if len(completion.Message.ToolCalls) == 0 {
			result.Answer = completion.Message.Content
			return result, nil
		}
		for _, call := range completion.Message.ToolCalls {
			tool, ok := r.tools[call.Name]
			if !ok {
				return result, fmt.Errorf("agent requested undeclared tool %q", call.Name)
			}
			output, toolErr := tool.Execute(ctx, call.Arguments)
			payload := map[string]any{"ok": toolErr == nil, "result": output}
			if toolErr != nil {
				payload["error"] = toolErr.Error()
			}
			encoded, _ := json.Marshal(payload)
			messages = append(messages, llm.Message{Role: "tool", ToolCallID: call.ID, Content: string(encoded)})
			tev := Event{Step: step, Type: "tool", Name: call.Name, Data: payload}
			result.Events = append(result.Events, tev)
			if stream != nil {
				select {
				case stream <- tev:
				default:
				}
			}
		}
	}
	return result, fmt.Errorf("agent exceeded maximum of %d steps", r.maxSteps)
}
