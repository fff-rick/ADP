package api

import (
	"net/http"
	"sort"
	"time"

	"adp/internal/domain/model"
	"adp/internal/infrastructure/db"
)

type dashboardSummaryResponse struct {
	User             model.User            `json:"user"`
	CurrentTime      string                `json:"current_time"`
	Metrics          model.MetricsSnapshot `json:"metrics"`
	RecentJobs       []model.Job           `json:"recent_jobs"`
	Workers          []model.Worker        `json:"workers"`
	PendingApprovals []model.Job           `json:"pending_approvals"`
	RecentCases      []model.IncidentCase  `json:"recent_cases"`
	RecentAuditLogs  []model.AuditLog      `json:"recent_audit_logs"`
	TemplatesTotal   int                    `json:"templates_total"`
	Templates        []model.CommandTemplate `json:"templates"`
}

func (s *Server) handleDashboardSummary(w http.ResponseWriter, r *http.Request) {
	var (
		jobs    []model.Job
		workers []model.Worker
		pending []model.Job
		cases   []model.IncidentCase
		logs    []model.AuditLog
		metrics model.MetricsSnapshot
	)

	if s.repo != nil {
		jobs, _ = s.repo.ListJobs(db.JobFilter{Limit: 8})
		if jobs == nil {
			jobs = []model.Job{}
		}
		workers, _ = s.repo.ListWorkers()
		if workers == nil {
			workers = []model.Worker{}
		}
		pending, _ = s.repo.ListPendingApprovalJobs()
		if pending == nil {
			pending = []model.Job{}
		}
		cases, _ = s.repo.ListIncidentCases(model.IncidentCaseFilter{Limit: 4})
		if cases == nil {
			cases = []model.IncidentCase{}
		}
		logs, _ = s.repo.ListAuditLogs("", "")
		if logs == nil {
			logs = []model.AuditLog{}
		}
		metrics, _ = s.repo.MetricsSnapshot()
	}

	sort.Slice(jobs, func(i, j int) bool { return jobs[i].CreatedAt.After(jobs[j].CreatedAt) })
	sort.Slice(workers, func(i, j int) bool { return workers[i].UpdatedAt.After(workers[j].UpdatedAt) })
	sort.Slice(pending, func(i, j int) bool { return pending[i].CreatedAt.After(pending[j].CreatedAt) })
	sort.Slice(logs, func(i, j int) bool { return logs[i].CreatedAt.After(logs[j].CreatedAt) })

	jobs = limitJobs(jobs, 8)
	pending = limitJobs(pending, 6)
	logs = limitAuditLogs(logs, 8)

	writeJSON(w, http.StatusOK, dashboardSummaryResponse{
		User:             currentUser(r),
		CurrentTime:      time.Now().Format(time.RFC3339),
		Metrics:          metrics,
		RecentJobs:       jobs,
		Workers:          workers,
		PendingApprovals: pending,
		RecentCases:      cases,
		RecentAuditLogs:  logs,
		TemplatesTotal:   len(s.templateEng.ListTemplates()),
		Templates:        s.templateEng.ListTemplates(),
	})
}

func limitJobs(values []model.Job, max int) []model.Job {
	if len(values) <= max {
		return values
	}
	return values[:max]
}

func limitAuditLogs(values []model.AuditLog, max int) []model.AuditLog {
	if len(values) <= max {
		return values
	}
	return values[:max]
}
