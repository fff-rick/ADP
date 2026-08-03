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

const SystemPrompt = `You are ADP, a controlled operations agent. Use tools to inspect facts and create only registered module operations. Never invent results, commands, YAML, credentials, or approvals. Perform one operation at a time, observe its result, then decide the next step. Finish with a concise evidence-based answer.`

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
		maxSteps = 12
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

func (r *Runtime) Run(ctx context.Context, input string) (Result, error) {
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
	messages := []llm.Message{{Role: "system", Content: SystemPrompt}, {Role: "user", Content: input}}
	result := Result{}
	for step := 1; step <= r.maxSteps; step++ {
		completion, err := r.client.Complete(ctx, llm.CompletionRequest{Messages: messages, Tools: definitions})
		if err != nil {
			return result, fmt.Errorf("agent step %d: %w", step, err)
		}
		result.Steps = step
		messages = append(messages, completion.Message)
		result.Events = append(result.Events, Event{Step: step, Type: "assistant", Data: completion.Message.Content})
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
			result.Events = append(result.Events, Event{Step: step, Type: "tool", Name: call.Name, Data: payload})
		}
	}
	return result, fmt.Errorf("agent exceeded maximum of %d steps", r.maxSteps)
}
