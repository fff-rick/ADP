package integration_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"adp/internal/domain/model"
	"adp/internal/interfaces/http"
)

func TestPhase6Acceptance(t *testing.T) {
	srv := api.NewServer(api.Config{
		Addr:              ":0",
		AdminUsername:     "admin",
		AdminPassword:     "admin123",
		AuthSecret:        "secret",
		WorkerSharedToken: "worker-secret",
	}, nil, nil)
	app := httptest.NewServer(srv.Handler())
	defer app.Close()

	token := loginForIntegration(t, app.URL)
	installParserRules(t, app.URL, app.Client(), token)

	t.Run("mysql backup approval flow", func(t *testing.T) {
		runResp := struct {
			Job              model.Job `json:"job"`
			ApprovalRequired bool      `json:"approval_required"`
		}{}
		status := doUserJSON(t, app.Client(), http.MethodPost, app.URL+"/api/v1/tasks/run", token, map[string]any{
			"input": "backup mysql database daily",
			"parameters": map[string]string{
				"Database":       "demo",
				"ServiceProfile": "mysql_prod",
				"ServiceType":    "mysql",
			},
		}, &runResp)
		if status != http.StatusAccepted {
			t.Fatalf("tasks/run status = %d, want %d", status, http.StatusAccepted)
		}
		if !runResp.ApprovalRequired || runResp.Job.Status != model.JobStatusWaitingApproval {
			t.Fatalf("expected waiting approval job, got approval_required=%t status=%s", runResp.ApprovalRequired, runResp.Job.Status)
		}

		approveResp := model.Job{}
		status = doUserJSON(t, app.Client(), http.MethodPost, app.URL+"/api/v1/approvals/jobs/"+runResp.Job.ID, token, map[string]any{
			"approved": true,
			"comment":  "phase6 acceptance",
		}, &approveResp)
		if status != http.StatusOK {
			t.Fatalf("approve status = %d, want %d", status, http.StatusOK)
		}
		if approveResp.Status != model.JobStatusPending {
			t.Fatalf("approved status = %s, want pending", approveResp.Status)
		}

		worker := registerWorkerForIntegration(t, app.URL)
		job := dispatchJobForIntegration(t, app.URL, token, runResp.Job.ID, worker.ID)
		if job.ID != runResp.Job.ID {
			t.Fatalf("expected approved backup job to be dispatched, got %+v", job)
		}

		status = doWorkerJSON(t, app.Client(), http.MethodPost, fmt.Sprintf("%s/api/v1/workers/%s/jobs/%s/complete", app.URL, worker.ID, job.ID), map[string]any{
			"success": true,
			"output":  "backup completed",
		}, nil)
		if status != http.StatusOK {
			t.Fatalf("complete status = %d, want %d", status, http.StatusOK)
		}

		finalJob := model.Job{}
		status = doUserJSON(t, app.Client(), http.MethodGet, app.URL+"/api/v1/jobs/"+job.ID, token, nil, &finalJob)
		if status != http.StatusOK {
			t.Fatalf("get job status = %d, want %d", status, http.StatusOK)
		}
		if finalJob.Status != model.JobStatusSuccess {
			t.Fatalf("final job status = %s, want success", finalJob.Status)
		}
	})

	t.Run("nginx diagnosis acceptance", func(t *testing.T) {
		planID := createPlanForIntegration(t, app.URL, token, "nginx is unreachable")
		executePlanForIntegration(t, app.URL, token, planID)
		plan := getPlanForIntegration(t, app.URL, token, planID)
		worker := registerWorkerForIntegration(t, app.URL)
		runDiagnosisJobs(t, app.URL, token, worker.ID, plan, map[string]workerCompletion{
			"check_process":     {Success: false, Output: ""},
			"check_port":        {Success: false, Output: ""},
			"read_log_tail":     {Success: true, Output: "permission denied"},
			"http_health_check": {Success: false, Output: "000"},
		})

		report := analyzePlanForIntegration(t, app.URL, token, planID)
		if report.FaultType == "" {
			t.Fatal("expected nginx analysis report fault type")
		}

		var cases []model.IncidentCase
		status := doUserJSON(t, app.Client(), http.MethodGet, app.URL+"/api/v1/cases?trigger_type=nginx_unreachable", token, nil, &cases)
		if status != http.StatusOK {
			t.Fatalf("cases status = %d, want %d", status, http.StatusOK)
		}
		if len(cases) == 0 {
			t.Fatal("expected nginx incident case to be stored")
		}
	})

	t.Run("redis diagnosis acceptance", func(t *testing.T) {
		planID := createPlanForIntegration(t, app.URL, token, "redis is slow")
		executePlanForIntegration(t, app.URL, token, planID)
		plan := getPlanForIntegration(t, app.URL, token, planID)
		worker := registerWorkerForIntegration(t, app.URL)
		runDiagnosisJobs(t, app.URL, token, worker.ID, plan, map[string]workerCompletion{
			"redis_ping":        {Success: true, Output: "PONG"},
			"redis_info":        {Success: true, Output: "used_memory:104857600"},
			"redis_slowlog_get": {Success: true, Output: "1) 1\n2) GET user:1"},
			"redis_client_list": {Success: true, Output: strings.Repeat("id=1 addr=127.0.0.1:6379\n", 55)},
		})

		report := analyzePlanForIntegration(t, app.URL, token, planID)
		if report.FaultType == "" {
			t.Fatal("expected redis analysis report fault type")
		}

		suggestions := struct {
			ReferenceCases  []model.IncidentCase `json:"reference_cases"`
			HistoricalHints []string             `json:"historical_hints"`
		}{}
		status := doUserJSON(t, app.Client(), http.MethodGet, app.URL+"/api/v1/cases/suggestions?description=redis+is+slow&trigger_type=redis_slow", token, nil, &suggestions)
		if status != http.StatusOK {
			t.Fatalf("case suggestions status = %d, want %d", status, http.StatusOK)
		}
		if len(suggestions.ReferenceCases) == 0 {
			t.Fatal("expected redis reference cases")
		}
		if len(suggestions.HistoricalHints) == 0 {
			t.Fatal("expected redis historical hints")
		}
	})
}

