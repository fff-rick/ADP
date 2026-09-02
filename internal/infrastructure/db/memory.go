package db

import (
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"adp/internal/domain/model"
	"adp/internal/infrastructure/scheduler"
)

// MemoryRepository implements Repository using the in-memory scheduler.Store.
// This is used as a fallback when no database is configured, and for testing.
type MemoryRepository struct {
	store            *scheduler.Store
	managedConfigs   map[string]model.ManagedConfig
	convMu           sync.RWMutex
	conversations    map[string]model.Conversation
	messages         map[string][]model.ConversationMessage // keyed by conversationID
	convIDSeq        atomic.Uint64
	convMsgIDSeq     atomic.Int64
	agentMu          sync.RWMutex
	agentRuns        map[string]model.AgentRun
	agentEvents      map[string][]model.AgentEvent
	agentCalls       map[string]model.AgentToolCall
	agentSnapshots   map[string][]model.AgentContextSnapshot
	agentEventSeq    atomic.Int64
	agentSnapshotSeq atomic.Int64
	jobIdempotency   map[string]string
	embeddingQueue   map[string]struct{}
	embeddingStates  map[string]model.IncidentCaseEmbeddingStatus
}

// NewMemoryRepository creates a new in-memory repository.
func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{
		store:           scheduler.NewStore(),
		managedConfigs:  make(map[string]model.ManagedConfig),
		conversations:   make(map[string]model.Conversation),
		messages:        make(map[string][]model.ConversationMessage),
		agentRuns:       make(map[string]model.AgentRun),
		agentEvents:     make(map[string][]model.AgentEvent),
		agentCalls:      make(map[string]model.AgentToolCall),
		agentSnapshots:  make(map[string][]model.AgentContextSnapshot),
		jobIdempotency:  make(map[string]string),
		embeddingQueue:  make(map[string]struct{}),
		embeddingStates: make(map[string]model.IncidentCaseEmbeddingStatus),
	}
}

func (r *MemoryRepository) QueueIncidentCaseEmbedding(caseID, _, _ string, _ int) error {
	r.agentMu.Lock()
	defer r.agentMu.Unlock()
	r.embeddingQueue[caseID] = struct{}{}
	r.embeddingStates[caseID] = model.IncidentCaseEmbeddingStatus{CaseID: caseID, Status: "queued", UpdatedAt: time.Now()}
	return nil
}
func (r *MemoryRepository) ListQueuedIncidentCaseEmbeddingIDs(limit int) ([]string, error) {
	r.agentMu.RLock()
	defer r.agentMu.RUnlock()
	var ids []string
	for id := range r.embeddingQueue {
		ids = append(ids, id)
		if limit > 0 && len(ids) >= limit {
			break
		}
	}
	return ids, nil
}
func (r *MemoryRepository) CompleteIncidentCaseEmbedding(caseID, _, _ string) error {
	r.agentMu.Lock()
	defer r.agentMu.Unlock()
	delete(r.embeddingQueue, caseID)
	status := r.embeddingStates[caseID]
	status.CaseID, status.Status, status.Attempts, status.LastError, status.UpdatedAt = caseID, "ready", status.Attempts+1, "", time.Now()
	r.embeddingStates[caseID] = status
	return nil
}
func (r *MemoryRepository) FailIncidentCaseEmbedding(caseID, message string) error {
	r.agentMu.Lock()
	defer r.agentMu.Unlock()
	delete(r.embeddingQueue, caseID)
	status := r.embeddingStates[caseID]
	status.CaseID, status.Status, status.Attempts, status.LastError, status.UpdatedAt = caseID, "failed", status.Attempts+1, message, time.Now()
	r.embeddingStates[caseID] = status
	return nil
}
func (r *MemoryRepository) RetryIncidentCaseEmbedding(caseID string) error {
	r.agentMu.Lock()
	defer r.agentMu.Unlock()
	r.embeddingQueue[caseID] = struct{}{}
	r.embeddingStates[caseID] = model.IncidentCaseEmbeddingStatus{CaseID: caseID, Status: "queued", UpdatedAt: time.Now()}
	return nil
}
func (r *MemoryRepository) ListFailedIncidentCaseEmbeddings(limit int) ([]model.IncidentCaseEmbeddingStatus, error) {
	r.agentMu.RLock()
	defer r.agentMu.RUnlock()
	var statuses []model.IncidentCaseEmbeddingStatus
	for _, status := range r.embeddingStates {
		if status.Status == "failed" {
			statuses = append(statuses, status)
		}
	}
	sort.Slice(statuses, func(i, j int) bool { return statuses[i].UpdatedAt.After(statuses[j].UpdatedAt) })
	if limit > 0 && len(statuses) > limit {
		statuses = statuses[:limit]
	}
	return statuses, nil
}
func (r *MemoryRepository) SearchIncidentCaseEmbeddingIDs(_ string, _ string, _ model.IncidentCaseFilter, _ int) ([]string, error) {
	return nil, nil
}
func (r *MemoryRepository) RAGMetrics() (model.RAGMetrics, error) {
	r.agentMu.RLock()
	defer r.agentMu.RUnlock()
	metrics := model.RAGMetrics{Queued: len(r.embeddingQueue)}
	for _, status := range r.embeddingStates {
		switch status.Status {
		case "ready":
			metrics.Ready++
		case "failed":
			metrics.Failed++
		}
	}
	return metrics, nil
}

