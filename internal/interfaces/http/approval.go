package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"adp/internal/domain/model"
)

type approveJobRequest struct {
	Approved *bool  `json:"approved"`
	Comment  string `json:"comment,omitempty"`
}

func (s *Server) handleListPendingApprovalJobs(w http.ResponseWriter, _ *http.Request) {
	if s.repo != nil {
		jobs, _ := s.repo.ListPendingApprovalJobs()
		writeJSON(w, http.StatusOK, jobs)
		return
	}
	writeJSON(w, http.StatusOK, []model.Job{})
}

func (s *Server) handleApproveJob(w http.ResponseWriter, r *http.Request) {
	jobID := strings.TrimPrefix(r.URL.Path, "/api/v1/approvals/jobs/")
	if jobID == "" {
		writeError(w, http.StatusBadRequest, errors.New("job id is required"))
		return
	}

	var req approveJobRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.Approved == nil {
		writeError(w, http.StatusBadRequest, errors.New("approved is required"))
		return
	}

	user := currentUser(r)

	if s.repo != nil {
		var (
			job model.Job
			err error
		)
		if *req.Approved {
			job, err = s.repo.ApproveJob(jobID, user.Username, req.Comment)
		} else {
			job, err = s.repo.RejectJob(jobID, user.Username, req.Comment)
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if job.ApprovedAt != nil {
			s.agentMetrics.approval(job.CreatedAt)
		}

		// Auto-dispatch if the Agent already assigned a worker.
		if *req.Approved && job.AssignedWorkerID != "" {
			dispatched, derr := s.dispatchJobToWorker(job.ID, job.AssignedWorkerID)
			if derr == nil {
				s.workerHub.PushJob(job.AssignedWorkerID, dispatched)
				job = dispatched
			}
		}
		// A run paused at this approval point resumes inside the service; no client
		// retry or second model submission is needed after a restart.
		if *req.Approved && job.SourceID != "" {
			go func(runID string) {
				if _, _, resumeErr := s.executePersistentRun(context.Background(), runID); resumeErr != nil {
					s.recordAudit("system", "agent", "agent.run.resume_failed", "agent_run", runID, map[string]any{"error": resumeErr.Error()})
				}
			}(job.SourceID)
		}
		if !*req.Approved && job.SourceID != "" {
			if run, runErr := s.repo.GetAgentRun(job.SourceID); runErr == nil && run.Status == model.AgentRunStatusWaitingApproval {
				run.Status = model.AgentRunStatusCancelled
				run.Error = "approval rejected"
				_ = s.repo.UpdateAgentRun(run)
				_, _ = s.repo.AddAgentEvent(model.AgentEvent{RunID: run.ID, Type: "approval_rejected", Data: map[string]any{"job_id": job.ID}})
			}
		}

		action := "job.approval.rejected"
		if *req.Approved {
			action = "job.approval.approved"
		}
		s.recordAudit("user", user.Username, action, "job", job.ID, map[string]any{
			"comment":     req.Comment,
			"source_type": job.SourceType,
			"source_id":   job.SourceID,
			"status":      job.Status,
		})
		writeJSON(w, http.StatusOK, job)
		return
	}

	writeError(w, http.StatusInternalServerError, errors.New("no store configured"))
}
