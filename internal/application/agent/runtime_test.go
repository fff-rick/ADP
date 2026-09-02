package agent

import (
	"context"
	"encoding/json"
	"fmt"
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

type finalClient struct{ calls int }

func (c *finalClient) Complete(_ context.Context, _ llm.CompletionRequest) (llm.Completion, error) {
	c.calls++
	return llm.Completion{Message: llm.Message{Role: "assistant", Content: "done"}}, nil
}

type captureClient struct {
	calls    int
	requests []llm.CompletionRequest
}

func (c *captureClient) Complete(_ context.Context, request llm.CompletionRequest) (llm.Completion, error) {
	c.calls++
	c.requests = append(c.requests, request)
	if c.calls == 1 {
		return llm.Completion{Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "call-long", Name: "inspect", Arguments: json.RawMessage(`{"target":"worker-1"}`)}}}}, nil
	}
	return llm.Completion{Message: llm.Message{Role: "assistant", Content: "done"}}, nil
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

func TestSystemPromptRequiresHistoricalSearchAfterOperationFailure(t *testing.T) {
	for _, required := range []string{"after get_job_result reports a failed or cancelled operation", "After a failed or cancelled operation, search the reviewed incident cases"} {
		if !strings.Contains(SystemPrompt, required) {
			t.Fatalf("SystemPrompt missing failure-retrieval rule %q", required)
		}
	}
}

func TestHistoricalSearchRequiredAfterFailedJobResult(t *testing.T) {
	failedJob := []Event{{Type: "tool", Name: "get_job_result", Data: map[string]any{"result": map[string]any{"results": []map[string]any{{"status": "failed"}}}}}}
	if !historicalSearchRequired(failedJob) {
		t.Fatal("failed job result should require historical search")
	}
	if historicalSearchRequired(append(failedJob, Event{Type: "tool", Name: "search_incident_cases"})) {
		t.Fatal("completed historical search should satisfy guard")
	}
}

func TestRuntimeSnapshotsContextBeforeModelCall(t *testing.T) {
	client := &finalClient{}
	var gotMessages []llm.Message
	var gotEstimate, gotBudget int
	_, err := New(client, nil, 1, 0).WithContextBudget(ContextBudget{
		ModelContextWindowTokens: 4096, ReservedOutputTokens: 512, HardUsageRatio: 0.8,
	}).RunMessages(context.Background(), []llm.Message{{Role: "system", Content: "rules"}, {Role: "user", Content: "inspect"}}, 1, nil, Observer{
		BeforeModel: func(_ int, messages []llm.Message, _ []llm.ToolDefinition, estimate, budget int) error {
			gotMessages, gotEstimate, gotBudget = messages, estimate, budget
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if client.calls != 1 || len(gotMessages) != 2 || gotEstimate <= 0 || gotBudget != 2867 {
		t.Fatalf("unexpected pre-model context: calls=%d messages=%d estimate=%d budget=%d", client.calls, len(gotMessages), gotEstimate, gotBudget)
	}
}

func TestRuntimeRejectsOverBudgetBeforeProviderCall(t *testing.T) {
	client := &finalClient{}
	_, err := New(client, nil, 1, 0).WithContextBudget(ContextBudget{
		ModelContextWindowTokens: 100, ReservedOutputTokens: 10, HardUsageRatio: 0.8,
	}).RunMessages(context.Background(), []llm.Message{{Role: "system", Content: strings.Repeat("x", 1000)}}, 1, nil, Observer{})
	if err == nil || !strings.Contains(err.Error(), "context budget exceeded") {
		t.Fatalf("RunMessages() error = %v, want context budget failure", err)
	}
	if client.calls != 0 {
		t.Fatalf("provider called %d times after budget failure", client.calls)
	}
}

func TestRuntimeOffloadsLongToolResultOnlyInModelContext(t *testing.T) {
	client := &captureClient{}
	toolResult := strings.Repeat("diagnostic-line ", 100)
	longTool := toolFunc{name: "inspect", execute: func(context.Context, json.RawMessage) (any, error) {
		return map[string]string{"output": toolResult}, nil
	}}
	var persisted map[string]any
	_, err := New(client, []Tool{longTool}, 2, 0).WithContextBudget(ContextBudget{ToolEvidenceMaxTokens: 30}).RunMessages(
		WithRunID(context.Background(), "run-1"), []llm.Message{{Role: "system", Content: "rules"}, {Role: "user", Content: "inspect"}}, 1, nil, Observer{
			OnToolResult: func(_ int, _ llm.ToolCall, payload map[string]any) error { persisted = payload; return nil },
		})
	if err != nil {
		t.Fatal(err)
	}
	if client.calls != 2 || persisted == nil || !strings.Contains(fmt.Sprint(persisted), toolResult[:40]) {
		t.Fatalf("full tool result was not retained for audit: calls=%d payload=%#v", client.calls, persisted)
	}
	toolMessage := client.requests[1].Messages[len(client.requests[1].Messages)-1]
	if toolMessage.Role != "tool" || toolMessage.ToolCallID != "call-long" || !strings.Contains(toolMessage.Content, `"evidence_ref":"run-1:call-long"`) || !strings.Contains(toolMessage.Content, `"truncated":true`) {
		t.Fatalf("tool protocol was not replaced with evidence card: %#v", toolMessage)
	}
}

type toolFunc struct {
	name    string
	execute func(context.Context, json.RawMessage) (any, error)
}

func (t toolFunc) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{Name: t.name, Parameters: map[string]any{"type": "object"}}
}
func (t toolFunc) Execute(ctx context.Context, args json.RawMessage) (any, error) {
	return t.execute(ctx, args)
}
