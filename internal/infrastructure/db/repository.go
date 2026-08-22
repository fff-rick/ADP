package db

import (
	"time"

	"adp/internal/domain/model"
)

// JobFilter defines filtering criteria for listing jobs.
type JobFilter struct {
	SourceType string
	WorkerType string
	Status     string
	Limit      int
}

// Repository defines the complete persistence interface for ADP.
// Implementations: PostgresRepository (production), MemoryRepository (testing).
type Repository interface {
	// ── Users ──

	CreateUser(username, passwordHash, role string) (model.User, error)
	GetUser(username string) (passwordHash string, user model.User, found bool, err error)
	ListUsers() ([]model.User, error)
	DeleteUser(username string) error
	UpdatePassword(username, newHash string) error

	// ── Workers ──

	RegisterWorker(name, workerType string) (model.Worker, error)
	HeartbeatWorker(id string, info *model.HostInfo) (model.Worker, error)
	GetWorker(id string) (model.Worker, error)
	ListWorkers() ([]model.Worker, error)
	UpdateWorkerStatus(id string, status model.WorkerStatus) error
	DeleteWorker(id string) error

	// ── Jobs ──

	CreateJob(job model.Job) (model.Job, error)
	GetJob(id string) (model.Job, error)
	ListJobs(filter JobFilter) ([]model.Job, error)
	AssignNextJob(workerID string) (model.Job, error)
	AssignJobToWorkers(jobID string, workerIDs []string) error
	CompleteJob(workerID, jobID, output string, success bool) (model.Job, error)
	DeleteJob(id string) error
	ListPendingApprovalJobs() ([]model.Job, error)
	ApproveJob(jobID, approvedBy, comment string) (model.Job, error)
	RejectJob(jobID, rejectedBy, reason string) (model.Job, error)

	// ── Diagnosis Plans ──

	CreatePlan(plan model.DiagnosisPlan) error
	GetPlan(id string) (model.DiagnosisPlan, error)
	UpdatePlan(id string, plan model.DiagnosisPlan) error

	// ── Audit Logs ──

	AddAuditLog(entry model.AuditLog) error
	ListAuditLogs(resourceType, resourceID string) ([]model.AuditLog, error)

	// ── Incident Cases ──

	UpsertIncidentCase(planID string, c model.IncidentCase) (model.IncidentCase, error)
	ListIncidentCases(filter model.IncidentCaseFilter) ([]model.IncidentCase, error)
	FindSimilarIncidentCases(description, triggerType, faultType string, limit int) ([]model.IncidentCase, error)

	// ── Job YAMLs ──

	SaveJobYAML(yaml model.JobYAML) (model.JobYAML, error)
	ListJobYAMLs() ([]model.JobYAML, error)
	GetJobYAML(id string) (model.JobYAML, error)
	DeleteJobYAML(id string) error

	// ── Worker Logs ──

	AddWorkerLog(entry model.WorkerLog) error
	ListWorkerLogs(workerID, jobID string, limit int) ([]model.WorkerLog, error)

	// ── Managed Runtime Configs ──

	SaveManagedConfig(config model.ManagedConfig) (model.ManagedConfig, error)
	ListManagedConfigs(kind string) ([]model.ManagedConfig, error)
	GetManagedConfig(kind, id string) (model.ManagedConfig, error)
	DeleteManagedConfig(kind, id string) error

	// ── Conversations ──

	CreateConversation(title string) (model.Conversation, error)
	GetConversation(id string) (model.Conversation, error)
	ListConversations() ([]model.Conversation, error)
	DeleteConversation(id string) error
	AddConversationMessage(msg model.ConversationMessage) error
	ListConversationMessages(conversationID string) ([]model.ConversationMessage, error)

	// ── Agent runs ──
	CreateAgentRun(run model.AgentRun) (model.AgentRun, error)
	GetAgentRun(id string) (model.AgentRun, error)
	ListAgentRunsByStatus(statuses ...model.AgentRunStatus) ([]model.AgentRun, error)
	UpdateAgentRun(run model.AgentRun) error
	AddAgentEvent(event model.AgentEvent) (model.AgentEvent, error)
	ListAgentEvents(runID string, afterID int64) ([]model.AgentEvent, error)
	CreateAgentToolCall(call model.AgentToolCall) error
	CompleteAgentToolCall(call model.AgentToolCall) error

	// ── Metrics ──

	MetricsSnapshot() (model.MetricsSnapshot, error)

	// ── Lifecycle ──

	Ping() error
	Migrate() error
}

// WorkerOnlineThreshold is the max age of a heartbeat before marking offline.
const WorkerOnlineThreshold = 30 * time.Second