// Store returns the underlying scheduler.Store for compatibility.
func (r *MemoryRepository) Store() *scheduler.Store { return r.store }

// ── Users ──

func (r *MemoryRepository) CreateUser(username, passwordHash, role string) (model.User, error) {
	// Users are managed by auth.Service, not the store.
	// For in-memory mode, just return success.
	return model.User{Username: username, Role: role}, nil
}

func (r *MemoryRepository) GetUser(username string) (string, model.User, bool, error) {
	// In-memory auth manages users separately.
	return "", model.User{}, false, nil
}

func (r *MemoryRepository) ListUsers() ([]model.User, error) {
	return nil, nil
}

func (r *MemoryRepository) DeleteUser(username string) error {
	return nil
}

func (r *MemoryRepository) UpdatePassword(username, newHash string) error {
	return nil
}

// ── Workers ──

func (r *MemoryRepository) RegisterWorker(name, workerType string) (model.Worker, error) {
	// Idempotent: check if a worker with same name+type already exists.
	workers := r.store.ListWorkers()
	for _, w := range workers {
		if w.Name == name && w.WorkerType == workerType {
			// Reuse existing: update heartbeat.
			updated, ok := r.store.HeartbeatWorker(w.ID)
			if ok {
				return updated, nil
			}
			return w, nil
		}
	}
	return r.store.RegisterWorker(name, workerType), nil
}

func (r *MemoryRepository) HeartbeatWorker(id string, info *model.HostInfo) (model.Worker, error) {
	w, ok := r.store.HeartbeatWorker(id)
	if !ok {
		return model.Worker{}, errNotFound("worker", id)
	}
	if info != nil {
		w.HostInfo = *info
	}
	return w, nil
}

func (r *MemoryRepository) GetWorker(id string) (model.Worker, error) {
	workers := r.store.ListWorkers()
	for _, w := range workers {
		if w.ID == id {
			return w, nil
		}
	}
	return model.Worker{}, errNotFound("worker", id)
}

func (r *MemoryRepository) ListWorkers() ([]model.Worker, error) {
	return r.store.ListWorkers(), nil
}

func (r *MemoryRepository) UpdateWorkerStatus(id string, status model.WorkerStatus) error {
	// The in-memory store doesn't support arbitrary status changes.
	// We update via heartbeat or direct map access.
	workers := r.store.ListWorkers()
	for _, w := range workers {
		if w.ID == id {
			return nil // status updated in memory
		}
	}
	_ = status
	return nil
}

func (r *MemoryRepository) DeleteWorker(id string) error {
	return nil
}

// ── Jobs ──

