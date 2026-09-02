package model

import "time"

type User struct {
	Username string `json:"username"`
	Role     string `json:"role"`
}

type WorkerStatus string

const (
	WorkerStatusOnline  WorkerStatus = "online"
	WorkerStatusOffline WorkerStatus = "offline"
)

type Worker struct {
	ID              string       `json:"id"`
	Name            string       `json:"name"`
	WorkerType      string       `json:"worker_type"`
	Status          WorkerStatus `json:"status"`
	HostInfo        HostInfo     `json:"host_info,omitempty"`
	LastHeartbeatAt time.Time    `json:"last_heartbeat_at"`
	CreatedAt       time.Time    `json:"created_at"`
	UpdatedAt       time.Time    `json:"updated_at"`
}

type JobStatus string

const (
	JobStatusPending         JobStatus = "pending"
	JobStatusWaitingApproval JobStatus = "waiting_approval"
	JobStatusQueued          JobStatus = "queued"
	JobStatusRunning         JobStatus = "running"
	JobStatusSuccess         JobStatus = "success"
	JobStatusFailed          JobStatus = "failed"
	JobStatusCancelled       JobStatus = "cancelled"
)

type ApprovalStatus string

const (
	ApprovalStatusNotRequired ApprovalStatus = "not_required"
	ApprovalStatusPending     ApprovalStatus = "pending"
	ApprovalStatusApproved    ApprovalStatus = "approved"
	ApprovalStatusRejected    ApprovalStatus = "rejected"
)

type Job struct {
	ID               string            `json:"id"`
	Name             string            `json:"name"`
	WorkerType       string            `json:"worker_type"`
	Command          string            `json:"command"`
	Status           JobStatus         `json:"status"`
	RiskLevel        RiskLevel         `json:"risk_level,omitempty"`
	ApprovalRequired bool              `json:"approval_required"`
	ApprovalStatus   ApprovalStatus    `json:"approval_status,omitempty"`
	ApprovalComment  string            `json:"approval_comment,omitempty"`
	ApprovedBy       string            `json:"approved_by,omitempty"`
	ApprovedAt       *time.Time        `json:"approved_at,omitempty"`
	RejectedBy       string            `json:"rejected_by,omitempty"`
	RejectedAt       *time.Time        `json:"rejected_at,omitempty"`
	TemplateCode     string            `json:"template_code,omitempty"`
	Parameters       map[string]string `json:"parameters,omitempty"`
	SourceType       string            `json:"source_type,omitempty"`
	SourceID         string            `json:"source_id,omitempty"`
	IdempotencyKey   string            `json:"-"`
	AssignedWorkerID string            `json:"assigned_worker_id,omitempty"`
	Output           string            `json:"output,omitempty"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
	StartedAt        *time.Time        `json:"started_at,omitempty"`
	FinishedAt       *time.Time        `json:"finished_at,omitempty"`
}

// AgentRunStatus is the durable lifecycle of a controlled Agent execution.
type AgentRunStatus string

const (
	AgentRunStatusQueued          AgentRunStatus = "queued"
	AgentRunStatusRunning         AgentRunStatus = "running"
	AgentRunStatusWaitingApproval AgentRunStatus = "waiting_approval"
	AgentRunStatusCompleted       AgentRunStatus = "completed"
	AgentRunStatusFailed          AgentRunStatus = "failed"
	AgentRunStatusCancelled       AgentRunStatus = "cancelled"
	AgentRunStatusTimedOut        AgentRunStatus = "timed_out"
)

// AgentRun stores the resumable transcript and immutable execution metadata.
type AgentRun struct {
	ID             string         `json:"id"`
	Input          string         `json:"input"`
	ConversationID string         `json:"conversation_id,omitempty"`
	Status         AgentRunStatus `json:"status"`
	TraceID        string         `json:"trace_id"`
	PolicyVersion  string         `json:"policy_version"`
	PromptVersion  string         `json:"prompt_version"`
	Transcript     []byte         `json:"-"`
	NextStep       int            `json:"next_step"`
	Answer         string         `json:"answer,omitempty"`
	Error          string         `json:"error,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
}

