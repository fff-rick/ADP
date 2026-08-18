package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync/atomic"
	"time"

	"github.com/lib/pq"

	"adp/internal/domain/model"
)

// PostgresRepository implements Repository using PostgreSQL.
type PostgresRepository struct {
	db     *sql.DB
	nextID atomic.Uint64
}

// NewPostgresRepository creates a new PostgresRepository and runs migrations.
func NewPostgresRepository(dsn string) (*PostgresRepository, error) {
	database, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	database.SetMaxOpenConns(25)
	database.SetMaxIdleConns(5)
	database.SetConnMaxLifetime(5 * time.Minute)

	if err := database.Ping(); err != nil {
		return nil, fmt.Errorf("ping database: %w", err)
	}

	repo := &PostgresRepository{db: database}

	if err := repo.Migrate(); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}

	// Seed the ID counter from existing database records to avoid collisions after restart.
	repo.seedCounter()

	return repo, nil
}

// Close closes the database connection.
func (r *PostgresRepository) Close() error {
	return r.db.Close()
}

// Ping checks database connectivity.
func (r *PostgresRepository) Ping() error {
	return r.db.Ping()
}

// Migrate runs schema creation.
func (r *PostgresRepository) Migrate() error {
	_, err := r.db.Exec(SchemaSQL)
	return err
}

// ── ID generation ──

// seedCounter reads the highest existing ID suffix from the database and sets the counter above it.
// Called once on startup to avoid collisions after server restart.
func (r *PostgresRepository) seedCounter() {
	tables := []string{"workers", "jobs", "audit_logs", "incident_cases", "diagnosis_plans", "job_yamls", "managed_configs", "conversations", "agent_runs"}
	var maxVal uint64
	for _, table := range tables {
		var suffix uint64
		// Extract numeric suffix from IDs like "worker-000123".
		err := r.db.QueryRow(
			fmt.Sprintf(`SELECT COALESCE(MAX(CAST(SUBSTRING(id FROM '[0-9]+$') AS BIGINT)), 0) FROM %s`, table),
		).Scan(&suffix)
		if err == nil && suffix > maxVal {
			maxVal = suffix
		}
	}
	if maxVal > 0 {
		r.nextID.Store(maxVal)
	}
	log.Printf("db: id counter seeded, nextID = %d", r.nextID.Load())
}

func (r *PostgresRepository) genID(prefix string) string {
	value := r.nextID.Add(1)
	return fmt.Sprintf("%s-%06d", prefix, value)
}

// ── Users ──

func (r *PostgresRepository) CreateUser(username, passwordHash, role string) (model.User, error) {
	now := time.Now()
	_, err := r.db.Exec(
		`INSERT INTO users (username, password, role, created_at, updated_at) VALUES ($1, $2, $3, $4, $5)`,
		username, passwordHash, role, now, now,
	)
	if err != nil {
		return model.User{}, fmt.Errorf("create user: %w", err)
	}
	return model.User{Username: username, Role: role}, nil
}

func (r *PostgresRepository) GetUser(username string) (string, model.User, bool, error) {
	var u model.User
	var passwordHash string
	err := r.db.QueryRow(
		`SELECT username, password, role FROM users WHERE username = $1`, username,
	).Scan(&u.Username, &passwordHash, &u.Role)
	if err == sql.ErrNoRows {
		return "", model.User{}, false, nil
	}
	if err != nil {
		return "", model.User{}, false, fmt.Errorf("get user: %w", err)
	}
	return passwordHash, u, true, nil
}

