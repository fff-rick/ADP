package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"adp/internal/domain/model"
)

func TestTaskApprovalFlow(t *testing.T) {
	server := NewServer(Config{
		Addr:              ":0",
		AdminUsername:     "admin",
		AdminPassword:     "admin123",
		AuthSecret:        "secret",
		WorkerSharedToken: "worker-secret",
	}, nil, nil)
	installTestParserRules(t, server)
	app := httptest.NewServer(server.httpServer.Handler)
	defer app.Close()

	token, _, err := server.authService.Login("admin", "admin123")
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}

	runResp := struct {
		Job              model.Job `json:"job"`
		ApprovalRequired bool      `json:"approval_required"`
	}{}
	status := mustJSONRequest(t, app.Client(), http.MethodPost, app.URL+"/api/v1/tasks/run", token, map[string]any{
		"input": "每天备份 mysql 数据库",
		"parameters": map[string]string{
			"Database":       "demo",
			"ServiceProfile": "mysql_prod",
		},
	}, &runResp)
	if status != http.StatusAccepted {
		t.Fatalf("tasks/run status = %d, want %d", status, http.StatusAccepted)
	}
	if !runResp.ApprovalRequired {
		t.Fatal("expected approval_required = true")
	}
	if runResp.Job.Status != model.JobStatusWaitingApproval {
		t.Fatalf("job status = %s, want waiting_approval", runResp.Job.Status)
	}

	var pending []model.Job
	status = mustJSONRequest(t, app.Client(), http.MethodGet, app.URL+"/api/v1/approvals/jobs", token, nil, &pending)
	if status != http.StatusOK {
		t.Fatalf("approvals/jobs status = %d, want %d", status, http.StatusOK)
	}
	if len(pending) != 1 || pending[0].ID != runResp.Job.ID {
		t.Fatalf("unexpected pending approvals: %+v", pending)
	}

	approvalResp := model.Job{}
	status = mustJSONRequest(t, app.Client(), http.MethodPost, app.URL+"/api/v1/approvals/jobs/"+runResp.Job.ID, token, map[string]any{
		"approved": true,
		"comment":  "approved for backup",
	}, &approvalResp)
	if status != http.StatusOK {
		t.Fatalf("approval status = %d, want %d", status, http.StatusOK)
	}
	if approvalResp.Status != model.JobStatusPending {
		t.Fatalf("approved job status = %s, want pending", approvalResp.Status)
	}

	worker := model.Worker{}
	status = mustWorkerJSONRequest(t, app.Client(), http.MethodPost, app.URL+"/api/v1/workers/register", "worker-secret", map[string]string{
		"name":        "worker-1",
		"worker_type": "shell",
	}, &worker)
	if status != http.StatusCreated {
		t.Fatalf("worker register status = %d, want %d", status, http.StatusCreated)
	}

	pollResp := struct {
		Job *model.Job `json:"job"`
	}{}
	status = mustWorkerJSONRequest(t, app.Client(), http.MethodPost, app.URL+"/api/v1/workers/"+worker.ID+"/poll", "worker-secret", map[string]string{}, &pollResp)
	if status != http.StatusOK {
		t.Fatalf("worker poll status = %d, want %d", status, http.StatusOK)
	}
	if pollResp.Job != nil {
		t.Fatalf("expected worker poll not to auto-assign jobs, got %+v", pollResp.Job)
	}

	var auditLogs []model.AuditLog
	status = mustJSONRequest(t, app.Client(), http.MethodGet, app.URL+"/api/v1/audit/logs?resource_type=job&resource_id="+runResp.Job.ID, token, nil, &auditLogs)
	if status != http.StatusOK {
		t.Fatalf("audit logs status = %d, want %d", status, http.StatusOK)
	}
	if len(auditLogs) < 2 {
		t.Fatalf("expected at least 2 audit logs, got %d", len(auditLogs))
	}
}

func mustJSONRequest(t *testing.T, client *http.Client, method, url, token string, body any, target any) int {
	t.Helper()

	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			t.Fatalf("json.Marshal() error = %v", err)
		}
	}

	req, err := http.NewRequest(method, url, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do() error = %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if target != nil {
		if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
	}

	return resp.StatusCode
}

func mustWorkerJSONRequest(t *testing.T, client *http.Client, method, url, workerToken string, body any, target any) int {
	t.Helper()

	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			t.Fatalf("json.Marshal() error = %v", err)
		}
	}

	req, err := http.NewRequest(method, url, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	req.Header.Set("X-Worker-Token", workerToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do() error = %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if target != nil {
		if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
	}

	return resp.StatusCode
}
