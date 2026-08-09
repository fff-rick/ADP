package api

import (
	"testing"

	"adp/internal/application/agent"
	"adp/internal/domain/model"
)

func TestPendingApprovalsFromEvents(t *testing.T) {
	events := []agent.Event{
		{Type: "assistant", Data: "creating operation"},
		{Type: "tool", Data: map[string]any{"ok": true, "result": map[string]any{"jobs": []map[string]any{
			{"job_id": "pending", "approval_required": true, "status": model.JobStatusWaitingApproval},
			{"job_id": "not-required", "approval_required": false, "status": model.JobStatusWaitingApproval},
			{"job_id": "queued", "approval_required": true, "status": model.JobStatusQueued},
		}}}},
		{Type: "tool", Data: map[string]any{"ok": true, "result": map[string]any{"jobs": []map[string]any{
			{"job_id": "pending", "approval_required": true, "status": "waiting_approval"},
		}}}},
	}

	pending := pendingApprovalsFromEvents(events)
	if len(pending) != 1 || pending[0]["job_id"] != "pending" {
		t.Fatalf("pending approvals = %#v, want only pending job", pending)
	}
}
