package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"adp/internal/domain/model"
)

func TestUserWorkerAndTaskManagementEndpoints(t *testing.T) {
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

	adminToken, _, err := server.authService.Login("admin", "admin123")
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}

	createdUser := model.User{}
	status := mustJSONRequest(t, app.Client(), http.MethodPost, app.URL+"/api/v1/users", adminToken, map[string]any{
		"username": "ops-user",
		"password": "ops-pass",
		"role":     "operator",
	}, &createdUser)
	if status != http.StatusCreated {
		t.Fatalf("create user status = %d, want %d", status, http.StatusCreated)
	}
	if createdUser.Username != "ops-user" || createdUser.Role != "operator" {
		t.Fatalf("unexpected created user: %+v", createdUser)
	}

	userList := []model.User{}
	status = mustJSONRequest(t, app.Client(), http.MethodGet, app.URL+"/api/v1/users", adminToken, nil, &userList)
	if status != http.StatusOK {
		t.Fatalf("list users status = %d, want %d", status, http.StatusOK)
	}
	if len(userList) < 2 {
		t.Fatalf("expected at least 2 users, got %d", len(userList))
	}

	operatorToken, _, err := server.authService.Login("ops-user", "ops-pass")
	if err != nil {
		t.Fatalf("operator login error = %v", err)
	}

	worker := model.Worker{}
	status = mustJSONRequest(t, app.Client(), http.MethodPost, app.URL+"/api/v1/workers", operatorToken, map[string]any{
		"name":        "ui-worker",
		"worker_type": "shell",
	}, &worker)
	if status != http.StatusCreated {
		t.Fatalf("create worker status = %d, want %d", status, http.StatusCreated)
	}
	if worker.Name != "ui-worker" {
		t.Fatalf("unexpected worker: %+v", worker)
	}

	taskRun := struct {
		Job model.Job `json:"job"`
	}{}
	status = mustJSONRequest(t, app.Client(), http.MethodPost, app.URL+"/api/v1/tasks/run", operatorToken, map[string]any{
		"input": "每天备份 mysql 数据库",
		"parameters": map[string]string{
			"Database":       "demo",
			"ServiceProfile": "mysql_prod",
		},
	}, &taskRun)
	if status != http.StatusCreated && status != http.StatusAccepted {
		t.Fatalf("task run status = %d, want 201 or 202", status)
	}
	if taskRun.Job.Parameters["Database"] != "demo" || taskRun.Job.Parameters["ServiceProfile"] != "mysql_prod" {
		t.Fatalf("expected non-secret task parameters to be persisted, got %+v", taskRun.Job.Parameters)
	}

	taskJobs := []model.Job{}
	status = mustJSONRequest(t, app.Client(), http.MethodGet, app.URL+"/api/v1/tasks", operatorToken, nil, &taskJobs)
	if status != http.StatusOK {
		t.Fatalf("list tasks status = %d, want %d", status, http.StatusOK)
	}
	if len(taskJobs) == 0 {
		t.Fatal("expected at least one task job")
	}

	workers := []model.Worker{}
	status = mustJSONRequest(t, app.Client(), http.MethodGet, app.URL+"/api/v1/workers", operatorToken, nil, &workers)
	if status != http.StatusOK {
		t.Fatalf("list workers status = %d, want %d", status, http.StatusOK)
	}
	if len(workers) == 0 {
		t.Fatal("expected at least one worker")
	}
}
