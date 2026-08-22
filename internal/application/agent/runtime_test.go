package agent

import (
	"context"
	"encoding/json"
	"strings"
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
	result, err := New(client, []Tool{tool}, 3, 0).Run(context.Background(), "inspect worker", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Answer, "worker is healthy") || !strings.Contains(result.Answer, "本次工具证据") || !strings.Contains(result.Answer, "历史参考案例") || result.Steps != 2 || !tool.called {
		t.Fatalf("unexpected result: %+v, tool called=%v", result, tool.called)
	}
}

func TestRuntimePausesAfterApprovedOperationIsRecorded(t *testing.T) {
	client := &scriptedClient{}
	tool := &testTool{}
	var recordedCall, recordedResult bool
	result, err := New(client, []Tool{tool}, 3, 0).RunMessages(context.Background(), []llm.Message{
		{Role: "system", Content: SystemPrompt}, {Role: "user", Content: "restart service"},
	}, 1, nil, Observer{
		OnToolCall:     func(_ int, _ llm.ToolCall) error { recordedCall = true; return nil },
		OnToolResult:   func(_ int, _ llm.ToolCall, _ map[string]any) error { recordedResult = true; return nil },
		PauseAfterTool: func(_ llm.ToolCall, _ map[string]any) bool { return true },
	})
	if err != nil || !result.Paused || result.Steps != 1 || !recordedCall || !recordedResult {
		t.Fatalf("unexpected paused result: %+v, call=%v result=%v err=%v", result, recordedCall, recordedResult, err)
	}
}
