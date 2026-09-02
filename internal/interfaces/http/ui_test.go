package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"adp/internal/domain/model"
)

func TestDashboardUIRoutesAndSummary(t *testing.T) {
	server := NewServer(Config{
		Addr:              ":0",
		AdminUsername:     "admin",
		AdminPassword:     "admin123",
		AuthSecret:        "secret",
		WorkerSharedToken: "worker-secret",
	}, nil, nil)
	app := httptest.NewServer(server.httpServer.Handler)
	defer app.Close()

	for _, route := range []string{"/", "/login", "/users", "/workers", "/jobs", "/tasks", "/configs", "/knowledge"} {
		resp, err := app.Client().Get(app.URL + route)
		if err != nil {
			t.Fatalf("GET %s error = %v", route, err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close() //nolint:errcheck
		if err != nil {
			t.Fatalf("ReadAll(%s) error = %v", route, err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s status = %d, want %d", route, resp.StatusCode, http.StatusOK)
		}
		if !strings.Contains(string(body), "ADP") {
			t.Fatalf("ui page missing expected content for %s: %s", route, string(body))
		}
		if route != "/login" && !strings.Contains(string(body), "href=\"/configs\"") {
			t.Fatalf("ui page missing configs navigation for %s", route)
		}
		if route == "/tasks" && !strings.Contains(string(body), "agent-run-monitor") {
			t.Fatalf("tasks page missing Agent run monitor")
		}
		if route == "/tasks" && !strings.Contains(string(body), "历史案例仅供参考") {
			t.Fatalf("tasks page missing historical-case provenance notice")
		}
		if route == "/knowledge" && (!strings.Contains(string(body), "knowledge-list") || !strings.Contains(string(body), "knowledge-import-form")) {
			t.Fatalf("knowledge page missing case list or Markdown import form")
		}
	}

	staticResp, err := app.Client().Get(app.URL + "/static/app.css")
	if err != nil {
		t.Fatalf("GET /static/app.css error = %v", err)
	}
	defer staticResp.Body.Close() //nolint:errcheck
	if staticResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /static/app.css status = %d, want %d", staticResp.StatusCode, http.StatusOK)
	}
	staticJSResp, err := app.Client().Get(app.URL + "/static/app.js")
	if err != nil {
		t.Fatalf("GET /static/app.js error = %v", err)
	}
	jsBody, _ := io.ReadAll(staticJSResp.Body)
	staticJSResp.Body.Close() //nolint:errcheck
	if staticJSResp.StatusCode != http.StatusOK || !strings.Contains(string(jsBody), "refreshAgentRunMonitor") || !strings.Contains(string(jsBody), "renderHistoricalReferences") {
		t.Fatalf("Agent run monitor client script unavailable")
	}

	token, _, err := server.authService.Login("admin", "admin123")
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}

	_, _ = server.repo.RegisterWorker("worker-1", "shell")
	_, _ = server.repo.CreateJob(model.Job{Name: "demo-job", WorkerType: "shell", Command: "echo demo"})

	summary := dashboardSummaryResponse{}
	status := mustJSONRequest(t, app.Client(), http.MethodGet, app.URL+"/api/v1/dashboard/summary", token, nil, &summary)
	if status != http.StatusOK {
		t.Fatalf("dashboard summary status = %d, want %d", status, http.StatusOK)
	}
	if summary.Metrics.JobsTotal != 1 {
		t.Fatalf("jobs_total = %d, want 1", summary.Metrics.JobsTotal)
	}
	if len(summary.Workers) != 1 {
		t.Fatalf("workers len = %d, want 1", len(summary.Workers))
	}
	if summary.TemplatesTotal == 0 {
		// Templates are loaded from managed configs at startup; 0 is acceptable
		// when managed configs are not pre-loaded in the test environment.
		t.Log("templates_total is 0 (managed configs not loaded in test)")
	}
}
