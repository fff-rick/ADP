package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"adp/internal/domain/model"
)

func TestAdminCanListAndReviewPendingIncidentCases(t *testing.T) {
	server := NewServer(Config{AdminUsername: "admin", AdminPassword: "admin123", AuthSecret: "test-secret"}, nil, nil)
	candidate, err := server.repo.UpsertIncidentCase("run-review-http", model.IncidentCase{Title: "Nginx unavailable", EvidenceSummary: "HTTP check failed", Status: model.IncidentCaseStatusPendingReview})
	if err != nil {
		t.Fatal(err)
	}
	app := httptest.NewServer(server.Handler())
	defer app.Close()
	token, _, err := server.authService.Login("admin", "admin123")
	if err != nil {
		t.Fatal(err)
	}
	var pending []model.IncidentCase
	if status := mustJSONRequest(t, app.Client(), http.MethodGet, app.URL+"/api/v1/cases/pending", token, nil, &pending); status != http.StatusOK {
		t.Fatalf("pending status = %d", status)
	}
	if len(pending) != 1 || pending[0].ID != candidate.ID {
		t.Fatalf("unexpected pending cases: %#v", pending)
	}
	var reviewed model.IncidentCase
	if status := mustJSONRequest(t, app.Client(), http.MethodPost, app.URL+"/api/v1/cases/"+candidate.ID+"/review", token, map[string]any{"action": "approve", "note": "verified", "updates": map[string]any{"root_cause": "invalid upstream configuration", "resolution_steps": []string{"fix config", "verify health"}, "environment_tags": []string{"prod", "nginx"}}}, &reviewed); status != http.StatusOK {
		t.Fatalf("review status = %d", status)
	}
	if reviewed.Status != model.IncidentCaseStatusApproved || reviewed.ReviewedBy != "admin" {
		t.Fatalf("unexpected reviewed case: %#v", reviewed)
	}
	if reviewed.RootCause != "invalid upstream configuration" || len(reviewed.ResolutionSteps) != 2 || len(reviewed.EnvironmentTags) != 2 {
		t.Fatalf("review updates were not saved: %#v", reviewed)
	}
	var retry map[string]string
	if status := mustJSONRequest(t, app.Client(), http.MethodPost, app.URL+"/api/v1/cases/"+candidate.ID+"/embedding/retry", token, nil, &retry); status != http.StatusOK || retry["status"] != "queued" {
		t.Fatalf("retry status=%d response=%#v", status, retry)
	}
	if status := mustJSONRequest(t, app.Client(), http.MethodGet, app.URL+"/api/v1/cases/pending", token, nil, &pending); status != http.StatusOK || len(pending) != 0 {
		t.Fatalf("pending after review status=%d values=%#v", status, pending)
	}
}

func TestAdminCanViewAndRetryFailedIncidentCaseEmbedding(t *testing.T) {
	server := NewServer(Config{AdminUsername: "admin", AdminPassword: "admin123", AuthSecret: "test-secret"}, nil, nil)
	candidate, err := server.repo.UpsertIncidentCase("run-embedding-failure", model.IncidentCase{Title: "Nginx vector failure", Status: model.IncidentCaseStatusApproved})
	if err != nil {
		t.Fatal(err)
	}
	if err := server.repo.QueueIncidentCaseEmbedding(candidate.ID, "hash", "model", 1536); err != nil {
		t.Fatal(err)
	}
	if err := server.repo.FailIncidentCaseEmbedding(candidate.ID, "embedding provider unavailable"); err != nil {
		t.Fatal(err)
	}
	app := httptest.NewServer(server.Handler())
	defer app.Close()
	token, _, err := server.authService.Login("admin", "admin123")
	if err != nil {
		t.Fatal(err)
	}
	var failed []model.IncidentCaseEmbeddingStatus
	if status := mustJSONRequest(t, app.Client(), http.MethodGet, app.URL+"/api/v1/cases/embedding/failed", token, nil, &failed); status != http.StatusOK {
		t.Fatalf("failed embeddings status = %d", status)
	}
	if len(failed) != 1 || failed[0].CaseID != candidate.ID || failed[0].LastError != "embedding provider unavailable" {
		t.Fatalf("unexpected failed embedding list: %#v", failed)
	}
	var retry map[string]string
	if status := mustJSONRequest(t, app.Client(), http.MethodPost, app.URL+"/api/v1/cases/"+candidate.ID+"/embedding/retry", token, nil, &retry); status != http.StatusOK || retry["status"] != "queued" {
		t.Fatalf("retry status=%d response=%#v", status, retry)
	}
	metrics, err := server.repo.RAGMetrics()
	if err != nil || metrics.Queued != 1 || metrics.Failed != 0 {
		t.Fatalf("unexpected RAG metrics=%#v err=%v", metrics, err)
	}
}

func TestApprovedCaseListIncludesEmbeddingStatus(t *testing.T) {
	server := NewServer(Config{AdminUsername: "admin", AdminPassword: "admin123", AuthSecret: "test-secret"}, nil, nil)
	candidate, err := server.repo.UpsertIncidentCase("run-embedding-status", model.IncidentCase{Title: "Indexed Nginx case", Status: model.IncidentCaseStatusApproved})
	if err != nil {
		t.Fatal(err)
	}
	if err := server.repo.QueueIncidentCaseEmbedding(candidate.ID, "hash", "model", 1024); err != nil {
		t.Fatal(err)
	}
	if err := server.repo.CompleteIncidentCaseEmbedding(candidate.ID, "model", "[0,1]"); err != nil {
		t.Fatal(err)
	}
	app := httptest.NewServer(server.Handler())
	defer app.Close()
	token, _, err := server.authService.Login("admin", "admin123")
	if err != nil {
		t.Fatal(err)
	}
	var cases []model.IncidentCase
	if status := mustJSONRequest(t, app.Client(), http.MethodGet, app.URL+"/api/v1/cases", token, nil, &cases); status != http.StatusOK {
		t.Fatalf("case list status = %d", status)
	}
	if len(cases) != 1 || cases[0].EmbeddingStatus != "ready" {
		t.Fatalf("embedding status not returned: %#v", cases)
	}
}