func installParserRules(t *testing.T, baseURL string, client *http.Client, token string) {
	t.Helper()
	status := doUserJSON(t, client, http.MethodPost, baseURL+"/api/v1/configs/parser_rules", token, map[string]any{"yaml_content": `id: task_parser
rules:
  - pattern: '(mysql|数据库).*(备份|backup)|(备份|backup).*(mysql|数据库)'
    intent: create_scheduled_backup
    target_type: mysql
    risk_level: medium
    matched_template: mysql_backup
    parameters: {}
`}, nil)
	if status != http.StatusCreated {
		t.Fatalf("parser rules status = %d", status)
	}
	status = doUserJSON(t, client, http.MethodPost, baseURL+"/api/v1/configs/policies", token, map[string]any{"yaml_content": `id: default
allowed_tools: [echo]
allowed_templates: [mysql_backup, http_health_check, check_process, check_port, read_log_tail, redis_ping, redis_info, redis_slowlog_get, redis_client_list]
high_risk_keywords: [delete, drop]
approval_risk_levels: [medium, high]
`}, nil)
	if status != http.StatusCreated {
		t.Fatalf("policy status = %d", status)
	}
	for _, code := range []string{"mysql_backup", "http_health_check", "check_process", "check_port", "read_log_tail", "redis_ping", "redis_info", "redis_slowlog_get", "redis_client_list"} {
		raw := "code: " + code + "\nname: " + code + "\ntool_type: shell\ncommand: echo ok\nrisk_level: low\nparameters: []\n"
		status = doUserJSON(t, client, http.MethodPost, baseURL+"/api/v1/configs/templates", token, map[string]any{"yaml_content": raw}, nil)
		if status != http.StatusCreated {
			t.Fatalf("template %s status = %d", code, status)
		}
	}
	for _, raw := range []string{`trigger_type: nginx_unreachable
title: Nginx
keywords: [nginx, 网站, http, unreachable]
steps:
  - {template_code: check_process, parameters: {ServiceProfile: nginx_prod, ServiceType: nginx}}
  - {template_code: check_port, parameters: {ServiceProfile: nginx_prod, ServiceType: nginx}}
  - {template_code: read_log_tail, parameters: {ServiceProfile: nginx_prod, ServiceType: nginx}}
  - {template_code: http_health_check, parameters: {ServiceProfile: adp_http, ServiceType: http}}
`, `trigger_type: redis_slow
title: Redis
keywords: [redis, 缓存, 慢, 性能]
steps:
  - {template_code: redis_ping, parameters: {ServiceProfile: redis_prod, ServiceType: redis}}
  - {template_code: redis_info, parameters: {ServiceProfile: redis_prod, ServiceType: redis}}
  - {template_code: redis_slowlog_get, parameters: {ServiceProfile: redis_prod, ServiceType: redis}}
  - {template_code: redis_client_list, parameters: {ServiceProfile: redis_prod, ServiceType: redis}}
`} {
		status = doUserJSON(t, client, http.MethodPost, baseURL+"/api/v1/configs/diagnosis_plans", token, map[string]any{"yaml_content": raw}, nil)
		if status != http.StatusCreated {
			t.Fatalf("diagnosis plan status = %d", status)
		}
	}
}

