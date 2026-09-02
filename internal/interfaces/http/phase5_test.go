package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"adp/internal/domain/model"
)

func TestIncidentCaseAndMetricsEndpoints(t *testing.T) {
	server := NewServer(Config{
		Addr:              ":0",
		AdminUsername:     "admin",
		AdminPassword:     "admin123",
		AuthSecret:        "secret",
		WorkerSharedToken: "worker-secret",
	}, nil, nil)
	app := httptest.NewServer(server.httpServer.Handler)
	defer app.Close()

	token, _, err := server.authService.Login("admin", "admin123")
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}

	now := time.Now()
	if _, err := server.repo.UpsertIncidentCase("plan-1", model.IncidentCase{
		Title:          "Nginx 不可访问诊断",
		TriggerType:    "nginx_unreachable",
		FaultType:      "Nginx 服务异常",
		Summary:        "Nginx 进程未运行",
		PossibleCauses: []string{"Nginx 进程未运行"},
		Suggestions:    []string{"systemctl start nginx"},
		Confidence:     0.9,
		SourcePlanID:   "plan-1",
		CreatedAt:      now,
		UpdatedAt:      now,
	}); err != nil {
		t.Fatalf("UpsertIncidentCase() error = %v", err)
	}

	var cases []model.IncidentCase
	status := mustJSONRequest(t, app.Client(), http.MethodGet, app.URL+"/api/v1/cases?trigger_type=nginx_unreachable", token, nil, &cases)
	if status != http.StatusOK {
		t.Fatalf("cases status = %d, want %d", status, http.StatusOK)
	}
	if len(cases) != 1 {
		t.Fatalf("expected 1 case, got %d", len(cases))
	}

	suggestions := struct {
		ReferenceCases  []model.IncidentCase `json:"reference_cases"`
		HistoricalHints []string             `json:"historical_hints"`
	}{}
	status = mustJSONRequest(t, app.Client(), http.MethodGet, app.URL+"/api/v1/cases/suggestions?description=nginx+页面无法访问&trigger_type=nginx_unreachable", token, nil, &suggestions)
	if status != http.StatusOK {
		t.Fatalf("case suggestions status = %d, want %d", status, http.StatusOK)
	}
	if len(suggestions.ReferenceCases) != 1 {
		t.Fatalf("expected 1 reference case, got %d", len(suggestions.ReferenceCases))
	}
	if len(suggestions.HistoricalHints) == 0 {
		t.Fatal("expected historical hints")
	}

	worker, _ := server.repo.RegisterWorker("worker-1", "shell")
	job, _ := server.repo.CreateJob(model.Job{Name: "job-1", WorkerType: "shell", Command: "echo demo"})
	if _, err := server.repo.AssignNextJob(worker.ID); err != nil {
		t.Fatal("expected job assignment")
	}
	if _, err := server.repo.CompleteJob(worker.ID, job.ID, "done", true); err != nil {
		t.Fatalf("CompleteJob() error = %v", err)
	}

	resp, err := app.Client().Get(app.URL + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics error = %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	text := string(body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("metrics status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if !strings.Contains(text, "adp_jobs_total") {
		t.Fatalf("expected adp_jobs_total in metrics output: %s", text)
	}
	if !strings.Contains(text, "adp_incident_cases_total") {
		t.Fatalf("expected adp_incident_cases_total in metrics output: %s", text)
	}
	if !strings.Contains(text, "adp_rag_embedding_generation_latency_seconds_avg") {
		t.Fatalf("expected RAG generation latency metric in output: %s", text)
	}
}
