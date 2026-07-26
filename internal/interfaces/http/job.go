package api

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"adp/internal/domain/model"
	"adp/internal/infrastructure/db"
	"adp/internal/infrastructure/scheduler"
)

type createJobRequest struct {
	Name       string   `json:"name"`
	WorkerType string   `json:"worker_type"`
	Command    string   `json:"command"`
	WorkerIDs  []string `json:"worker_ids,omitempty"`
}

func (s *Server) handleCreateJob(w http.ResponseWriter, r *http.Request) {
	var req createJobRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	if req.Name == "" || req.WorkerType == "" {
		writeError(w, http.StatusBadRequest, errors.New("name and worker_type are required"))
		return
	}

	opts := scheduler.CreateJobOptions{
		Status:         model.JobStatusPending,
		ApprovalStatus: model.ApprovalStatusNotRequired,
		SourceType:     "manual_job",
	}

	user := currentUser(r)

	if s.repo != nil {
		job := model.Job{
			Name:             req.Name,
			WorkerType:       req.WorkerType,
			Command:          req.Command,
			Status:           model.JobStatusPending,
			ApprovalStatus:   model.ApprovalStatusNotRequired,
			SourceType:       "manual_job",
			ApprovalRequired: false,
		}

		// If worker_ids specified, create and dispatch a copy for each worker.
		if len(req.WorkerIDs) > 0 {
			var assigned []model.Job
			for _, wid := range req.WorkerIDs {
				// Verify worker exists and type matches.
				wk, err := s.repo.GetWorker(wid)
				if err != nil {
					writeError(w, http.StatusBadRequest, fmt.Errorf("worker %s not found", wid))
					return
				}
				if !model.WorkerCanRunType(wk.WorkerType, req.WorkerType) {
					writeError(w, http.StatusBadRequest, fmt.Errorf("worker %s type %s cannot run job type %s", wid, wk.WorkerType, req.WorkerType))
					return
				}

				// Create a copy for this worker.
				workerJob := job
				workerJob.Name = fmt.Sprintf("[worker:%s] %s", wid, req.Name)
				workerJob.Status = model.JobStatusPending
				created, err := s.repo.CreateJob(workerJob)
				if err != nil {
					writeError(w, http.StatusInternalServerError, err)
					return
				}
				created, err = s.dispatchJobToWorker(created.ID, wid)
				if err != nil {
					writeError(w, http.StatusInternalServerError, err)
					return
				}

				// Push job immediately if worker has an active gRPC stream.
				if s.workerHub.PushJob(wid, created) {
					log.Printf("job %s pushed to worker %s via gRPC stream", created.ID, wid)
				}

				assigned = append(assigned, created)
			}
			s.recordAudit("user", user.Username, "job.created_and_assigned", "job", "", map[string]any{
				"worker_type": req.WorkerType,
				"worker_ids":  req.WorkerIDs,
				"job_count":   len(assigned),
			})
			writeJSON(w, http.StatusCreated, map[string]any{
				"jobs":       assigned,
				"total":      len(assigned),
				"worker_ids": req.WorkerIDs,
			})
			return
		}

		created, err := s.repo.CreateJob(job)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		s.recordAudit("user", user.Username, "job.created", "job", created.ID, map[string]any{
			"worker_type": req.WorkerType,
		})
		writeJSON(w, http.StatusCreated, created)
		return
	}

	// Fallback: use in-memory store.
	_ = opts
	writeError(w, http.StatusInternalServerError, errors.New("no store configured"))
}

func (s *Server) handleListJobs(w http.ResponseWriter, r *http.Request) {
	sourceType := strings.TrimSpace(r.URL.Query().Get("source_type"))
	workerType := strings.TrimSpace(r.URL.Query().Get("worker_type"))
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	limitValue := strings.TrimSpace(r.URL.Query().Get("limit"))

	if s.repo != nil {
		filter := db.JobFilter{
			SourceType: sourceType,
			WorkerType: workerType,
			Status:     status,
		}
		if limitValue != "" {
			if limit, err := strconv.Atoi(limitValue); err == nil && limit > 0 {
				filter.Limit = limit
			}
		}
		jobs, err := s.repo.ListJobs(filter)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, jobs)
		return
	}

	writeJSON(w, http.StatusOK, []model.Job{})
}