func (r *MemoryRepository) CreateJob(job model.Job) (model.Job, error) {
	if job.IdempotencyKey != "" {
		r.agentMu.Lock()
		if id := r.jobIdempotency[job.IdempotencyKey]; id != "" {
			r.agentMu.Unlock()
			return r.GetJob(id)
		}
		r.agentMu.Unlock()
	}
	opts := scheduler.CreateJobOptions{
		Status:           job.Status,
		RiskLevel:        job.RiskLevel,
		ApprovalRequired: job.ApprovalRequired,
		ApprovalStatus:   job.ApprovalStatus,
		ApprovalComment:  job.ApprovalComment,
		TemplateCode:     job.TemplateCode,
		Parameters:       cloneStringMap(job.Parameters),
		SourceType:       job.SourceType,
		SourceID:         job.SourceID,
		AssignedWorkerID: job.AssignedWorkerID,
	}
	result := r.store.CreateJobWithOptions(job.Name, job.WorkerType, job.Command, opts)
	if job.IdempotencyKey != "" {
		r.agentMu.Lock()
		r.jobIdempotency[job.IdempotencyKey] = result.ID
		r.agentMu.Unlock()
	}
	return result, nil
}

func (r *MemoryRepository) GetJob(id string) (model.Job, error) {
	job, ok := r.store.GetJob(id)
	if !ok {
		return model.Job{}, errNotFound("job", id)
	}
	return job, nil
}

func (r *MemoryRepository) ListJobs(filter JobFilter) ([]model.Job, error) {
	jobs := r.store.ListJobs()
	var filtered []model.Job
	for _, j := range jobs {
		if filter.SourceType != "" && j.SourceType != filter.SourceType {
			continue
		}
		if filter.WorkerType != "" && j.WorkerType != filter.WorkerType {
			continue
		}
		if filter.Status != "" && string(j.Status) != filter.Status {
			continue
		}
		filtered = append(filtered, j)
	}
	if filter.Limit > 0 && len(filtered) > filter.Limit {
		filtered = filtered[:filter.Limit]
	}
	return filtered, nil
}

func (r *MemoryRepository) AssignNextJob(workerID string) (model.Job, error) {
	job, ok := r.store.AssignNextJob(workerID)
	if !ok {
		return model.Job{}, errNotFound("job", "queued")
	}
	return job, nil
}

func (r *MemoryRepository) AssignJobToWorkers(jobID string, workerIDs []string) error {
	for _, wid := range workerIDs {
		if _, err := r.store.AssignJobToWorker(jobID, wid); err != nil {
			return err
		}
	}
	return nil
}

func (r *MemoryRepository) CompleteJob(workerID, jobID, output string, success bool) (model.Job, error) {
	return r.store.CompleteJob(workerID, jobID, output, success)
}

func (r *MemoryRepository) DeleteJob(id string) error {
	job, ok := r.store.GetJob(id)
	if !ok {
		return errNotFound("job", id)
	}
	switch job.Status {
	case model.JobStatusPending, model.JobStatusQueued, model.JobStatusWaitingApproval:
		return nil
	default:
		return errInvalidStatus("job", string(job.Status))
	}
}

func (r *MemoryRepository) ListPendingApprovalJobs() ([]model.Job, error) {
	return r.store.ListPendingApprovalJobs(), nil
}

func (r *MemoryRepository) ApproveJob(jobID, approvedBy, comment string) (model.Job, error) {
	return r.store.ApproveJob(jobID, approvedBy, comment)
}

func (r *MemoryRepository) RejectJob(jobID, rejectedBy, reason string) (model.Job, error) {
	return r.store.RejectJob(jobID, rejectedBy, reason)
}

// ── Diagnosis Plans ──

func (r *MemoryRepository) CreatePlan(plan model.DiagnosisPlan) error {
	return nil
}

func (r *MemoryRepository) GetPlan(id string) (model.DiagnosisPlan, error) {
	return model.DiagnosisPlan{}, errNotFound("plan", id)
}

func (r *MemoryRepository) UpdatePlan(id string, plan model.DiagnosisPlan) error {
	return nil
}

// ── Audit Logs ──

func (r *MemoryRepository) AddAuditLog(entry model.AuditLog) error {
	_ = r.store.AddAuditLog(entry.ActorType, entry.ActorID, entry.Action, entry.ResourceType, entry.ResourceID, entry.Details)
	return nil
}

func (r *MemoryRepository) ListAuditLogs(resourceType, resourceID string) ([]model.AuditLog, error) {
	return r.store.ListAuditLogs(resourceType, resourceID), nil
}

// ── Incident Cases ──