type workerCompletion struct {
	Success bool
	Output  string
}

func loginForIntegration(t *testing.T, baseURL string) string {
	t.Helper()

	resp := struct {
		Token string `json:"token"`
	}{}
	status := doRawJSON(t, http.DefaultClient, http.MethodPost, baseURL+"/api/v1/auth/login", map[string]any{
		"username": "admin",
		"password": "admin123",
	}, nil, &resp)
	if status != http.StatusOK {
		t.Fatalf("login status = %d, want %d", status, http.StatusOK)
	}
	if resp.Token == "" {
		t.Fatal("expected login token")
	}
	return resp.Token
}

func registerWorkerForIntegration(t *testing.T, baseURL string) model.Worker {
	t.Helper()

	worker := model.Worker{}
	status := doWorkerJSON(t, http.DefaultClient, http.MethodPost, baseURL+"/api/v1/workers/register", map[string]string{
		"name":        "worker-1",
		"worker_type": "shell",
	}, &worker)
	if status != http.StatusCreated {
		t.Fatalf("register worker status = %d, want %d", status, http.StatusCreated)
	}
	return worker
}

func dispatchJobForIntegration(t *testing.T, baseURL, token, jobID, workerID string) model.Job {
	t.Helper()

	resp := struct {
		Jobs []model.Job `json:"jobs"`
	}{}
	status := doUserJSON(t, http.DefaultClient, http.MethodPost, fmt.Sprintf("%s/api/v1/jobs/%s/dispatch", baseURL, jobID), token, map[string]any{
		"worker_ids": []string{workerID},
	}, &resp)
	if status != http.StatusOK {
		t.Fatalf("dispatch job status = %d, want %d", status, http.StatusOK)
	}
	if len(resp.Jobs) != 1 {
		t.Fatalf("expected 1 dispatched job, got %d", len(resp.Jobs))
	}
	return resp.Jobs[0]
}

func createPlanForIntegration(t *testing.T, baseURL, token, description string) string {
	t.Helper()

	plan := model.DiagnosisPlan{}
	status := doUserJSON(t, http.DefaultClient, http.MethodPost, baseURL+"/api/v1/diagnosis/plan", token, map[string]string{
		"description": description,
	}, &plan)
	if status != http.StatusCreated {
		t.Fatalf("create plan status = %d, want %d", status, http.StatusCreated)
	}
	if plan.ID == "" {
		t.Fatal("expected plan id")
	}
	return plan.ID
}