func (s *Server) handleGetJob(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/jobs/")
	if id == "" {
		writeError(w, http.StatusBadRequest, errors.New("job id is required"))
		return
	}

	if s.repo != nil {
		job, err := s.repo.GetJob(id)
		if err != nil {
			writeError(w, http.StatusNotFound, err)
			return
		}
		writeJSON(w, http.StatusOK, job)
		return
	}

	writeError(w, http.StatusNotFound, errors.New("job not found"))
}

// handleDeleteJob deletes a job. Only allowed for non-dispatched jobs.
func (s *Server) handleDeleteJob(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/jobs/")
	if id == "" {
		writeError(w, http.StatusBadRequest, errors.New("job id is required"))
		return
	}

	if s.repo != nil {
		if err := s.repo.DeleteJob(id); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		user := currentUser(r)
		s.recordAudit("user", user.Username, "job.deleted", "job", id, nil)
		writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
		return
	}

	writeError(w, http.StatusInternalServerError, errors.New("no store configured"))
}

type dispatchJobRequest struct {
	WorkerIDs []string `json:"worker_ids"`
}

func (s *Server) handleDispatchJob(w http.ResponseWriter, r *http.Request) {
	jobID := strings.TrimPrefix(r.URL.Path, "/api/v1/jobs/")
	jobID = strings.TrimSuffix(jobID, "/dispatch")
	if jobID == "" {
		writeError(w, http.StatusBadRequest, errors.New("job id is required"))
		return
	}

	var req dispatchJobRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if len(req.WorkerIDs) == 0 {
		writeError(w, http.StatusBadRequest, errors.New("worker_ids is required"))
		return
	}
	if s.repo == nil {
		writeError(w, http.StatusInternalServerError, errors.New("no store configured"))
		return
	}

	base, err := s.repo.GetJob(jobID)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	if base.Status != model.JobStatusPending && base.Status != model.JobStatusQueued {
		writeError(w, http.StatusBadRequest, fmt.Errorf("job status must be pending, got %s", base.Status))
		return
	}

	var dispatched []model.Job
	for i, wid := range req.WorkerIDs {
		worker, err := s.repo.GetWorker(wid)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("worker %s not found", wid))
			return
		}
		if !model.WorkerCanRunType(worker.WorkerType, base.WorkerType) {
			writeError(w, http.StatusBadRequest, fmt.Errorf("worker %s type %s cannot run job type %s", wid, worker.WorkerType, base.WorkerType))
			return
		}
		targetID := base.ID
		if i > 0 {
			clone := base
			clone.ID = ""
			clone.Name = fmt.Sprintf("[dispatch:%s][worker:%s] %s", base.ID, wid, base.Name)
			clone.Status = model.JobStatusPending
			clone.AssignedWorkerID = ""
			clone.Output = ""
			clone.StartedAt = nil
			clone.FinishedAt = nil
			clone.SourceID = base.ID
			if clone.SourceType == "" {
				clone.SourceType = "manual_dispatch"
			}
			created, err := s.repo.CreateJob(clone)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err)
				return
			}
			targetID = created.ID
		}

		job, err := s.dispatchJobToWorker(targetID, wid)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		s.workerHub.PushJob(wid, job)
		dispatched = append(dispatched, job)
	}

	user := currentUser(r)
	s.recordAudit("user", user.Username, "job.dispatched", "job", base.ID, map[string]any{
		"worker_ids": req.WorkerIDs,
		"job_count":  len(dispatched),
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"jobs":       dispatched,
		"total":      len(dispatched),
		"worker_ids": req.WorkerIDs,
	})
}

func (s *Server) dispatchJobToWorker(jobID, workerID string) (model.Job, error) {
	if err := s.repo.AssignJobToWorkers(jobID, []string{workerID}); err != nil {
		return model.Job{}, err
	}
	return s.repo.GetJob(jobID)
}

// YAMLJobSpec defines the format for a YAML-based batch job definition.
type YAMLJobSpec struct {
	Name       string     `yaml:"name"`
	Tasks      []YAMLTask `yaml:"tasks"`
	WorkerType string     `yaml:"worker_type"`
	Workers    []string   `yaml:"workers"`
}

// YAMLTask defines a single task within a YAML job spec.
type YAMLTask struct {
	Name       string            `yaml:"name"`
	Template   string            `yaml:"template"`
	Parameters map[string]string `yaml:"parameters"`
}