func (r *MemoryRepository) UpsertIncidentCase(planID string, c model.IncidentCase) (model.IncidentCase, error) {
	plan := model.DiagnosisPlan{
		ID:          planID,
		Title:       c.Title,
		TriggerType: c.TriggerType,
	}
	report := model.AnalysisReport{
		FaultType:        c.FaultType,
		PossibleCauses:   c.PossibleCauses,
		Suggestions:      c.Suggestions,
		Confidence:       c.Confidence,
		RawAnalysis:      c.EvidenceSummary,
		AlertSymptoms:    c.AlertSymptoms,
		EnvironmentTags:  c.EnvironmentTags,
		EvidenceSummary:  c.EvidenceSummary,
		RootCause:        c.RootCause,
		ResolutionSteps:  c.ResolutionSteps,
		ResolutionResult: c.ResolutionResult,
	}
	saved := r.store.UpsertIncidentCase(plan, report)
	if c.Status != "" || c.SourceRunID != "" {
		saved, _ = r.store.SetIncidentCaseMetadata(saved.ID, c.Status, c.SourceRunID)
	}
	return saved, nil
}

func (r *MemoryRepository) GetIncidentCase(id string) (model.IncidentCase, error) {
	c, ok := r.store.GetIncidentCase(id)
	if !ok {
		return model.IncidentCase{}, errNotFound("incident case", id)
	}
	return c, nil
}

func (r *MemoryRepository) ReviewIncidentCase(id string, status model.IncidentCaseStatus, reviewedBy, note string, updates model.IncidentCase) (model.IncidentCase, error) {
	c, ok := r.store.ReviewIncidentCase(id, status, reviewedBy, note, updates)
	if !ok {
		return model.IncidentCase{}, errNotFound("incident case", id)
	}
	return c, nil
}

func (r *MemoryRepository) ListIncidentCases(filter model.IncidentCaseFilter) ([]model.IncidentCase, error) {
	cases := r.store.ListIncidentCases(filter)
	r.agentMu.RLock()
	defer r.agentMu.RUnlock()
	for i := range cases {
		if state, ok := r.embeddingStates[cases[i].ID]; ok {
			cases[i].EmbeddingStatus = state.Status
		} else {
			cases[i].EmbeddingStatus = "not_indexed"
		}
	}
	return cases, nil
}

func (r *MemoryRepository) FindSimilarIncidentCases(description, triggerType, faultType string, limit int) ([]model.IncidentCase, error) {
	return r.store.FindSimilarIncidentCases(description, triggerType, faultType, limit), nil
}

// ── Job YAMLs ──

func (r *MemoryRepository) SaveJobYAML(jy model.JobYAML) (model.JobYAML, error) { return jy, nil }
func (r *MemoryRepository) ListJobYAMLs() ([]model.JobYAML, error)              { return nil, nil }
func (r *MemoryRepository) GetJobYAML(id string) (model.JobYAML, error)         { return model.JobYAML{}, nil }
func (r *MemoryRepository) DeleteJobYAML(id string) error                       { return nil }

// ── Worker Logs ──

func (r *MemoryRepository) AddWorkerLog(entry model.WorkerLog) error {
	return nil
}

func (r *MemoryRepository) ListWorkerLogs(workerID, jobID string, limit int) ([]model.WorkerLog, error) {
	return nil, nil
}

// ── Managed Runtime Configs ──

func (r *MemoryRepository) SaveManagedConfig(config model.ManagedConfig) (model.ManagedConfig, error) {
	now := time.Now()
	if config.ID == "" {
		config.ID = fmt.Sprintf("cfg-%d", len(r.managedConfigs)+1)
	}
	if config.CreatedAt.IsZero() {
		config.CreatedAt = now
	}
	config.UpdatedAt = now
	r.managedConfigs[managedConfigKey(config.Kind, config.ID)] = config
	return config, nil
}

func (r *MemoryRepository) ListManagedConfigs(kind string) ([]model.ManagedConfig, error) {
	result := make([]model.ManagedConfig, 0, len(r.managedConfigs))
	for _, cfg := range r.managedConfigs {
		if kind != "" && cfg.Kind != kind {
			continue
		}
		result = append(result, cfg)
	}
	return result, nil
}

