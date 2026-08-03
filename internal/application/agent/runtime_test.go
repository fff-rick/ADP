package agent

import (
	"context"
	"encoding/json"
	"testing"

	"adp/internal/infrastructure/llm"
)

type scriptedClient struct{ calls int }

func (c *scriptedClient) Complete(_ context.Context, _ llm.CompletionRequest) (llm.Completion, error) {
	c.calls++
	if c.calls == 1 {
		return llm.Completion{Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "call-1", Name: "inspect", Arguments: json.RawMessage(`{"target":"worker-1"}`)}}}}, nil
	}
	return llm.Completion{Message: llm.Message{Role: "assistant", Content: "worker is healthy"}}, nil
}

type testTool struct{ called bool }

func (t *testTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{Name: "inspect", Parameters: map[string]any{"type": "object"}}
}
func (t *testTool) Execute(_ context.Context, args json.RawMessage) (any, error) {
	t.called = string(args) == `{"target":"worker-1"}`
	return map[string]string{"status": "ok"}, nil
}
func TestRuntimeRunsToolLoop(t *testing.T) {
	client := &scriptedClient{}
	tool := &testTool{}
	result, err := New(client, []Tool{tool}, 3, 0).Run(context.Background(), "inspect worker")
	if err != nil {
		t.Fatal(err)
	}
	if result.Answer != "worker is healthy" || result.Steps != 2 || !tool.called {
		t.Fatalf("unexpected result: %+v, tool called=%v", result, tool.called)
	}
}