type yamlJobRequest struct {
	YAML string `json:"yaml"`
}

// handleCreateJobFromYAML creates jobs from a YAML definition.
func (s *Server) handleCreateJobFromYAML(w http.ResponseWriter, r *http.Request) {
	var req yamlJobRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	if req.YAML == "" {
		writeError(w, http.StatusBadRequest, errors.New("yaml payload is required"))
		return
	}

	var spec YAMLJobSpec
	if err := yaml.Unmarshal([]byte(req.YAML), &spec); err != nil {
		writeError(w, http.StatusBadRequest, errors.New("invalid yaml: "+err.Error()))
		return
	}
	if err := s.validateAndFixYAML(&spec); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	if s.repo == nil {
		writeError(w, http.StatusInternalServerError, errors.New("no store configured"))
		return
	}

	user := currentUser(r)
	var createdJobs []model.Job

	// Resolve worker list.
	workerIDs := spec.Workers
	if len(workerIDs) == 1 && strings.ToLower(workerIDs[0]) == "all" {
		allWorkers, _ := s.repo.ListWorkers()
		workerIDs = nil
		for _, w := range allWorkers {
			if model.WorkerCanRunType(w.WorkerType, spec.WorkerType) && w.Status == model.WorkerStatusOnline {
				workerIDs = append(workerIDs, w.ID)
			}
		}
	}

	for _, task := range spec.Tasks {
		// Render command from template.
		tmpl, cmd, err := s.templateEng.Render(task.Template, task.Parameters)
		if err != nil {
			writeError(w, http.StatusBadRequest, errors.New("task '"+task.Name+"': "+err.Error()))
			return
		}

		// Validate against policy.
		if err := s.policyEng.ValidateTemplate(task.Template); err != nil {
			writeError(w, http.StatusForbidden, errors.New("task '"+task.Name+"': "+err.Error()))
			return
		}
		if err := s.policyEng.ValidateCommand(cmd); err != nil {
			writeError(w, http.StatusForbidden, errors.New("task '"+task.Name+"': "+err.Error()))
			return
		}

		// If workers specified, create a copy per worker.
		targets := workerIDs
		if len(targets) == 0 {
			targets = []string{""} // single job, queued for any worker to pick up
		}

		for _, wid := range targets {
			jobStatus := model.JobStatusPending
			assignedName := fmt.Sprintf("[yaml:%s] %s", spec.Name, task.Name)
			if wid != "" {
				assignedName = fmt.Sprintf("[yaml:%s][worker:%s] %s", spec.Name, wid, task.Name)
			}

			job := model.Job{
				Name:             assignedName,
				WorkerType:       spec.WorkerType,
				Command:          cmd,
				Status:           jobStatus,
				RiskLevel:        tmpl.RiskLevel,
				ApprovalRequired: false,
				ApprovalStatus:   model.ApprovalStatusNotRequired,
				TemplateCode:     task.Template,
				Parameters:       cloneStringMap(task.Parameters),
				SourceType:       "yaml_job",
			}

			created, err := s.repo.CreateJob(job)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err)
				return
			}

			if wid != "" {
				created, err = s.dispatchJobToWorker(created.ID, wid)
				if err != nil {
					writeError(w, http.StatusInternalServerError, err)
					return
				}
				s.workerHub.PushJob(wid, created)
			}

			createdJobs = append(createdJobs, created)
		}
	}

	for _, j := range createdJobs {
		s.recordAudit("user", user.Username, "yaml_job.created", "job", j.ID, map[string]any{
			"worker_type": spec.WorkerType,
			"template":    j.TemplateCode,
		})
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"jobs":         createdJobs,
		"total":        len(createdJobs),
		"worker_count": len(workerIDs),
	})
}

// handleAddWorkerLog accepts worker execution logs.
func (s *Server) handleAddWorkerLog(w http.ResponseWriter, r *http.Request) {
	var entry model.WorkerLog
	if err := decodeJSON(r, &entry); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	if s.repo != nil {
		if err := s.repo.AddWorkerLog(entry); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]string{"status": "logged"})
		return
	}

	writeJSON(w, http.StatusCreated, map[string]string{"status": "ok"})
}

// Ensure sort import is used by the fallback code path.
var _ = sort.Slice
