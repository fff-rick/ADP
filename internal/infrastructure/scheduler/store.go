package scheduler

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"adp/internal/domain/model"
)

type Store struct {
	mu             sync.RWMutex
	workers        map[string]model.Worker
	jobs           map[string]model.Job
	auditLogs      []model.AuditLog
	incidentCases  map[string]model.IncidentCase
	incidentByPlan map[string]string
	nextID         atomic.Uint64
}

const workerOnlineThreshold = 30 * time.Second

type CreateJobOptions struct {
	Status           model.JobStatus
	RiskLevel        model.RiskLevel
	ApprovalRequired bool
	ApprovalStatus   model.ApprovalStatus
	ApprovalComment  string
	TemplateCode     string
	Parameters       map[string]string
	SourceType       string
	SourceID         string
	AssignedWorkerID string
}

func NewStore() *Store {
	return &Store{
		workers:        make(map[string]model.Worker),
		jobs:           make(map[string]model.Job),
		incidentCases:  make(map[string]model.IncidentCase),
		incidentByPlan: make(map[string]string),
	}
}

func (s *Store) RegisterWorker(name, workerType string) model.Worker {
	now := time.Now()
	worker := model.Worker{
		ID:              s.newID("worker"),
		Name:            name,
		WorkerType:      workerType,
		Status:          model.WorkerStatusOnline,
		LastHeartbeatAt: now,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.workers[worker.ID] = worker

	return worker
}

func (s *Store) HeartbeatWorker(id string) (model.Worker, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	worker, ok := s.workers[id]
	if !ok {
		return model.Worker{}, false
	}

	now := time.Now()
	worker.LastHeartbeatAt = now
	worker.Status = model.WorkerStatusOnline
	worker.UpdatedAt = now
	s.workers[id] = worker

	return worker, true
}

func (s *Store) ListWorkers() []model.Worker {
	s.mu.RLock()
	defer s.mu.RUnlock()

	workers := make([]model.Worker, 0, len(s.workers))
	for _, worker := range s.workers {
		workers = append(workers, worker)
	}
	return workers
}

func (s *Store) CreateJob(name, workerType, command string) model.Job {
	return s.CreateJobWithOptions(name, workerType, command, CreateJobOptions{})
}

func (s *Store) CreateJobWithOptions(name, workerType, command string, opts CreateJobOptions) model.Job {
	now := time.Now()
	status := opts.Status
	if status == "" {
		status = model.JobStatusPending
	}
	approvalStatus := opts.ApprovalStatus
	if opts.ApprovalRequired && approvalStatus == "" {
		approvalStatus = model.ApprovalStatusPending
	}
	if !opts.ApprovalRequired && approvalStatus == "" {
		approvalStatus = model.ApprovalStatusNotRequired
	}

	job := model.Job{
		ID:               s.newID("job"),
		Name:             name,
		WorkerType:       workerType,
		Command:          command,
		Status:           status,
		RiskLevel:        opts.RiskLevel,
		ApprovalRequired: opts.ApprovalRequired,
		ApprovalStatus:   approvalStatus,
		ApprovalComment:  opts.ApprovalComment,
		TemplateCode:     opts.TemplateCode,
		Parameters:       cloneStringMap(opts.Parameters),
		SourceType:       opts.SourceType,
		SourceID:         opts.SourceID,
		AssignedWorkerID: opts.AssignedWorkerID,
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs[job.ID] = job

	return job
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func (s *Store) ListJobs() []model.Job {
	s.mu.RLock()
	defer s.mu.RUnlock()

	jobs := make([]model.Job, 0, len(s.jobs))
	for _, job := range s.jobs {
		jobs = append(jobs, job)
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].CreatedAt.After(jobs[j].CreatedAt) })
	return jobs
}

func (s *Store) GetJob(id string) (model.Job, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	job, ok := s.jobs[id]
	return job, ok
}

func (s *Store) AssignNextJob(workerID string) (model.Job, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	worker, ok := s.workers[workerID]
	if !ok {
		return model.Job{}, false
	}

	var (
		selectedID  string
		selectedJob model.Job
		found       bool
	)

	for id, job := range s.jobs {
		if (job.Status != model.JobStatusQueued && job.Status != model.JobStatusPending) || !model.WorkerCanRunType(worker.WorkerType, job.WorkerType) {
			continue
		}

		if !found || job.CreatedAt.Before(selectedJob.CreatedAt) || (job.CreatedAt.Equal(selectedJob.CreatedAt) && job.ID < selectedJob.ID) {
			selectedID = id
			selectedJob = job
			found = true
		}
	}

	if !found {
		return model.Job{}, false
	}

	now := time.Now()
	selectedJob.Status = model.JobStatusRunning
	selectedJob.AssignedWorkerID = workerID
	selectedJob.StartedAt = &now
	selectedJob.UpdatedAt = now
	s.jobs[selectedID] = selectedJob

	return selectedJob, true
}

func (s *Store) CompleteJob(workerID, jobID, output string, success bool) (model.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	job, ok := s.jobs[jobID]
	if !ok {
		return model.Job{}, fmt.Errorf("job not found")
	}

	if job.AssignedWorkerID != workerID {
		return model.Job{}, fmt.Errorf("job is not assigned to worker")
	}

	now := time.Now()
	job.Output = output
	job.FinishedAt = &now
	job.UpdatedAt = now
	if success {
		job.Status = model.JobStatusSuccess
	} else {
		job.Status = model.JobStatusFailed
	}

	s.jobs[jobID] = job
	return job, nil
}

func (s *Store) AssignJobToWorker(jobID, workerID string) (model.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	worker, ok := s.workers[workerID]
	if !ok {
		return model.Job{}, fmt.Errorf("worker not found")
	}

	job, ok := s.jobs[jobID]
	if !ok {
		return model.Job{}, fmt.Errorf("job not found")
	}
	if !model.WorkerCanRunType(worker.WorkerType, job.WorkerType) {
		return model.Job{}, fmt.Errorf("worker type does not match job type")
	}

	now := time.Now()
	job.Status = model.JobStatusRunning
	job.AssignedWorkerID = workerID
	job.StartedAt = &now
	job.UpdatedAt = now
	s.jobs[jobID] = job
	return job, nil
}

func (s *Store) ListPendingApprovalJobs() []model.Job {
	s.mu.RLock()
	defer s.mu.RUnlock()

	jobs := make([]model.Job, 0)
	for _, job := range s.jobs {
		if job.Status == model.JobStatusWaitingApproval {
			jobs = append(jobs, job)
		}
	}
	return jobs
}

func (s *Store) ApproveJob(jobID, approvedBy, comment string) (model.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	job, ok := s.jobs[jobID]
	if !ok {
		return model.Job{}, fmt.Errorf("job not found")
	}
	if job.Status != model.JobStatusWaitingApproval {
		return model.Job{}, fmt.Errorf("job is not waiting for approval")
	}

	now := time.Now()
	job.Status = model.JobStatusPending
	job.ApprovalStatus = model.ApprovalStatusApproved
	job.ApprovedBy = approvedBy
	job.ApprovedAt = &now
	job.ApprovalComment = comment
	job.UpdatedAt = now
	s.jobs[jobID] = job

	return job, nil
}

func (s *Store) RejectJob(jobID, rejectedBy, reason string) (model.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	job, ok := s.jobs[jobID]
	if !ok {
		return model.Job{}, fmt.Errorf("job not found")
	}
	if job.Status != model.JobStatusWaitingApproval {
		return model.Job{}, fmt.Errorf("job is not waiting for approval")
	}

	now := time.Now()
	job.Status = model.JobStatusCancelled
	job.ApprovalStatus = model.ApprovalStatusRejected
	job.RejectedBy = rejectedBy
	job.RejectedAt = &now
	job.ApprovalComment = reason
	job.UpdatedAt = now
	s.jobs[jobID] = job

	return job, nil
}

func (s *Store) AddAuditLog(actorType, actorID, action, resourceType, resourceID string, details map[string]any) model.AuditLog {
	entry := model.AuditLog{
		ID:           s.newID("audit"),
		ActorType:    actorType,
		ActorID:      actorID,
		Action:       action,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		Details:      details,
		CreatedAt:    time.Now(),
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.auditLogs = append(s.auditLogs, entry)

	return entry
}

func (s *Store) ListAuditLogs(resourceType, resourceID string) []model.AuditLog {
	s.mu.RLock()
	defer s.mu.RUnlock()

	logs := make([]model.AuditLog, 0, len(s.auditLogs))
	for _, entry := range s.auditLogs {
		if resourceType != "" && entry.ResourceType != resourceType {
			continue
		}
		if resourceID != "" && entry.ResourceID != resourceID {
			continue
		}
		logs = append(logs, entry)
	}
	return logs
}

func (s *Store) UpsertIncidentCase(plan model.DiagnosisPlan, report model.AnalysisReport) model.IncidentCase {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	caseID := s.incidentByPlan[plan.ID]
	incidentCase, ok := s.incidentCases[caseID]
	if !ok {
		incidentCase = model.IncidentCase{
			ID:        s.newID("case"),
			CreatedAt: now,
		}
	}

	incidentCase.Title = plan.Title
	incidentCase.TriggerType = plan.TriggerType
	incidentCase.FaultType = report.FaultType
	incidentCase.Summary = summarizeCase(report)
	incidentCase.PossibleCauses = cloneStrings(report.PossibleCauses)
	incidentCase.Suggestions = cloneStrings(report.Suggestions)
	incidentCase.Confidence = report.Confidence
	incidentCase.SourcePlanID = plan.ID
	incidentCase.UpdatedAt = now

	s.incidentCases[incidentCase.ID] = incidentCase
	s.incidentByPlan[plan.ID] = incidentCase.ID
	return incidentCase
}

func (s *Store) ListIncidentCases(filter model.IncidentCaseFilter) []model.IncidentCase {
	s.mu.RLock()
	defer s.mu.RUnlock()

	cases := make([]model.IncidentCase, 0, len(s.incidentCases))
	for _, incidentCase := range s.incidentCases {
		if !matchIncidentCase(incidentCase, filter) {
			continue
		}
		cases = append(cases, incidentCase)
	}

	sort.Slice(cases, func(i, j int) bool {
		return cases[i].UpdatedAt.After(cases[j].UpdatedAt)
	})

	if filter.Limit > 0 && len(cases) > filter.Limit {
		cases = cases[:filter.Limit]
	}
	return cases
}

func (s *Store) FindSimilarIncidentCases(description, triggerType, faultType string, limit int) []model.IncidentCase {
	s.mu.RLock()
	defer s.mu.RUnlock()

	type scoredCase struct {
		caseItem model.IncidentCase
		score    int
	}

	var scored []scoredCase
	for _, incidentCase := range s.incidentCases {
		score := incidentCaseScore(incidentCase, description, triggerType, faultType)
		if score <= 0 {
			continue
		}
		scored = append(scored, scoredCase{
			caseItem: incidentCase,
			score:    score,
		})
	}

	sort.Slice(scored, func(i, j int) bool {
		if scored[i].score == scored[j].score {
			return scored[i].caseItem.UpdatedAt.After(scored[j].caseItem.UpdatedAt)
		}
		return scored[i].score > scored[j].score
	})

	if limit <= 0 {
		limit = 3
	}
	if len(scored) > limit {
		scored = scored[:limit]
	}

	result := make([]model.IncidentCase, 0, len(scored))
	for _, item := range scored {
		result = append(result, item.caseItem)
	}
	return result
}

func (s *Store) MetricsSnapshot() model.MetricsSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	now := time.Now()
	snapshot := model.MetricsSnapshot{
		JobsTotal:          len(s.jobs),
		IncidentCasesTotal: len(s.incidentCases),
	}

	var completedJobs int
	var totalScheduleLatencySeconds float64
	var scheduleLatencyCount int

	for _, worker := range s.workers {
		if worker.Status == model.WorkerStatusOnline && now.Sub(worker.LastHeartbeatAt) <= workerOnlineThreshold {
			snapshot.WorkersOnline++
		}
	}

	for _, job := range s.jobs {
		switch job.Status {
		case model.JobStatusSuccess:
			snapshot.JobsSuccess++
			completedJobs++
		case model.JobStatusFailed:
			snapshot.JobsFailed++
			completedJobs++
		case model.JobStatusCancelled:
			completedJobs++
		case model.JobStatusWaitingApproval:
			snapshot.JobsWaitingApproval++
		}

		if job.StartedAt != nil {
			totalScheduleLatencySeconds += job.StartedAt.Sub(job.CreatedAt).Seconds()
			scheduleLatencyCount++
		}
	}

	if completedJobs > 0 {
		snapshot.JobSuccessRate = float64(snapshot.JobsSuccess) / float64(completedJobs)
		snapshot.JobFailureRate = float64(snapshot.JobsFailed) / float64(completedJobs)
	}
	if scheduleLatencyCount > 0 {
		snapshot.AvgScheduleLatencySeconds = totalScheduleLatencySeconds / float64(scheduleLatencyCount)
	}

	return snapshot
}

func matchIncidentCase(incidentCase model.IncidentCase, filter model.IncidentCaseFilter) bool {
	if filter.TriggerType != "" && incidentCase.TriggerType != filter.TriggerType {
		return false
	}
	if filter.FaultType != "" && incidentCase.FaultType != filter.FaultType {
		return false
	}
	if filter.Query == "" {
		return true
	}

	query := strings.ToLower(filter.Query)
	haystack := incidentCaseSearchText(incidentCase)
	return strings.Contains(haystack, query)
}

func incidentCaseScore(incidentCase model.IncidentCase, description, triggerType, faultType string) int {
	score := 0
	if triggerType != "" && incidentCase.TriggerType == triggerType {
		score += 3
	}
	if faultType != "" && strings.EqualFold(incidentCase.FaultType, faultType) {
		score += 4
	}

	haystack := incidentCaseSearchText(incidentCase)
	for _, token := range tokenizeSearch(description) {
		if strings.Contains(haystack, token) {
			score++
		}
	}

	return score
}

func incidentCaseSearchText(incidentCase model.IncidentCase) string {
	parts := []string{
		incidentCase.Title,
		incidentCase.TriggerType,
		incidentCase.FaultType,
		incidentCase.Summary,
		strings.Join(incidentCase.PossibleCauses, " "),
		strings.Join(incidentCase.Suggestions, " "),
	}
	return strings.ToLower(strings.Join(parts, " "))
}

func tokenizeSearch(input string) []string {
	input = strings.ToLower(strings.TrimSpace(input))
	if input == "" {
		return nil
	}

	replacer := strings.NewReplacer(",", " ", ".", " ", ";", " ", ":", " ", "(", " ", ")", " ", "\n", " ", "\t", " ")
	input = replacer.Replace(input)
	parts := strings.Fields(input)

	tokens := make([]string, 0, len(parts))
	seen := make(map[string]bool, len(parts))
	for _, part := range parts {
		if len(part) < 2 || seen[part] {
			continue
		}
		seen[part] = true
		tokens = append(tokens, part)
	}
	return tokens
}

func summarizeCase(report model.AnalysisReport) string {
	if len(report.PossibleCauses) > 0 {
		return strings.Join(report.PossibleCauses, "; ")
	}
	return report.RawAnalysis
}

func cloneStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	cloned := make([]string, len(values))
	copy(cloned, values)
	return cloned
}

func (s *Store) newID(prefix string) string {
	value := s.nextID.Add(1)
	return fmt.Sprintf("%s-%06d", prefix, value)
}
