package api

import (
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
