package db

import (
	"testing"

	"adp/internal/domain/model"
)

func TestMemoryRepositoryAgentRunAndIdempotentJob(t *testing.T) {
	repo := NewMemoryRepository()
	run, err := repo.CreateAgentRun(model.AgentRun{Input: "inspect", TraceID: "trace", PolicyVersion: "p1", PromptVersion: "v1"})
	if err != nil || run.Status != model.AgentRunStatusQueued {
		t.Fatalf("create run: %+v, %v", run, err)
	}
	if _, err := repo.AddAgentEvent(model.AgentEvent{RunID: run.ID, Type: "started"}); err != nil {
		t.Fatal(err)
	}
	events, err := repo.ListAgentEvents(run.ID, 0)
	if err != nil || len(events) != 1 {
		t.Fatalf("events=%+v err=%v", events, err)
	}
	first, err := repo.CreateJob(model.Job{Name: "once", WorkerType: "shell", IdempotencyKey: "run:call:worker"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := repo.CreateJob(model.Job{Name: "twice", WorkerType: "shell", IdempotencyKey: "run:call:worker"})
	if err != nil || first.ID != second.ID {
		t.Fatalf("idempotency failed: %s %s %v", first.ID, second.ID, err)
	}
}