func executePlanForIntegration(t *testing.T, baseURL, token, planID string) {
	t.Helper()
	status := doUserJSON(t, http.DefaultClient, http.MethodPost, fmt.Sprintf("%s/api/v1/diagnosis/plan/%s/execute", baseURL, planID), token, map[string]string{}, nil)
	if status != http.StatusOK {
		t.Fatalf("execute plan status = %d, want %d", status, http.StatusOK)
	}
}

func getPlanForIntegration(t *testing.T, baseURL, token, planID string) model.DiagnosisPlan {
	t.Helper()

	plan := model.DiagnosisPlan{}
	status := doUserJSON(t, http.DefaultClient, http.MethodGet, fmt.Sprintf("%s/api/v1/diagnosis/plan/%s", baseURL, planID), token, nil, &plan)
	if status != http.StatusOK {
		t.Fatalf("get plan status = %d, want %d", status, http.StatusOK)
	}
	return plan
}

func analyzePlanForIntegration(t *testing.T, baseURL, token, planID string) model.AnalysisReport {
	t.Helper()

	report := model.AnalysisReport{}
	status := doUserJSON(t, http.DefaultClient, http.MethodPost, fmt.Sprintf("%s/api/v1/diagnosis/plan/%s/analyze", baseURL, planID), token, map[string]string{}, &report)
	if status != http.StatusOK {
		t.Fatalf("analyze plan status = %d, want %d", status, http.StatusOK)
	}
	return report
}

func runDiagnosisJobs(t *testing.T, baseURL, token, workerID string, plan model.DiagnosisPlan, completions map[string]workerCompletion) {
	t.Helper()

	byJobID := make(map[string]workerCompletion, len(plan.Steps))
	for _, step := range plan.Steps {
		completion, ok := completions[step.TemplateCode]
		if !ok {
			t.Fatalf("missing completion payload for template %s", step.TemplateCode)
		}
		byJobID[step.JobID] = completion
	}

	for _, step := range plan.Steps {
		job := dispatchJobForIntegration(t, baseURL, token, step.JobID, workerID)
		if job.ID != step.JobID {
			t.Fatalf("expected diagnosis job %s to be dispatched, got %s", step.JobID, job.ID)
		}

		completion, ok := byJobID[step.JobID]
		if !ok {
			t.Fatalf("unexpected diagnosis job id %s", step.JobID)
		}

		status := doWorkerJSON(t, http.DefaultClient, http.MethodPost, fmt.Sprintf("%s/api/v1/workers/%s/jobs/%s/complete", baseURL, workerID, step.JobID), map[string]any{
			"success": completion.Success,
			"output":  completion.Output,
		}, nil)
		if status != http.StatusOK {
			t.Fatalf("complete diagnosis job status = %d, want %d", status, http.StatusOK)
		}
		delete(byJobID, step.JobID)
	}

	if len(byJobID) != 0 {
		t.Fatalf("some diagnosis jobs were not completed: %d", len(byJobID))
	}
}

func doUserJSON(t *testing.T, client *http.Client, method, url, token string, body any, target any) int {
	t.Helper()
	headers := map[string]string{
		"Authorization": "Bearer " + token,
		"Content-Type":  "application/json",
	}
	return doRawJSON(t, client, method, url, body, headers, target)
}

func doWorkerJSON(t *testing.T, client *http.Client, method, url string, body any, target any) int {
	t.Helper()
	headers := map[string]string{
		"X-Worker-Token": "worker-secret",
		"Content-Type":   "application/json",
	}
	return doRawJSON(t, client, method, url, body, headers, target)
}

func doRawJSON(t *testing.T, client *http.Client, method, url string, body any, headers map[string]string, target any) int {
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
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do() error = %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if target != nil {
		if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
	} else {
		_, _ = io.Copy(io.Discard, resp.Body)
	}

	return resp.StatusCode
}
