package worker

import (
	"strings"
	"testing"

	"adp/internal/domain/model"
)

func TestDiagnosticCommandsSucceedWhenNoMatchIsFound(t *testing.T) {
	runner := NewRunner("shell")
	job := model.Job{
		WorkerType: "shell",
		SourceType: "agent",
		Command:    "ps aux | grep -v grep | grep -F -- adp-process-that-does-not-exist || true",
	}

	success, output := runner.Execute(job, "worker-1")
	if !success {
		t.Fatalf("diagnostic without a match must complete successfully, output=%q", output)
	}
	if strings.Contains(output, "authorization denied") {
		t.Fatalf("diagnostic was not executed: %q", output)
	}
}
