package worker

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	adpv1 "adp/api/proto/adp/v1"
	"adp/internal/domain/model"
)

func TestExecuteJobPassesJobParametersToModule(t *testing.T) {
	const procName = "adp-definitely-missing-process"

	var completion struct {
		Success bool   `json:"success"`
		Output  string `json:"output"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/workers/worker-1/jobs/job-1/complete" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&completion); err != nil {
			t.Fatalf("decode completion: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewClient(server.URL, "worker-secret", "worker-1", "shell", time.Second)
	client.registeredID = "worker-1"
	client.executeJob(model.Job{
		ID:           "job-1",
		WorkerType:   "shell",
		TemplateCode: "check_process",
		Parameters: map[string]string{
			"ProcessName": procName,
		},
	})

	if !completion.Success {
		t.Fatalf("completion success = false, output=%q", completion.Output)
	}
	if !strings.Contains(completion.Output, procName) {
		t.Fatalf("expected output to contain process parameter %q, got %q", procName, completion.Output)
	}
}

func TestTypedWorkerAuthorization(t *testing.T) {
	mysqlWorker := NewClient("http://example.invalid", "token", "mysql-1", "mysql", time.Second)

	tests := []struct {
		name string
		job  model.Job
		want bool
	}{
		{
			name: "matching mysql module",
			job:  model.Job{WorkerType: "mysql", TemplateCode: "mysql_backup", Parameters: map[string]string{"ServiceType": "mysql"}},
			want: true,
		},
		{
			name: "raw mysql-looking shell command is rejected",
			job:  model.Job{WorkerType: "mysql", Command: "mysqldump app > /tmp/app.sql"},
			want: false,
		},
		{
			name: "redis module is rejected",
			job:  model.Job{WorkerType: "redis", TemplateCode: "redis_ping", Parameters: map[string]string{"ServiceType": "redis"}},
			want: false,
		},
		{
			name: "wrong service type is rejected",
			job:  model.Job{WorkerType: "mysql", TemplateCode: "mysql_backup", Parameters: map[string]string{"ServiceType": "redis"}},
			want: false,
		},
		{
			name: "shell module cannot be relabelled as mysql",
			job:  model.Job{WorkerType: "mysql", TemplateCode: "check_process", Parameters: map[string]string{"ServiceType": "mysql"}},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := mysqlWorker.authorizeJob(tt.job)
			if (err == nil) != tt.want {
				t.Fatalf("authorizeJob() error = %v, want success=%t", err, tt.want)
			}
		})
	}
}

func TestShellWorkerCanRunEveryJobType(t *testing.T) {
	shellWorker := NewClient("http://example.invalid", "token", "shell-1", "shell", time.Second)
	for _, jobType := range []string{"shell", "mysql", "redis"} {
		if err := shellWorker.authorizeJob(model.Job{WorkerType: jobType, Command: "echo permitted"}); err != nil {
			t.Fatalf("shell worker rejected %s job: %v", jobType, err)
		}
	}
}

func TestRestartCommandRebuildsRunnerAndKeepsAgentAlive(t *testing.T) {
	client := NewAgent("http://example.invalid", "token", "shell-1", "shell", time.Second)
	configPath := filepath.Join(t.TempDir(), "services.cnf")
	if err := os.WriteFile(configPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	client.SetServicesConfigPath(configPath)
	oldRunner := client.runner
	acked := false
	shouldStop := client.handleCommand("restart", func(*adpv1.WorkerEnvelope) error {
		acked = true
		return nil
	})
	if shouldStop {
		t.Fatal("restart must keep the Agent running")
	}
	if !acked {
		t.Fatal("restart command was not acknowledged")
	}
	if client.runner == oldRunner {
		t.Fatal("restart did not rebuild the Runner")
	}
}