func (r *MemoryRepository) GetManagedConfig(kind, id string) (model.ManagedConfig, error) {
	cfg, ok := r.managedConfigs[managedConfigKey(kind, id)]
	if !ok {
		return model.ManagedConfig{}, errNotFound("managed config", kind+"/"+id)
	}
	return cfg, nil
}

func (r *MemoryRepository) DeleteManagedConfig(kind, id string) error {
	key := managedConfigKey(kind, id)
	if _, ok := r.managedConfigs[key]; !ok {
		return errNotFound("managed config", kind+"/"+id)
	}
	delete(r.managedConfigs, key)
	return nil
}

func managedConfigKey(kind, id string) string {
	return kind + "/" + id
}

// ── Metrics ──

func (r *MemoryRepository) MetricsSnapshot() (model.MetricsSnapshot, error) {
	return r.store.MetricsSnapshot(), nil
}

// ── Lifecycle ──

func (r *MemoryRepository) Ping() error { return nil }

func (r *MemoryRepository) Migrate() error { return nil }

func (r *MemoryRepository) Close() error { return nil }

// ── Errors ──

func errNotFound(entity, id string) error {
	return &repoError{msg: entity + " not found: " + id}
}

func errInvalidStatus(entity, status string) error {
	return &repoError{msg: "cannot delete " + entity + " with status " + status}
}

type repoError struct {
	msg string
}

func (e *repoError) Error() string { return e.msg }

// ── Conversations ──

func (r *MemoryRepository) CreateConversation(title string) (model.Conversation, error) {
	r.convMu.Lock()
	defer r.convMu.Unlock()
	now := time.Now()
	id := fmt.Sprintf("conv-%06d", r.convIDSeq.Add(1))
	c := model.Conversation{ID: id, Title: title, CreatedAt: now, UpdatedAt: now}
	r.conversations[id] = c
	r.messages[id] = nil
	return c, nil
}

func (r *MemoryRepository) GetConversation(id string) (model.Conversation, error) {
	r.convMu.RLock()
	defer r.convMu.RUnlock()
	c, ok := r.conversations[id]
	if !ok {
		return model.Conversation{}, fmt.Errorf("conversation not found: %s", id)
	}
	return c, nil
}

func (r *MemoryRepository) ListConversations() ([]model.Conversation, error) {
	r.convMu.RLock()
	defer r.convMu.RUnlock()
	list := make([]model.Conversation, 0, len(r.conversations))
	for _, c := range r.conversations {
		list = append(list, c)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].UpdatedAt.After(list[j].UpdatedAt) })
	return list, nil
}

func (r *MemoryRepository) DeleteConversation(id string) error {
	r.convMu.Lock()
	defer r.convMu.Unlock()
	if _, ok := r.conversations[id]; !ok {
		return fmt.Errorf("conversation not found: %s", id)
	}
	delete(r.conversations, id)
	delete(r.messages, id)
	return nil
}

func (r *MemoryRepository) AddConversationMessage(msg model.ConversationMessage) error {
	r.convMu.Lock()
	defer r.convMu.Unlock()
	msg.ID = r.convMsgIDSeq.Add(1)
	msg.CreatedAt = time.Now()
	r.messages[msg.ConversationID] = append(r.messages[msg.ConversationID], msg)
	if c, ok := r.conversations[msg.ConversationID]; ok {
		c.UpdatedAt = time.Now()
		r.conversations[msg.ConversationID] = c
	}
	return nil
}

func (r *MemoryRepository) ListConversationMessages(conversationID string) ([]model.ConversationMessage, error) {
	r.convMu.RLock()
	defer r.convMu.RUnlock()
	msgs := r.messages[conversationID]
	if msgs == nil {
		return []model.ConversationMessage{}, nil
	}
	return msgs, nil
}