func (r *PostgresRepository) ListUsers() ([]model.User, error) {
	rows, err := r.db.Query(`SELECT username, role FROM users ORDER BY username`)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var users []model.User
	for rows.Next() {
		var u model.User
		if err := rows.Scan(&u.Username, &u.Role); err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func (r *PostgresRepository) DeleteUser(username string) error {
	result, err := r.db.Exec(`DELETE FROM users WHERE username = $1`, username)
	if err != nil {
		return fmt.Errorf("delete user: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("user not found: %s", username)
	}
	return nil
}

func (r *PostgresRepository) UpdatePassword(username, newHash string) error {
	result, err := r.db.Exec(
		`UPDATE users SET password = $1, updated_at = $2 WHERE username = $3`,
		newHash, time.Now(), username,
	)
	if err != nil {
		return fmt.Errorf("update password: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("user not found: %s", username)
	}
	return nil
}

// ── Workers ──

func (r *PostgresRepository) RegisterWorker(name, workerType string) (model.Worker, error) {
	now := time.Now()

	// Idempotent: check if a worker with same name+type already exists.
	var existing model.Worker
	err := r.db.QueryRow(
		`SELECT id, name, worker_type, status, hostname, ip_address, cpu_usage, storage_usage,
		        last_heartbeat_at, created_at, updated_at
		 FROM workers WHERE name = $1 AND worker_type = $2`, name, workerType,
	).Scan(&existing.ID, &existing.Name, &existing.WorkerType, &existing.Status,
		&existing.HostInfo.Hostname, &existing.HostInfo.IPAddress, &existing.HostInfo.CPUUsage, &existing.HostInfo.StorageUsage,
		&existing.LastHeartbeatAt, &existing.CreatedAt, &existing.UpdatedAt)
	if err == nil {
		// Reuse existing worker: update heartbeat and return.
		_, _ = r.db.Exec(
			`UPDATE workers SET status = $1, last_heartbeat_at = $2, updated_at = $3 WHERE id = $4`,
			model.WorkerStatusOnline, now, now, existing.ID,
		)
		existing.Status = model.WorkerStatusOnline
		existing.LastHeartbeatAt = now
		existing.UpdatedAt = now
		return existing, nil
	}

	// Not found — create new.
	worker := model.Worker{
		ID:              r.genID("worker"),
		Name:            name,
		WorkerType:      workerType,
		Status:          model.WorkerStatusOnline,
		LastHeartbeatAt: now,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	_, err = r.db.Exec(
		`INSERT INTO workers (id, name, worker_type, status, last_heartbeat_at, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		worker.ID, worker.Name, worker.WorkerType, worker.Status,
		worker.LastHeartbeatAt, worker.CreatedAt, worker.UpdatedAt,
	)
	if err != nil {
		return model.Worker{}, fmt.Errorf("register worker: %w", err)
	}
	return worker, nil
}

func (r *PostgresRepository) HeartbeatWorker(id string, info *model.HostInfo) (model.Worker, error) {
	now := time.Now()

	query := `UPDATE workers SET last_heartbeat_at = $1, status = $2, updated_at = $3`
	args := []any{now, model.WorkerStatusOnline, now}

	if info != nil {
		query += `, hostname = $4, ip_address = $5, cpu_usage = $6, storage_usage = $7`
		args = append(args, info.Hostname, info.IPAddress, info.CPUUsage, info.StorageUsage)
		query += ` WHERE id = $8`
		args = append(args, id)
	} else {
		query += ` WHERE id = $4`
		args = append(args, id)
	}

	result, err := r.db.Exec(query, args...)
	if err != nil {
		return model.Worker{}, fmt.Errorf("heartbeat worker: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return model.Worker{}, fmt.Errorf("worker not found: %s", id)
	}

	return r.GetWorker(id)
}

func (r *PostgresRepository) GetWorker(id string) (model.Worker, error) {
	var w model.Worker
	err := r.db.QueryRow(
		`SELECT id, name, worker_type, status, hostname, ip_address, cpu_usage, storage_usage,
		        last_heartbeat_at, created_at, updated_at
		 FROM workers WHERE id = $1`, id,
	).Scan(&w.ID, &w.Name, &w.WorkerType, &w.Status,
		&w.HostInfo.Hostname, &w.HostInfo.IPAddress, &w.HostInfo.CPUUsage, &w.HostInfo.StorageUsage,
		&w.LastHeartbeatAt, &w.CreatedAt, &w.UpdatedAt)
	if err == sql.ErrNoRows {
		return model.Worker{}, fmt.Errorf("worker not found: %s", id)
	}
	if err != nil {
		return model.Worker{}, fmt.Errorf("get worker: %w", err)
	}

	// Determine online status from heartbeat recency.
	if time.Since(w.LastHeartbeatAt) > WorkerOnlineThreshold {
		w.Status = model.WorkerStatusOffline
	} else {
		w.Status = model.WorkerStatusOnline
	}

	return w, nil
}

func (r *PostgresRepository) ListWorkers() ([]model.Worker, error) {
	rows, err := r.db.Query(
		`SELECT id, name, worker_type, status, hostname, ip_address, cpu_usage, storage_usage,
		        last_heartbeat_at, created_at, updated_at
		 FROM workers ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("list workers: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var workers []model.Worker
	now := time.Now()
	for rows.Next() {
		var w model.Worker
		if err := rows.Scan(&w.ID, &w.Name, &w.WorkerType, &w.Status,
			&w.HostInfo.Hostname, &w.HostInfo.IPAddress, &w.HostInfo.CPUUsage, &w.HostInfo.StorageUsage,
			&w.LastHeartbeatAt, &w.CreatedAt, &w.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan worker: %w", err)
		}
		// Recompute online status.
		if w.Status == model.WorkerStatusOnline && now.Sub(w.LastHeartbeatAt) > WorkerOnlineThreshold {
			w.Status = model.WorkerStatusOffline
		}
		workers = append(workers, w)
	}
	return workers, rows.Err()
}

func (r *PostgresRepository) UpdateWorkerStatus(id string, status model.WorkerStatus) error {
	_, err := r.db.Exec(
		`UPDATE workers SET status = $1, updated_at = $2 WHERE id = $3`,
		status, time.Now(), id,
	)
	return err
}

func (r *PostgresRepository) DeleteWorker(id string) error {
	_, err := r.db.Exec(`DELETE FROM workers WHERE id = $1`, id)
	return err
}

// ── Jobs ──

func (r *PostgresRepository) CreateJob(job model.Job) (model.Job, error) {
	if job.IdempotencyKey != "" {
		var existingID string
		err := r.db.QueryRow(`SELECT id FROM jobs WHERE idempotency_key = $1`, job.IdempotencyKey).Scan(&existingID)
		if err == nil {
			return r.GetJob(existingID)
		}
		if err != sql.ErrNoRows {
			return model.Job{}, fmt.Errorf("find idempotent job: %w", err)
		}
	}
	now := time.Now()
	job.ID = r.genID("job")
	job.CreatedAt = now
	job.UpdatedAt = now
	if job.Status == "" {
		job.Status = model.JobStatusPending
	}
	if job.ApprovalStatus == "" {
		if job.ApprovalRequired {
			job.ApprovalStatus = model.ApprovalStatusPending
		} else {
			job.ApprovalStatus = model.ApprovalStatusNotRequired
		}
	}
	parametersJSON, err := marshalStringMap(job.Parameters)
	if err != nil {
		return model.Job{}, fmt.Errorf("marshal job parameters: %w", err)
	}

	_, err = r.db.Exec(
		`INSERT INTO jobs (id, name, worker_type, command, status, risk_level,
		 approval_required, approval_status, approval_comment,
		 approved_by, approved_at, rejected_by, rejected_at,
		 template_code, parameters, source_type, source_id, idempotency_key, assigned_worker_id, output,
		 created_at, updated_at, started_at, finished_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24)`,
		job.ID, job.Name, job.WorkerType, job.Command, job.Status, job.RiskLevel,
		job.ApprovalRequired, job.ApprovalStatus, job.ApprovalComment,
		job.ApprovedBy, job.ApprovedAt, job.RejectedBy, job.RejectedAt,
		job.TemplateCode, parametersJSON, job.SourceType, job.SourceID, job.IdempotencyKey, job.AssignedWorkerID, job.Output,
		job.CreatedAt, job.UpdatedAt, job.StartedAt, job.FinishedAt,
	)
	if err != nil {
		if job.IdempotencyKey != "" {
			var existingID string
			if lookupErr := r.db.QueryRow(`SELECT id FROM jobs WHERE idempotency_key = $1`, job.IdempotencyKey).Scan(&existingID); lookupErr == nil {
				return r.GetJob(existingID)
			}
		}
		return model.Job{}, fmt.Errorf("create job: %w", err)
	}
	return job, nil
}

func marshalStringMap(values map[string]string) ([]byte, error) {
	if values == nil {
		values = map[string]string{}
	}
	return json.Marshal(values)
}

func (r *PostgresRepository) GetJob(id string) (model.Job, error) {
	var j model.Job
	var startedAt, finishedAt, approvedAt, rejectedAt sql.NullTime
	var parametersJSON []byte
	err := r.db.QueryRow(
		`SELECT id, name, worker_type, command, status, risk_level,
		 approval_required, approval_status, approval_comment,
		 approved_by, approved_at, rejected_by, rejected_at,
		 template_code, parameters, source_type, source_id, assigned_worker_id, output,
		 created_at, updated_at, started_at, finished_at
		 FROM jobs WHERE id = $1`, id,
	).Scan(&j.ID, &j.Name, &j.WorkerType, &j.Command, &j.Status, &j.RiskLevel,
		&j.ApprovalRequired, &j.ApprovalStatus, &j.ApprovalComment,
		&j.ApprovedBy, &approvedAt, &j.RejectedBy, &rejectedAt,
		&j.TemplateCode, &parametersJSON, &j.SourceType, &j.SourceID, &j.AssignedWorkerID, &j.Output,
		&j.CreatedAt, &j.UpdatedAt, &startedAt, &finishedAt)
	if err == sql.ErrNoRows {
		return model.Job{}, fmt.Errorf("job not found: %s", id)
	}
	if err != nil {
		return model.Job{}, fmt.Errorf("get job: %w", err)
	}
	if startedAt.Valid {
		j.StartedAt = &startedAt.Time
	}
	if finishedAt.Valid {
		j.FinishedAt = &finishedAt.Time
	}
	if approvedAt.Valid {
		j.ApprovedAt = &approvedAt.Time
	}
	if rejectedAt.Valid {
		j.RejectedAt = &rejectedAt.Time
	}
	if len(parametersJSON) > 0 {
		if err := json.Unmarshal(parametersJSON, &j.Parameters); err != nil {
			return model.Job{}, fmt.Errorf("unmarshal job parameters: %w", err)
		}
	}
	return j, nil
}

func (r *PostgresRepository) ListJobs(filter JobFilter) ([]model.Job, error) {
	query := `SELECT id, name, worker_type, command, status, risk_level,
		 approval_required, approval_status, approval_comment,
		 approved_by, approved_at, rejected_by, rejected_at,
		 template_code, parameters, source_type, source_id, assigned_worker_id, output,
		 created_at, updated_at, started_at, finished_at
		 FROM jobs WHERE 1=1`
	var args []any
	argN := 1

	if filter.SourceType != "" {
		query += fmt.Sprintf(" AND source_type = $%d", argN)
		args = append(args, filter.SourceType)
		argN++
	}
	if filter.WorkerType != "" {
		query += fmt.Sprintf(" AND worker_type = $%d", argN)
		args = append(args, filter.WorkerType)
		argN++
	}
	if filter.Status != "" {
		query += fmt.Sprintf(" AND status = $%d", argN)
		args = append(args, filter.Status)
		argN++
	}

	query += " ORDER BY created_at DESC"

	if filter.Limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", argN)
		args = append(args, filter.Limit)
	}

	rows, err := r.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list jobs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var jobs []model.Job
	for rows.Next() {
		var j model.Job
		var startedAt, finishedAt, approvedAt, rejectedAt sql.NullTime
		var parametersJSON []byte
		if err := rows.Scan(&j.ID, &j.Name, &j.WorkerType, &j.Command, &j.Status, &j.RiskLevel,
			&j.ApprovalRequired, &j.ApprovalStatus, &j.ApprovalComment,
			&j.ApprovedBy, &approvedAt, &j.RejectedBy, &rejectedAt,
			&j.TemplateCode, &parametersJSON, &j.SourceType, &j.SourceID, &j.AssignedWorkerID, &j.Output,
			&j.CreatedAt, &j.UpdatedAt, &startedAt, &finishedAt); err != nil {
			return nil, fmt.Errorf("scan job: %w", err)
		}
		if startedAt.Valid {
			j.StartedAt = &startedAt.Time
		}
		if finishedAt.Valid {
			j.FinishedAt = &finishedAt.Time
		}
		if approvedAt.Valid {
			j.ApprovedAt = &approvedAt.Time
		}
		if rejectedAt.Valid {
			j.RejectedAt = &rejectedAt.Time
		}
		if len(parametersJSON) > 0 {
			if err := json.Unmarshal(parametersJSON, &j.Parameters); err != nil {
				return nil, fmt.Errorf("unmarshal job parameters: %w", err)
			}
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

func (r *PostgresRepository) AssignNextJob(workerID string) (model.Job, error) {
	// Get worker type first.
	worker, err := r.GetWorker(workerID)
	if err != nil {
		return model.Job{}, err
	}

	// Find the earliest queued job compatible with the worker type. Shell is
	// intentionally the only general-purpose worker type.
	var jobID string
	query := `SELECT id FROM jobs WHERE status IN ($1, $2)`
	args := []any{model.JobStatusQueued, model.JobStatusPending}
	if model.NormalizeWorkerType(worker.WorkerType) != "shell" {
		query += ` AND worker_type = $3`
		args = append(args, worker.WorkerType)
	}
	query += ` ORDER BY created_at ASC, id ASC LIMIT 1`
	err = r.db.QueryRow(query, args...).Scan(&jobID)
	if err == sql.ErrNoRows {
		return model.Job{}, fmt.Errorf("no queued jobs for worker type %s", worker.WorkerType)
	}
	if err != nil {
		return model.Job{}, fmt.Errorf("assign next job: %w", err)
	}

	now := time.Now()
	_, err = r.db.Exec(
		`UPDATE jobs SET status = $1, assigned_worker_id = $2, started_at = $3, updated_at = $4
		 WHERE id = $5`,
		model.JobStatusRunning, workerID, now, now, jobID,
	)
	if err != nil {
		return model.Job{}, fmt.Errorf("assign next job update: %w", err)
	}

	return r.GetJob(jobID)
}

func (r *PostgresRepository) AssignJobToWorkers(jobID string, workerIDs []string) error {
	job, err := r.GetJob(jobID)
	if err != nil {
		return err
	}
	now := time.Now()
	for _, wid := range workerIDs {
		worker, err := r.GetWorker(wid)
		if err != nil {
			return err
		}
		if !model.WorkerCanRunType(worker.WorkerType, job.WorkerType) {
			return fmt.Errorf("worker %s type %s cannot run job type %s", wid, worker.WorkerType, job.WorkerType)
		}
		_, err = r.db.Exec(
			`UPDATE jobs SET status = $1, assigned_worker_id = $2, started_at = $3, updated_at = $4
			 WHERE id = $5`,
			model.JobStatusRunning, wid, now, now, jobID,
		)
		if err != nil {
			return fmt.Errorf("assign job %s to worker %s: %w", jobID, wid, err)
		}
	}
	return nil
}

func (r *PostgresRepository) CompleteJob(workerID, jobID, output string, success bool) (model.Job, error) {
	now := time.Now()
	status := model.JobStatusSuccess
	if !success {
		status = model.JobStatusFailed
	}

	result, err := r.db.Exec(
		`UPDATE jobs SET status = $1, output = $2, finished_at = $3, updated_at = $4
		 WHERE id = $5 AND assigned_worker_id = $6 AND status = $7`,
		status, output, now, now, jobID, workerID, model.JobStatusRunning,
	)
	if err != nil {
		return model.Job{}, fmt.Errorf("complete job: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return model.Job{}, fmt.Errorf("job not found or not assigned to worker")
	}

	return r.GetJob(jobID)
}

func (r *PostgresRepository) DeleteJob(id string) error {
	job, err := r.GetJob(id)
	if err != nil {
		return err
	}

	// Only allow deleting jobs that haven't been dispatched.
	switch job.Status {
	case model.JobStatusPending, model.JobStatusQueued, model.JobStatusWaitingApproval:
		// allowed
	default:
		return fmt.Errorf("cannot delete job with status %s", job.Status)
	}

	_, err = r.db.Exec(`DELETE FROM jobs WHERE id = $1`, id)
	return err
}

func (r *PostgresRepository) ListPendingApprovalJobs() ([]model.Job, error) {
	return r.ListJobs(JobFilter{Status: string(model.JobStatusWaitingApproval)})
}

func (r *PostgresRepository) ApproveJob(jobID, approvedBy, comment string) (model.Job, error) {
	now := time.Now()
	result, err := r.db.Exec(
		`UPDATE jobs SET status = $1, approval_status = $2, approved_by = $3, approved_at = $4,
		 approval_comment = $5, updated_at = $6
		 WHERE id = $7 AND status = $8`,
		model.JobStatusPending, model.ApprovalStatusApproved, approvedBy, now,
		comment, now, jobID, model.JobStatusWaitingApproval,
	)
	if err != nil {
		return model.Job{}, fmt.Errorf("approve job: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return model.Job{}, fmt.Errorf("job not found or not waiting for approval")
	}
	return r.GetJob(jobID)
}

func (r *PostgresRepository) RejectJob(jobID, rejectedBy, reason string) (model.Job, error) {
	now := time.Now()
	result, err := r.db.Exec(
		`UPDATE jobs SET status = $1, approval_status = $2, rejected_by = $3, rejected_at = $4,
		 approval_comment = $5, updated_at = $6
		 WHERE id = $7 AND status = $8`,
		model.JobStatusCancelled, model.ApprovalStatusRejected, rejectedBy, now,
		reason, now, jobID, model.JobStatusWaitingApproval,
	)
	if err != nil {
		return model.Job{}, fmt.Errorf("reject job: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return model.Job{}, fmt.Errorf("job not found or not waiting for approval")
	}
	return r.GetJob(jobID)
}

// ── Diagnosis Plans ──

func (r *PostgresRepository) CreatePlan(plan model.DiagnosisPlan) error {
	stepsJSON, err := json.Marshal(plan.Steps)
	if err != nil {
		return fmt.Errorf("marshal steps: %w", err)
	}

	now := time.Now()
	_, err = r.db.Exec(
		`INSERT INTO diagnosis_plans (id, title, description, trigger_type, steps, status, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		plan.ID, plan.Title, plan.Description, plan.TriggerType, stepsJSON, plan.Status, now, now,
	)
	return err
}

func (r *PostgresRepository) GetPlan(id string) (model.DiagnosisPlan, error) {
	var plan model.DiagnosisPlan
	var stepsJSON []byte
	err := r.db.QueryRow(
		`SELECT id, title, description, trigger_type, steps, status, created_at, updated_at
		 FROM diagnosis_plans WHERE id = $1`, id,
	).Scan(&plan.ID, &plan.Title, &plan.Description, &plan.TriggerType,
		&stepsJSON, &plan.Status, &plan.CreatedAt, &plan.UpdatedAt)
	if err == sql.ErrNoRows {
		return model.DiagnosisPlan{}, fmt.Errorf("plan not found: %s", id)
	}
	if err != nil {
		return model.DiagnosisPlan{}, fmt.Errorf("get plan: %w", err)
	}
	if err := json.Unmarshal(stepsJSON, &plan.Steps); err != nil {
		return model.DiagnosisPlan{}, fmt.Errorf("unmarshal steps: %w", err)
	}
	return plan, nil
}

func (r *PostgresRepository) UpdatePlan(id string, plan model.DiagnosisPlan) error {
	stepsJSON, err := json.Marshal(plan.Steps)
	if err != nil {
		return fmt.Errorf("marshal steps: %w", err)
	}
	_, err = r.db.Exec(
		`UPDATE diagnosis_plans SET title = $1, description = $2, trigger_type = $3,
		 steps = $4, status = $5, updated_at = $6
		 WHERE id = $7`,
		plan.Title, plan.Description, plan.TriggerType, stepsJSON, plan.Status, time.Now(), id,
	)
	return err
}

// ── Audit Logs ──

func (r *PostgresRepository) AddAuditLog(entry model.AuditLog) error {
	detailsJSON, _ := json.Marshal(entry.Details)
	entry.ID = r.genID("audit")
	_, err := r.db.Exec(
		`INSERT INTO audit_logs (id, actor_type, actor_id, action, resource_type, resource_id, details, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		entry.ID, entry.ActorType, entry.ActorID, entry.Action,
		entry.ResourceType, entry.ResourceID, detailsJSON, time.Now(),
	)
	return err
}

func (r *PostgresRepository) ListAuditLogs(resourceType, resourceID string) ([]model.AuditLog, error) {
	query := `SELECT id, actor_type, actor_id, action, resource_type, resource_id, details, created_at
		 FROM audit_logs WHERE 1=1`
	var args []any
	argN := 1

	if resourceType != "" {
		query += fmt.Sprintf(" AND resource_type = $%d", argN)
		args = append(args, resourceType)
		argN++
	}
	if resourceID != "" {
		query += fmt.Sprintf(" AND resource_id = $%d", argN)
		args = append(args, resourceID)
	}

	query += " ORDER BY created_at DESC"

	rows, err := r.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list audit logs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var logs []model.AuditLog
	for rows.Next() {
		var l model.AuditLog
		var detailsJSON []byte
		if err := rows.Scan(&l.ID, &l.ActorType, &l.ActorID, &l.Action,
			&l.ResourceType, &l.ResourceID, &detailsJSON, &l.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan audit log: %w", err)
		}
		if len(detailsJSON) > 0 {
			_ = json.Unmarshal(detailsJSON, &l.Details)
		}
		logs = append(logs, l)
	}
	return logs, rows.Err()
}

// ── Incident Cases ──

func (r *PostgresRepository) UpsertIncidentCase(planID string, c model.IncidentCase) (model.IncidentCase, error) {
	now := time.Now()

	// Check if a case already exists for this plan.
	var existingID string
	err := r.db.QueryRow(
		`SELECT id FROM incident_cases WHERE source_plan_id = $1`, planID,
	).Scan(&existingID)

	if err == nil {
		// Update existing.
		c.ID = existingID
		c.UpdatedAt = now
		_, err = r.db.Exec(
			`UPDATE incident_cases SET title = $1, trigger_type = $2, fault_type = $3,
			 summary = $4, possible_causes = $5, suggestions = $6, confidence = $7,
			 alert_symptoms = $8, environment_tags = $9, evidence_summary = $10,
			 root_cause = $11, resolution_steps = $12, resolution_result = $13,
			 updated_at = $14 WHERE id = $15`,
			c.Title, c.TriggerType, c.FaultType, c.Summary,
			pq.Array(c.PossibleCauses), pq.Array(c.Suggestions), c.Confidence,
			c.AlertSymptoms, pq.Array(c.EnvironmentTags), c.EvidenceSummary,
			c.RootCause, pq.Array(c.ResolutionSteps), c.ResolutionResult, now, c.ID,
		)
		if err != nil {
			return model.IncidentCase{}, fmt.Errorf("update incident case: %w", err)
		}
		return c, nil
	}

	// Insert new.
	c.ID = r.genID("case")
	c.CreatedAt = now
	c.UpdatedAt = now
	c.SourcePlanID = planID
	if c.PossibleCauses == nil {
		c.PossibleCauses = []string{}
	}
	if c.Suggestions == nil {
		c.Suggestions = []string{}
	}
	if c.EnvironmentTags == nil {
		c.EnvironmentTags = []string{}
	}
	if c.ResolutionSteps == nil {
		c.ResolutionSteps = []string{}
	}

	_, err = r.db.Exec(
		`INSERT INTO incident_cases (id, title, trigger_type, fault_type, summary,
		 possible_causes, suggestions, confidence, source_plan_id, alert_symptoms,
		 environment_tags, evidence_summary, root_cause, resolution_steps,
		 resolution_result, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)`,
		c.ID, c.Title, c.TriggerType, c.FaultType, c.Summary,
		pq.Array(c.PossibleCauses), pq.Array(c.Suggestions), c.Confidence,
		planID, c.AlertSymptoms, pq.Array(c.EnvironmentTags), c.EvidenceSummary,
		c.RootCause, pq.Array(c.ResolutionSteps), c.ResolutionResult, c.CreatedAt, c.UpdatedAt,
	)
	if err != nil {
		return model.IncidentCase{}, fmt.Errorf("insert incident case: %w", err)
	}
	return c, nil
}

func (r *PostgresRepository) ListIncidentCases(filter model.IncidentCaseFilter) ([]model.IncidentCase, error) {
	query := `SELECT id, title, trigger_type, fault_type, summary,
		 possible_causes, suggestions, confidence, source_plan_id, alert_symptoms,
		 environment_tags, evidence_summary, root_cause, resolution_steps,
		 resolution_result, created_at, updated_at
		 FROM incident_cases WHERE 1=1`
	var args []any
	argN := 1

	if filter.TriggerType != "" {
		query += fmt.Sprintf(" AND trigger_type = $%d", argN)
		args = append(args, filter.TriggerType)
		argN++
	}
	if filter.FaultType != "" {
		query += fmt.Sprintf(" AND fault_type = $%d", argN)
		args = append(args, filter.FaultType)
		argN++
	}
	if len(filter.EnvironmentTags) > 0 {
		query += fmt.Sprintf(" AND environment_tags @> $%d", argN)
		args = append(args, pq.Array(filter.EnvironmentTags))
		argN++
	}
	if filter.Query != "" {
		query += fmt.Sprintf(` AND concat_ws(' ', title, trigger_type, fault_type, summary,
			alert_symptoms, evidence_summary, root_cause, resolution_result,
			array_to_string(environment_tags, ' '), array_to_string(resolution_steps, ' '),
			array_to_string(possible_causes, ' '), array_to_string(suggestions, ' ')) ILIKE $%d`, argN)
		pattern := "%" + filter.Query + "%"
		args = append(args, pattern)
		argN++
	}

	query += " ORDER BY updated_at DESC"

	if filter.Limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", argN)
		args = append(args, filter.Limit)
	}

	rows, err := r.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list incident cases: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var cases []model.IncidentCase
	for rows.Next() {
		var c model.IncidentCase
		if err := rows.Scan(&c.ID, &c.Title, &c.TriggerType, &c.FaultType, &c.Summary,
			pq.Array(&c.PossibleCauses), pq.Array(&c.Suggestions), &c.Confidence,
			&c.SourcePlanID, &c.AlertSymptoms, pq.Array(&c.EnvironmentTags),
			&c.EvidenceSummary, &c.RootCause, pq.Array(&c.ResolutionSteps),
			&c.ResolutionResult, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan incident case: %w", err)
		}
		cases = append(cases, c)
	}
	return cases, rows.Err()
}

func (r *PostgresRepository) FindSimilarIncidentCases(description, triggerType, faultType string, limit int) ([]model.IncidentCase, error) {
	// P1 retrieval is deliberately deterministic keyword/structured matching.
	// It avoids the previous full-table fetch and keeps the eventual pgvector
	// addition an in-place enhancement rather than a second data store.
	return r.ListIncidentCases(model.IncidentCaseFilter{Query: description, TriggerType: triggerType, FaultType: faultType, Limit: limit})
}

// ── Job YAMLs ──

func (r *PostgresRepository) SaveJobYAML(jy model.JobYAML) (model.JobYAML, error) {
	now := time.Now()
	if jy.ID == "" {
		jy.ID = r.genID("yaml")
	}
	jy.CreatedAt = now
	jy.UpdatedAt = now

	_, err := r.db.Exec(
		`INSERT INTO job_yamls (id, name, description, yaml_content, source, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 ON CONFLICT (id) DO UPDATE SET name=$2, description=$3, yaml_content=$4, source=$5, updated_at=$7`,
		jy.ID, jy.Name, jy.Description, jy.YAMLContent, jy.Source, jy.CreatedAt, jy.UpdatedAt,
	)
	if err != nil {
		return model.JobYAML{}, fmt.Errorf("save job yaml: %w", err)
	}
	return jy, nil
}

func (r *PostgresRepository) ListJobYAMLs() ([]model.JobYAML, error) {
	rows, err := r.db.Query(
		`SELECT id, name, description, yaml_content, source, created_at, updated_at
		 FROM job_yamls ORDER BY updated_at DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var result []model.JobYAML
	for rows.Next() {
		var jy model.JobYAML
		if err := rows.Scan(&jy.ID, &jy.Name, &jy.Description, &jy.YAMLContent, &jy.Source, &jy.CreatedAt, &jy.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, jy)
	}
	return result, nil
}

func (r *PostgresRepository) GetJobYAML(id string) (model.JobYAML, error) {
	var jy model.JobYAML
	err := r.db.QueryRow(
		`SELECT id, name, description, yaml_content, source, created_at, updated_at FROM job_yamls WHERE id=$1`, id,
	).Scan(&jy.ID, &jy.Name, &jy.Description, &jy.YAMLContent, &jy.Source, &jy.CreatedAt, &jy.UpdatedAt)
	if err != nil {
		return model.JobYAML{}, err
	}
	return jy, nil
}

func (r *PostgresRepository) DeleteJobYAML(id string) error {
	_, err := r.db.Exec(`DELETE FROM job_yamls WHERE id=$1`, id)
	return err
}

// ── Worker Logs ──

func (r *PostgresRepository) AddWorkerLog(entry model.WorkerLog) error {
	_, err := r.db.Exec(
		`INSERT INTO worker_logs (worker_id, job_id, command, progress, result, success, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		entry.WorkerID, entry.JobID, entry.Command, entry.Progress, entry.Result,
		entry.Success, time.Now(),
	)
	return err
}

func (r *PostgresRepository) ListWorkerLogs(workerID, jobID string, limit int) ([]model.WorkerLog, error) {
	query := `SELECT id, worker_id, job_id, command, progress, result, success, created_at
		 FROM worker_logs WHERE 1=1`
	var args []any
	argN := 1

	if workerID != "" {
		query += fmt.Sprintf(" AND worker_id = $%d", argN)
		args = append(args, workerID)
		argN++
	}
	if jobID != "" {
		query += fmt.Sprintf(" AND job_id = $%d", argN)
		args = append(args, jobID)
		argN++
	}

	query += " ORDER BY created_at DESC"

	if limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", argN)
		args = append(args, limit)
	}

	rows, err := r.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list worker logs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var logs []model.WorkerLog
	for rows.Next() {
		var l model.WorkerLog
		if err := rows.Scan(&l.ID, &l.WorkerID, &l.JobID, &l.Command, &l.Progress,
			&l.Result, &l.Success, &l.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan worker log: %w", err)
		}
		logs = append(logs, l)
	}
	return logs, rows.Err()
}

// ── Managed Runtime Configs ──

func (r *PostgresRepository) SaveManagedConfig(config model.ManagedConfig) (model.ManagedConfig, error) {
	now := time.Now()
	if config.ID == "" {
		config.ID = r.genID("cfg")
	}
	if config.CreatedAt.IsZero() {
		config.CreatedAt = now
	}
	config.UpdatedAt = now

	_, err := r.db.Exec(
		`INSERT INTO managed_configs (id, kind, name, yaml_content, active, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 ON CONFLICT (kind, id) DO UPDATE SET
		   name = $3, yaml_content = $4, active = $5, updated_at = $7`,
		config.ID, config.Kind, config.Name, config.YAMLContent, config.Active, config.CreatedAt, config.UpdatedAt,
	)
	if err != nil {
		return model.ManagedConfig{}, fmt.Errorf("save managed config: %w", err)
	}
	return config, nil
}

func (r *PostgresRepository) ListManagedConfigs(kind string) ([]model.ManagedConfig, error) {
	query := `SELECT id, kind, name, yaml_content, active, created_at, updated_at FROM managed_configs WHERE 1=1`
	var args []any
	if kind != "" {
		query += ` AND kind = $1`
		args = append(args, kind)
	}
	query += ` ORDER BY kind ASC, updated_at DESC`

	rows, err := r.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list managed configs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var result []model.ManagedConfig
	for rows.Next() {
		var cfg model.ManagedConfig
		if err := rows.Scan(&cfg.ID, &cfg.Kind, &cfg.Name, &cfg.YAMLContent, &cfg.Active, &cfg.CreatedAt, &cfg.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan managed config: %w", err)
		}
		result = append(result, cfg)
	}
	return result, rows.Err()
}

func (r *PostgresRepository) GetManagedConfig(kind, id string) (model.ManagedConfig, error) {
	var cfg model.ManagedConfig
	err := r.db.QueryRow(
		`SELECT id, kind, name, yaml_content, active, created_at, updated_at
		 FROM managed_configs WHERE kind = $1 AND id = $2`,
		kind, id,
	).Scan(&cfg.ID, &cfg.Kind, &cfg.Name, &cfg.YAMLContent, &cfg.Active, &cfg.CreatedAt, &cfg.UpdatedAt)
	if err == sql.ErrNoRows {
		return model.ManagedConfig{}, fmt.Errorf("managed config not found: %s/%s", kind, id)
	}
	if err != nil {
		return model.ManagedConfig{}, fmt.Errorf("get managed config: %w", err)
	}
	return cfg, nil
}

func (r *PostgresRepository) DeleteManagedConfig(kind, id string) error {
	result, err := r.db.Exec(`DELETE FROM managed_configs WHERE kind = $1 AND id = $2`, kind, id)
	if err != nil {
		return fmt.Errorf("delete managed config: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("managed config not found: %s/%s", kind, id)
	}
	return nil
}

// ── Metrics ──

func (r *PostgresRepository) MetricsSnapshot() (model.MetricsSnapshot, error) {
	snapshot := model.MetricsSnapshot{}

	// Job stats.
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM jobs`).Scan(&snapshot.JobsTotal); err != nil {
		return snapshot, fmt.Errorf("count jobs: %w", err)
	}
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE status = $1`, model.JobStatusSuccess).Scan(&snapshot.JobsSuccess); err != nil {
		return snapshot, fmt.Errorf("count successful jobs: %w", err)
	}
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE status = $1`, model.JobStatusFailed).Scan(&snapshot.JobsFailed); err != nil {
		return snapshot, fmt.Errorf("count failed jobs: %w", err)
	}
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE status = $1`, model.JobStatusWaitingApproval).Scan(&snapshot.JobsWaitingApproval); err != nil {
		return snapshot, fmt.Errorf("count waiting approval jobs: %w", err)
	}

	// Workers online.
	threshold := time.Now().Add(-WorkerOnlineThreshold)
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM workers WHERE last_heartbeat_at >= $1`, threshold).Scan(&snapshot.WorkersOnline); err != nil {
		return snapshot, fmt.Errorf("count online workers: %w", err)
	}

	// Incident cases.
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM incident_cases`).Scan(&snapshot.IncidentCasesTotal); err != nil {
		return snapshot, fmt.Errorf("count incident cases: %w", err)
	}

	// Success/failure rates.
	completed := snapshot.JobsSuccess + snapshot.JobsFailed
	if completed > 0 {
		snapshot.JobSuccessRate = float64(snapshot.JobsSuccess) / float64(completed)
		snapshot.JobFailureRate = float64(snapshot.JobsFailed) / float64(completed)
	}

	// Average schedule latency.
	var avgLatency sql.NullFloat64
	if err := r.db.QueryRow(
		`SELECT AVG(EXTRACT(EPOCH FROM (started_at - created_at)))
		 FROM jobs WHERE started_at IS NOT NULL`,
	).Scan(&avgLatency); err != nil {
		return snapshot, fmt.Errorf("average schedule latency: %w", err)
	}
	if avgLatency.Valid {
		snapshot.AvgScheduleLatencySeconds = avgLatency.Float64
	}

	return snapshot, nil
}

// ── Conversations ──

func (r *PostgresRepository) CreateConversation(title string) (model.Conversation, error) {
	id := r.genID("conv")
	now := time.Now()
	_, err := r.db.Exec(
		`INSERT INTO conversations (id, title, created_at, updated_at) VALUES ($1, $2, $3, $4)`,
		id, title, now, now,
	)
	if err != nil {
		return model.Conversation{}, fmt.Errorf("create conversation: %w", err)
	}
	return model.Conversation{ID: id, Title: title, CreatedAt: now, UpdatedAt: now}, nil
}

func (r *PostgresRepository) GetConversation(id string) (model.Conversation, error) {
	var c model.Conversation
	err := r.db.QueryRow(
		`SELECT id, title, created_at, updated_at FROM conversations WHERE id = $1`, id,
	).Scan(&c.ID, &c.Title, &c.CreatedAt, &c.UpdatedAt)
	if err == sql.ErrNoRows {
		return model.Conversation{}, fmt.Errorf("conversation not found: %s", id)
	}
	if err != nil {
		return model.Conversation{}, fmt.Errorf("get conversation: %w", err)
	}
	return c, nil
}

func (r *PostgresRepository) ListConversations() ([]model.Conversation, error) {
	rows, err := r.db.Query(`SELECT id, title, created_at, updated_at FROM conversations ORDER BY updated_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list conversations: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			log.Printf("close conversation rows: %v", closeErr)
		}
	}()
	var list []model.Conversation
	for rows.Next() {
		var c model.Conversation
		if err := rows.Scan(&c.ID, &c.Title, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		list = append(list, c)
	}
	if list == nil {
		list = []model.Conversation{}
	}
	return list, rows.Err()
}

func (r *PostgresRepository) DeleteConversation(id string) error {
	result, err := r.db.Exec(`DELETE FROM conversations WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete conversation: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("conversation not found: %s", id)
	}
	return nil
}

func (r *PostgresRepository) AddConversationMessage(msg model.ConversationMessage) error {
	toolDataJSON, _ := json.Marshal(msg.ToolData)
	_, err := r.db.Exec(
		`INSERT INTO conversation_messages (conversation_id, role, content, tool_name, tool_data, step)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		msg.ConversationID, msg.Role, msg.Content, msg.ToolName, toolDataJSON, msg.Step,
	)
	if err != nil {
		return fmt.Errorf("add conversation message: %w", err)
	}
	// Touch conversation updated_at.
	_, _ = r.db.Exec(`UPDATE conversations SET updated_at = $1 WHERE id = $2`, time.Now(), msg.ConversationID)
	return nil
}

func (r *PostgresRepository) ListConversationMessages(conversationID string) ([]model.ConversationMessage, error) {
	rows, err := r.db.Query(
		`SELECT id, conversation_id, role, content, tool_name, tool_data, step, created_at
		 FROM conversation_messages WHERE conversation_id = $1 ORDER BY id ASC`, conversationID,
	)
	if err != nil {
		return nil, fmt.Errorf("list conversation messages: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			log.Printf("close conversation message rows: %v", closeErr)
		}
	}()
	var list []model.ConversationMessage
	for rows.Next() {
		var m model.ConversationMessage
		var toolDataJSON []byte
		if err := rows.Scan(&m.ID, &m.ConversationID, &m.Role, &m.Content, &m.ToolName, &toolDataJSON, &m.Step, &m.CreatedAt); err != nil {
			return nil, err
		}
		if len(toolDataJSON) > 0 {
			_ = json.Unmarshal(toolDataJSON, &m.ToolData)
		}
		list = append(list, m)
	}
	if list == nil {
		list = []model.ConversationMessage{}
	}
	return list, rows.Err()
}

// ── Agent runs ──

func (r *PostgresRepository) CreateAgentRun(run model.AgentRun) (model.AgentRun, error) {
	if run.ID == "" {
		run.ID = r.genID("run")
	}
	now := time.Now()
	run.CreatedAt, run.UpdatedAt = now, now
	if run.Status == "" {
		run.Status = model.AgentRunStatusQueued
	}
	if len(run.Transcript) == 0 {
		run.Transcript = []byte("[]")
	}
	_, err := r.db.Exec(`INSERT INTO agent_runs (id,input,conversation_id,status,trace_id,policy_version,prompt_version,transcript,next_step,answer,error,created_at,updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`, run.ID, run.Input, run.ConversationID, run.Status, run.TraceID, run.PolicyVersion, run.PromptVersion, run.Transcript, run.NextStep, run.Answer, run.Error, run.CreatedAt, run.UpdatedAt)
	if err != nil {
		return model.AgentRun{}, fmt.Errorf("create agent run: %w", err)
	}
	return run, nil
}
func (r *PostgresRepository) GetAgentRun(id string) (model.AgentRun, error) {
	var run model.AgentRun
	err := r.db.QueryRow(`SELECT id,input,conversation_id,status,trace_id,policy_version,prompt_version,transcript,next_step,answer,error,created_at,updated_at FROM agent_runs WHERE id=$1`, id).Scan(&run.ID, &run.Input, &run.ConversationID, &run.Status, &run.TraceID, &run.PolicyVersion, &run.PromptVersion, &run.Transcript, &run.NextStep, &run.Answer, &run.Error, &run.CreatedAt, &run.UpdatedAt)
	if err == sql.ErrNoRows {
		return model.AgentRun{}, fmt.Errorf("agent run not found: %s", id)
	}
	if err != nil {
		return model.AgentRun{}, fmt.Errorf("get agent run: %w", err)
	}
	return run, nil
}
func (r *PostgresRepository) ListAgentRunsByStatus(statuses ...model.AgentRunStatus) ([]model.AgentRun, error) {
	if len(statuses) == 0 {
		return []model.AgentRun{}, nil
	}
	args := make([]any, len(statuses))
	placeholders := make([]string, len(statuses))
	for i, status := range statuses {
		args[i] = status
		placeholders[i] = fmt.Sprintf("$%d", i+1)
	}
	rows, err := r.db.Query(`SELECT id,input,conversation_id,status,trace_id,policy_version,prompt_version,transcript,next_step,answer,error,created_at,updated_at FROM agent_runs WHERE status IN (`+strings.Join(placeholders, ",")+`) ORDER BY created_at`, args...)
	if err != nil {
		return nil, fmt.Errorf("list agent runs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []model.AgentRun{}
	for rows.Next() {
		var run model.AgentRun
		if err := rows.Scan(&run.ID, &run.Input, &run.ConversationID, &run.Status, &run.TraceID, &run.PolicyVersion, &run.PromptVersion, &run.Transcript, &run.NextStep, &run.Answer, &run.Error, &run.CreatedAt, &run.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, rows.Err()
}
func (r *PostgresRepository) UpdateAgentRun(run model.AgentRun) error {
	run.UpdatedAt = time.Now()
	if len(run.Transcript) == 0 {
		run.Transcript = []byte("[]")
	}
	result, err := r.db.Exec(`UPDATE agent_runs SET status=$1,transcript=$2,next_step=$3,answer=$4,error=$5,updated_at=$6 WHERE id=$7`, run.Status, run.Transcript, run.NextStep, run.Answer, run.Error, run.UpdatedAt, run.ID)
	if err != nil {
		return fmt.Errorf("update agent run: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("agent run not found: %s", run.ID)
	}
	return nil
}
func (r *PostgresRepository) AddAgentEvent(event model.AgentEvent) (model.AgentEvent, error) {
	data, _ := json.Marshal(event.Data)
	now := time.Now()
	err := r.db.QueryRow(`INSERT INTO agent_events (run_id,step,type,name,data,created_at) VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`, event.RunID, event.Step, event.Type, event.Name, data, now).Scan(&event.ID)
	if err != nil {
		return model.AgentEvent{}, fmt.Errorf("add agent event: %w", err)
	}
	event.CreatedAt = now
	return event, nil
}
func (r *PostgresRepository) ListAgentEvents(runID string, afterID int64) ([]model.AgentEvent, error) {
	rows, err := r.db.Query(`SELECT id,run_id,step,type,name,data,created_at FROM agent_events WHERE run_id=$1 AND id>$2 ORDER BY id`, runID, afterID)
	if err != nil {
		return nil, fmt.Errorf("list agent events: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []model.AgentEvent{}
	for rows.Next() {
		var e model.AgentEvent
		var data []byte
		if err := rows.Scan(&e.ID, &e.RunID, &e.Step, &e.Type, &e.Name, &data, &e.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(data, &e.Data)
		out = append(out, e)
	}
	return out, rows.Err()
}
func (r *PostgresRepository) CreateAgentToolCall(call model.AgentToolCall) error {
	if call.ID == "" {
		return fmt.Errorf("tool call id is required")
	}
	if len(call.Arguments) == 0 {
		call.Arguments = []byte("{}")
	}
	_, err := r.db.Exec(`INSERT INTO agent_tool_calls (id,run_id,step,tool_name,arguments,status,created_at) VALUES ($1,$2,$3,$4,$5,$6,$7) ON CONFLICT (id) DO NOTHING`, call.ID, call.RunID, call.Step, call.ToolName, call.Arguments, call.Status, time.Now())
	return err
}
func (r *PostgresRepository) CompleteAgentToolCall(call model.AgentToolCall) error {
	if len(call.Result) == 0 {
		call.Result = []byte("{}")
	}
	now := time.Now()
	_, err := r.db.Exec(`UPDATE agent_tool_calls SET result=$1,error=$2,status=$3,completed_at=$4 WHERE id=$5`, call.Result, call.Error, call.Status, now, call.ID)
	return err
}