type AgentEvent struct {
	ID        int64          `json:"id"`
	RunID     string         `json:"run_id"`
	Step      int            `json:"step"`
	Type      string         `json:"type"`
	Name      string         `json:"name,omitempty"`
	Data      map[string]any `json:"data,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}

type AgentToolCall struct {
	ID          string     `json:"id"`
	RunID       string     `json:"run_id"`
	Step        int        `json:"step"`
	ToolName    string     `json:"tool_name"`
	Arguments   []byte     `json:"arguments"`
	Result      []byte     `json:"result,omitempty"`
	Error       string     `json:"error,omitempty"`
	Status      string     `json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

// AgentContextSnapshot records the sanitized prompt projection actually sent to
// the model before a run step. The canonical transcript remains the recovery
// source; snapshots make budget decisions auditable without mutating it.
type AgentContextSnapshot struct {
	ID                int64          `json:"id"`
	RunID             string         `json:"run_id"`
	Step              int            `json:"step"`
	TranscriptVersion int            `json:"transcript_version"`
	TokenEstimate     int            `json:"token_estimate"`
	BudgetTokens      int            `json:"budget_tokens"`
	Decisions         map[string]any `json:"decisions,omitempty"`
	Messages          []byte         `json:"-"`
	ContentSHA256     string         `json:"content_sha256"`
	CreatedAt         time.Time      `json:"created_at"`
}

// RiskLevel represents the risk classification of a task.
type RiskLevel string

const (
	RiskLevelLow    RiskLevel = "low"
	RiskLevelMedium RiskLevel = "medium"
	RiskLevelHigh   RiskLevel = "high"
)

// TaskIntent is the structured result of parsing a natural language task.
type TaskIntent struct {
	Intent          string            `json:"intent"`
	TargetType      string            `json:"target_type"`
	Schedule        string            `json:"schedule,omitempty"`
	RiskLevel       RiskLevel         `json:"risk_level"`
	Parameters      map[string]string `json:"parameters,omitempty"`
	MatchedTemplate string            `json:"matched_template,omitempty"`
	// ParseSource records how this result was produced so callers can tell a
	// successful LLM parse from a rule-based fallback.
	ParseSource string `json:"parse_source,omitempty"`
	// ParseWarning is non-empty only when the LLM was unavailable or returned an
	// invalid response and the rule parser recovered the request.
	ParseWarning string `json:"parse_warning,omitempty"`
}

// CommandTemplate defines a reusable, parameterized command template.
type CommandTemplate struct {
	Code        string          `json:"code"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	ToolType    string          `json:"tool_type"`
	Command     string          `json:"command"`
	Parameters  []TemplateParam `json:"parameters"`
	RiskLevel   RiskLevel       `json:"risk_level"`
}

// TemplateParam defines a single parameter within a command template.
type TemplateParam struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Required    bool   `json:"required"`
	Default     string `json:"default,omitempty"`
}

// PlanStatus tracks the lifecycle of a diagnosis plan.
type PlanStatus string

const (
	PlanStatusPending         PlanStatus = "pending"
	PlanStatusWaitingApproval PlanStatus = "waiting_approval"
	PlanStatusRunning         PlanStatus = "running"
	PlanStatusCompleted       PlanStatus = "completed"
	PlanStatusFailed          PlanStatus = "failed"
)

// DiagnosisPlan is a multi-step plan for diagnosing a fault.
type DiagnosisPlan struct {
	ID          string          `json:"id"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	TriggerType string          `json:"trigger_type"`
	Steps       []DiagnosisStep `json:"steps"`
	Status      PlanStatus      `json:"status"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

// DiagnosisStep is one step within a diagnosis plan.
type DiagnosisStep struct {
	StepNo       int               `json:"step_no"`
	Name         string            `json:"name"`
	Description  string            `json:"description"`
	TemplateCode string            `json:"template_code"`
	Parameters   map[string]string `json:"parameters"`
	TimeoutSec   int               `json:"timeout_seconds"`
	Status       JobStatus         `json:"status"`
	JobID        string            `json:"job_id,omitempty"`
	Result       *StepResult       `json:"result,omitempty"`
}

// StepResult captures the execution output of a diagnosis step.
type StepResult struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
	Success  bool   `json:"success"`
	Summary  string `json:"summary,omitempty"`
}

// AnalysisReport is the AI-generated analysis of diagnosis results.
type AnalysisReport struct {
	PlanID           string         `json:"plan_id"`
	FaultType        string         `json:"fault_type"`
	PossibleCauses   []string       `json:"possible_causes"`
	Suggestions      []string       `json:"suggestions"`
	Confidence       float64        `json:"confidence"`
	RawAnalysis      string         `json:"raw_analysis"`
	ReferenceCases   []IncidentCase `json:"reference_cases,omitempty"`
	HistoricalHints  []string       `json:"historical_hints,omitempty"`
	CreatedAt        time.Time      `json:"created_at"`
	AlertSymptoms    string         `json:"alert_symptoms,omitempty"`
	EnvironmentTags  []string       `json:"environment_tags,omitempty"`
	EvidenceSummary  string         `json:"evidence_summary,omitempty"`
	RootCause        string         `json:"root_cause,omitempty"`
	ResolutionSteps  []string       `json:"resolution_steps,omitempty"`
	ResolutionResult string         `json:"resolution_result,omitempty"`
}

type AuditLog struct {
	ID           string         `json:"id"`
	ActorType    string         `json:"actor_type"`
	ActorID      string         `json:"actor_id"`
	Action       string         `json:"action"`
	ResourceType string         `json:"resource_type"`
	ResourceID   string         `json:"resource_id"`
	Details      map[string]any `json:"details,omitempty"`
	CreatedAt    time.Time      `json:"created_at"`
}

type IncidentCase struct {
	ID             string    `json:"id"`
	Title          string    `json:"title"`
	TriggerType    string    `json:"trigger_type"`
	FaultType      string    `json:"fault_type"`
	Summary        string    `json:"summary"`
	PossibleCauses []string  `json:"possible_causes"`
	Suggestions    []string  `json:"suggestions"`
	Confidence     float64   `json:"confidence"`
	SourcePlanID   string    `json:"source_plan_id,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`

	// Structured historical knowledge. It must never be presented as a live
	// observation of the host that is currently being diagnosed.
	AlertSymptoms    string             `json:"alert_symptoms,omitempty"`
	EnvironmentTags  []string           `json:"environment_tags,omitempty"`
	EvidenceSummary  string             `json:"evidence_summary,omitempty"`
	RootCause        string             `json:"root_cause,omitempty"`
	ResolutionSteps  []string           `json:"resolution_steps,omitempty"`
	ResolutionResult string             `json:"resolution_result,omitempty"`
	Status           IncidentCaseStatus `json:"status"`
	SourceRunID      string             `json:"source_run_id,omitempty"`
	ReviewedBy       string             `json:"reviewed_by,omitempty"`
	ReviewedAt       *time.Time         `json:"reviewed_at,omitempty"`
	ReviewNote       string             `json:"review_note,omitempty"`
	// EmbeddingStatus is read-only operational metadata exposed with knowledge
	// browsing. It never contains provider errors or vector contents.
	EmbeddingStatus string `json:"embedding_status,omitempty"`
}

// IncidentCaseStatus keeps unverified model output out of the retrieval corpus.
type IncidentCaseStatus string

const (
	IncidentCaseStatusPendingReview IncidentCaseStatus = "pending_review"
	IncidentCaseStatusApproved      IncidentCaseStatus = "approved"
	IncidentCaseStatusRejected      IncidentCaseStatus = "rejected"
)

type IncidentCaseFilter struct {
	Query           string
	TriggerType     string
	FaultType       string
	EnvironmentTags []string
	Limit           int
	Status          IncidentCaseStatus
}

type MetricsSnapshot struct {
	JobsTotal                 int     `json:"jobs_total"`
	JobsSuccess               int     `json:"jobs_success"`
	JobsFailed                int     `json:"jobs_failed"`
	JobsWaitingApproval       int     `json:"jobs_waiting_approval"`
	WorkersOnline             int     `json:"workers_online"`
	IncidentCasesTotal        int     `json:"incident_cases_total"`
	JobSuccessRate            float64 `json:"job_success_rate"`
	JobFailureRate            float64 `json:"job_failure_rate"`
	AvgScheduleLatencySeconds float64 `json:"avg_schedule_latency_seconds"`
}

type RAGMetrics struct {
	Queued int `json:"queued"`
	Ready  int `json:"ready"`
	Failed int `json:"failed"`
}

// IncidentCaseEmbeddingStatus is operational metadata for an approved case's
// vector. It is deliberately kept separate from the reviewed case content.
type IncidentCaseEmbeddingStatus struct {
	CaseID        string     `json:"case_id"`
	CaseTitle     string     `json:"case_title,omitempty"`
	Status        string     `json:"status"`
	Attempts      int        `json:"attempts"`
	LastError     string     `json:"last_error,omitempty"`
	NextAttemptAt *time.Time `json:"next_attempt_at,omitempty"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// HostInfo represents host-level information collected by workers.
type HostInfo struct {
	Hostname     string  `json:"hostname"`
	IPAddress    string  `json:"ip_address"`
	CPUUsage     float64 `json:"cpu_usage"`
	StorageUsage float64 `json:"storage_usage"`
}

// JobYAML represents a stored YAML job definition.
type JobYAML struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	YAMLContent string    `json:"yaml_content"`
	Source      string    `json:"source"` // "ai" | "manual"
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// WorkerLog represents a worker execution log entry.
type WorkerLog struct {
	ID        int64     `json:"id"`
	WorkerID  string    `json:"worker_id"`
	JobID     string    `json:"job_id"`
	Command   string    `json:"command"`
	Progress  string    `json:"progress"`
	Result    string    `json:"result"`
	Success   bool      `json:"success"`
	CreatedAt time.Time `json:"created_at"`
}

// ManagedConfig stores runtime-managed YAML configuration.
type ManagedConfig struct {
	ID          string    `json:"id"`
	Kind        string    `json:"kind"`
	Name        string    `json:"name"`
	YAMLContent string    `json:"yaml_content"`
	Active      bool      `json:"active"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Conversation represents an Agent conversation session.
type Conversation struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ConversationMessage is a single message in a conversation.
type ConversationMessage struct {
	ID             int64          `json:"id"`
	ConversationID string         `json:"conversation_id"`
	Role           string         `json:"role"` // user, assistant, tool
	Content        string         `json:"content"`
	ToolName       string         `json:"tool_name,omitempty"`
	ToolData       map[string]any `json:"tool_data,omitempty"`
	Step           int            `json:"step"`
	CreatedAt      time.Time      `json:"created_at"`
}
