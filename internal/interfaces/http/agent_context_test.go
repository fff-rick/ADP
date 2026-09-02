package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"adp/internal/application/agent"
	"adp/internal/domain/model"
	"adp/internal/infrastructure/db"
	"adp/internal/infrastructure/llm"
)

type contextTestClient struct{}

func (contextTestClient) Complete(context.Context, llm.CompletionRequest) (llm.Completion, error) {
	return llm.Completion{Message: llm.Message{Role: "assistant", Content: "completed"}}, nil
}

func TestPersistentRunSanitizesInputAndStoresContextSnapshot(t *testing.T) {
	repo := db.NewMemoryRepository()
	server := NewServer(Config{}, repo, nil)
	server.agentRuntime = agent.New(contextTestClient{}, nil, 1, 0)

	run, err := server.createPersistentRun("diagnose password=super-secret", "conversation-1", nil)
	if err != nil {
		t.Fatalf("createPersistentRun() error = %v", err)
	}
	if strings.Contains(string(run.Transcript), "super-secret") {
		t.Fatalf("raw secret persisted in transcript: %s", run.Transcript)
	}
	if _, _, err := server.executePersistentRun(context.Background(), run.ID, nil); err != nil {
		t.Fatalf("executePersistentRun() error = %v", err)
	}
	snapshots, err := repo.ListAgentContextSnapshots(run.ID)
	if err != nil || len(snapshots) != 1 {
		t.Fatalf("snapshots = %#v, err = %v", snapshots, err)
	}
	if strings.Contains(string(snapshots[0].Messages), "super-secret") || snapshots[0].TokenEstimate <= 0 || snapshots[0].ContentSHA256 == "" {
		t.Fatalf("unsafe or incomplete context snapshot: %#v", snapshots[0])
	}
	var request llm.CompletionRequest
	if err := json.Unmarshal(snapshots[0].Messages, &request); err != nil {
		t.Fatalf("decode stored request: %v", err)
	}
	if len(request.Messages) != 2 || !strings.Contains(request.Messages[1].Content, "[REDACTED]") {
		t.Fatalf("unexpected stored context: %#v", request.Messages)
	}
}

func TestConversationContextKeepsCompleteRecentTurnsAndEvidenceCards(t *testing.T) {
	var messages []model.ConversationMessage
	for i := 1; i <= 5; i++ {
		messages = append(messages,
			model.ConversationMessage{Role: "user", Content: "request-" + string(rune('0'+i))},
			model.ConversationMessage{Role: "tool", ToolName: "get_job_result", Step: i, ToolData: map[string]any{"output": "evidence-" + string(rune('0'+i))}},
			model.ConversationMessage{Role: "assistant", Content: "answer-" + string(rune('0'+i))},
		)
	}
	contextMessages := conversationMessagesToLLM(messages)
	if len(contextMessages) != 10 { // digest + evidence card + 4 complete user/assistant turns
		t.Fatalf("context length = %d, want 10: %#v", len(contextMessages), contextMessages)
	}
	if contextMessages[0].Role != "system" || !strings.Contains(contextMessages[0].Content, "request-1") || contextMessages[1].Role != "system" || !strings.Contains(contextMessages[1].Content, "evidence-2") || strings.Contains(contextMessages[1].Content, "evidence-1") {
		t.Fatalf("unexpected compacted context: %#v", contextMessages[:2])
	}
	if contextMessages[2].Content != "request-2" || contextMessages[len(contextMessages)-1].Content != "answer-5" {
		t.Fatalf("turn boundary was not preserved: %#v", contextMessages)
	}
}

func TestContextShadowRecordsBaselineAndCompactedTokens(t *testing.T) {
	server := NewServer(Config{AgentContextShadowEnabled: true}, db.NewMemoryRepository(), nil)
	var messages []model.ConversationMessage
	for i := 0; i < 5; i++ {
		messages = append(messages, model.ConversationMessage{Role: "user", Content: strings.Repeat("request ", 100)}, model.ConversationMessage{Role: "assistant", Content: strings.Repeat("answer ", 100)})
	}
	server.recordContextShadow(messages, conversationMessagesToLLM(messages))
	snapshot := server.agentMetrics.snapshot()
	if snapshot.contextShadowSamples != 1 || snapshot.contextShadowBaselineTokens <= snapshot.contextShadowCompactedTokens {
		t.Fatalf("unexpected shadow metrics: %#v", snapshot)
	}
}

func TestAgentContextSnapshotsEndpointDoesNotExposeMessages(t *testing.T) {
	repo := db.NewMemoryRepository()
	server := NewServer(Config{}, repo, nil)
	run, err := repo.CreateAgentRun(model.AgentRun{ID: "run-context"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateAgentContextSnapshot(model.AgentContextSnapshot{RunID: run.ID, Step: 1, Messages: []byte(`[{"content":"secret"}]`), ContentSHA256: "hash"}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/agent/runs/run-context/context", nil)
	resp := httptest.NewRecorder()
	server.handleGetAgentRun(resp, req)
	if resp.Code != http.StatusOK || strings.Contains(resp.Body.String(), "secret") || !strings.Contains(resp.Body.String(), "content_sha256") {
		t.Fatalf("unexpected context response: status=%d body=%s", resp.Code, resp.Body.String())
	}
}