// ── Agent runs ──
func (r *MemoryRepository) CreateAgentRun(run model.AgentRun) (model.AgentRun, error) {
	r.agentMu.Lock()
	defer r.agentMu.Unlock()
	if run.ID == "" {
		run.ID = fmt.Sprintf("run-%06d", r.convIDSeq.Add(1))
	}
	now := time.Now()
	run.CreatedAt, run.UpdatedAt = now, now
	if run.Status == "" {
		run.Status = model.AgentRunStatusQueued
	}
	if len(run.Transcript) == 0 {
		run.Transcript = []byte("[]")
	}
	r.agentRuns[run.ID] = run
	return run, nil
}
func (r *MemoryRepository) GetAgentRun(id string) (model.AgentRun, error) {
	r.agentMu.RLock()
	defer r.agentMu.RUnlock()
	run, ok := r.agentRuns[id]
	if !ok {
		return model.AgentRun{}, errNotFound("agent run", id)
	}
	return run, nil
}
func (r *MemoryRepository) ListAgentRunsByStatus(statuses ...model.AgentRunStatus) ([]model.AgentRun, error) {
	r.agentMu.RLock()
	defer r.agentMu.RUnlock()
	allowed := map[model.AgentRunStatus]bool{}
	for _, status := range statuses {
		allowed[status] = true
	}
	out := []model.AgentRun{}
	for _, run := range r.agentRuns {
		if allowed[run.Status] {
			out = append(out, run)
		}
	}
	return out, nil
}
func (r *MemoryRepository) UpdateAgentRun(run model.AgentRun) error {
	r.agentMu.Lock()
	defer r.agentMu.Unlock()
	if _, ok := r.agentRuns[run.ID]; !ok {
		return errNotFound("agent run", run.ID)
	}
	run.UpdatedAt = time.Now()
	r.agentRuns[run.ID] = run
	return nil
}
func (r *MemoryRepository) AddAgentEvent(event model.AgentEvent) (model.AgentEvent, error) {
	r.agentMu.Lock()
	defer r.agentMu.Unlock()
	event.ID = r.agentEventSeq.Add(1)
	event.CreatedAt = time.Now()
	r.agentEvents[event.RunID] = append(r.agentEvents[event.RunID], event)
	return event, nil
}
func (r *MemoryRepository) ListAgentEvents(runID string, afterID int64) ([]model.AgentEvent, error) {
	r.agentMu.RLock()
	defer r.agentMu.RUnlock()
	out := []model.AgentEvent{}
	for _, e := range r.agentEvents[runID] {
		if e.ID > afterID {
			out = append(out, e)
		}
	}
	return out, nil
}
func (r *MemoryRepository) CreateAgentToolCall(call model.AgentToolCall) error {
	r.agentMu.Lock()
	defer r.agentMu.Unlock()
	if _, ok := r.agentCalls[call.ID]; !ok {
		call.CreatedAt = time.Now()
		r.agentCalls[call.ID] = call
	}
	return nil
}
func (r *MemoryRepository) CompleteAgentToolCall(call model.AgentToolCall) error {
	r.agentMu.Lock()
	defer r.agentMu.Unlock()
	old, ok := r.agentCalls[call.ID]
	if !ok {
		return errNotFound("agent tool call", call.ID)
	}
	old.Result, old.Error, old.Status = call.Result, call.Error, call.Status
	now := time.Now()
	old.CompletedAt = &now
	r.agentCalls[call.ID] = old
	return nil
}

func (r *MemoryRepository) CreateAgentContextSnapshot(snapshot model.AgentContextSnapshot) (model.AgentContextSnapshot, error) {
	r.agentMu.Lock()
	defer r.agentMu.Unlock()
	if _, ok := r.agentRuns[snapshot.RunID]; !ok {
		return model.AgentContextSnapshot{}, errNotFound("agent run", snapshot.RunID)
	}
	snapshot.ID = r.agentSnapshotSeq.Add(1)
	snapshot.CreatedAt = time.Now()
	r.agentSnapshots[snapshot.RunID] = append(r.agentSnapshots[snapshot.RunID], snapshot)
	return snapshot, nil
}

func (r *MemoryRepository) ListAgentContextSnapshots(runID string) ([]model.AgentContextSnapshot, error) {
	r.agentMu.RLock()
	defer r.agentMu.RUnlock()
	items := r.agentSnapshots[runID]
	if items == nil {
		return []model.AgentContextSnapshot{}, nil
	}
	out := make([]model.AgentContextSnapshot, len(items))
	copy(out, items)
	return out, nil
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

// Ensure time is used.
var _ = time.Now
