package api

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"adp/internal/domain/model"
)

func (s *Server) handleMetrics(w http.ResponseWriter, _ *http.Request) {
	var snapshot model.MetricsSnapshot
	var rag model.RAGMetrics
	if s.repo != nil {
		snapshot, _ = s.repo.MetricsSnapshot()
		rag, _ = s.repo.RAGMetrics()
	}
	// Recalculate online workers in real-time.
	if s.repo != nil {
		workers, _ := s.repo.ListWorkers()
		snapshot.WorkersOnline = 0
		threshold := time.Now().Add(-30 * time.Second)
		for _, w := range workers {
			if w.LastHeartbeatAt.After(threshold) {
				snapshot.WorkersOnline++
			}
		}
	}

	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	var out strings.Builder
	out.WriteString("# HELP adp_jobs_total Total number of jobs created.\n")
	out.WriteString("# TYPE adp_jobs_total gauge\n")
	writeMetricf(&out, "adp_jobs_total %d\n", snapshot.JobsTotal)
	out.WriteString("# HELP adp_jobs_success_total Total number of successful jobs.\n")
	out.WriteString("# TYPE adp_jobs_success_total gauge\n")
	writeMetricf(&out, "adp_jobs_success_total %d\n", snapshot.JobsSuccess)
	out.WriteString("# HELP adp_jobs_failed_total Total number of failed jobs.\n")
	out.WriteString("# TYPE adp_jobs_failed_total gauge\n")
	writeMetricf(&out, "adp_jobs_failed_total %d\n", snapshot.JobsFailed)
	out.WriteString("# HELP adp_jobs_waiting_approval Total number of jobs waiting for approval.\n")
	out.WriteString("# TYPE adp_jobs_waiting_approval gauge\n")
	writeMetricf(&out, "adp_jobs_waiting_approval %d\n", snapshot.JobsWaitingApproval)
	out.WriteString("# HELP adp_workers_online Current number of online workers.\n")
	out.WriteString("# TYPE adp_workers_online gauge\n")
	writeMetricf(&out, "adp_workers_online %d\n", snapshot.WorkersOnline)
	out.WriteString("# HELP adp_incident_cases_total Total number of stored incident cases.\n")
	out.WriteString("# TYPE adp_incident_cases_total gauge\n")
	writeMetricf(&out, "adp_incident_cases_total %d\n", snapshot.IncidentCasesTotal)
	out.WriteString("# HELP adp_rag_embeddings Number of incident-case embeddings by state.\n# TYPE adp_rag_embeddings gauge\n")
	writeMetricf(&out, "adp_rag_embeddings{status=\"queued\"} %d\n", rag.Queued)
	writeMetricf(&out, "adp_rag_embeddings{status=\"ready\"} %d\n", rag.Ready)
	writeMetricf(&out, "adp_rag_embeddings{status=\"failed\"} %d\n", rag.Failed)
	if s.ragMetrics != nil {
		calls, failures, latency := s.ragMetrics.snapshot()
		out.WriteString("# HELP adp_rag_embedding_generation_total Embedding generation attempts in this server process.\n# TYPE adp_rag_embedding_generation_total counter\n")
		writeMetricf(&out, "adp_rag_embedding_generation_total %d\n", calls)
		out.WriteString("# HELP adp_rag_embedding_generation_failures_total Failed embedding generation attempts in this server process.\n# TYPE adp_rag_embedding_generation_failures_total counter\n")
		writeMetricf(&out, "adp_rag_embedding_generation_failures_total %d\n", failures)
		out.WriteString("# HELP adp_rag_embedding_generation_latency_seconds_avg Average embedding generation latency in this server process.\n# TYPE adp_rag_embedding_generation_latency_seconds_avg gauge\n")
		writeMetricf(&out, "adp_rag_embedding_generation_latency_seconds_avg %.6f\n", latency)
	}
	out.WriteString("# HELP adp_job_success_rate Success rate of completed jobs.\n")
	out.WriteString("# TYPE adp_job_success_rate gauge\n")
	writeMetricf(&out, "adp_job_success_rate %.6f\n", snapshot.JobSuccessRate)
	out.WriteString("# HELP adp_job_failure_rate Failure rate of completed jobs.\n")
	out.WriteString("# TYPE adp_job_failure_rate gauge\n")
	writeMetricf(&out, "adp_job_failure_rate %.6f\n", snapshot.JobFailureRate)
	out.WriteString("# HELP adp_job_schedule_latency_seconds_avg Average queue-to-start latency in seconds.\n")
	out.WriteString("# TYPE adp_job_schedule_latency_seconds_avg gauge\n")
	writeMetricf(&out, "adp_job_schedule_latency_seconds_avg %.6f\n", snapshot.AvgScheduleLatencySeconds)
	if s.agentMetrics != nil {
		m := s.agentMetrics.snapshot()
		out.WriteString("# HELP adp_agent_run_success_rate Successful controlled Agent runs divided by completed runs.\n# TYPE adp_agent_run_success_rate gauge\n")
		runRate := float64(0)
		if m.runs > 0 {
			runRate = float64(m.successfulRuns) / float64(m.runs)
		}
		writeMetricf(&out, "adp_agent_run_success_rate %.6f\n", runRate)
		out.WriteString("# HELP adp_agent_tool_error_total Tool calls returning an error.\n# TYPE adp_agent_tool_error_total counter\n")
		writeMetricf(&out, "adp_agent_tool_error_total %d\n", m.toolErrors)
		out.WriteString("# HELP adp_agent_policy_rejection_total Policy-enforced tool rejections.\n# TYPE adp_agent_policy_rejection_total counter\n")
		writeMetricf(&out, "adp_agent_policy_rejection_total %d\n", m.policyRejections)
		out.WriteString("# HELP adp_agent_steps_avg Average LLM steps per Agent run.\n# TYPE adp_agent_steps_avg gauge\n")
		avgSteps := float64(0)
		if m.runs > 0 {
			avgSteps = float64(m.steps) / float64(m.runs)
		}
		writeMetricf(&out, "adp_agent_steps_avg %.6f\n", avgSteps)
		out.WriteString("# HELP adp_agent_approval_wait_seconds_avg Average time a job waited for human approval.\n# TYPE adp_agent_approval_wait_seconds_avg gauge\n")
		approvalWait := float64(0)
		if m.approvals > 0 {
			approvalWait = m.approvalWait.Seconds() / float64(m.approvals)
		}
		writeMetricf(&out, "adp_agent_approval_wait_seconds_avg %.6f\n", approvalWait)
		out.WriteString("# HELP adp_agent_model_latency_seconds_avg Average model completion latency.\n# TYPE adp_agent_model_latency_seconds_avg gauge\n")
		modelLatency := float64(0)
		if m.modelCalls > 0 {
			modelLatency = m.modelLatency.Seconds() / float64(m.modelCalls)
		}
		writeMetricf(&out, "adp_agent_model_latency_seconds_avg %.6f\n", modelLatency)
		out.WriteString("# HELP adp_agent_tool_latency_seconds_avg Average local tool latency.\n# TYPE adp_agent_tool_latency_seconds_avg gauge\n")
		toolLatency := float64(0)
		if m.toolLatencyCalls > 0 {
			toolLatency = m.toolLatency.Seconds() / float64(m.toolLatencyCalls)
		}
		writeMetricf(&out, "adp_agent_tool_latency_seconds_avg %.6f\n", toolLatency)
		out.WriteString("# HELP adp_agent_tokens_total Model token usage reported by the provider.\n# TYPE adp_agent_tokens_total counter\n")
		writeMetricf(&out, "adp_agent_tokens_total %d\n", m.totalTokens)
		out.WriteString("# HELP adp_agent_prompt_tokens_total Prompt token usage reported by the provider.\n# TYPE adp_agent_prompt_tokens_total counter\n")
		writeMetricf(&out, "adp_agent_prompt_tokens_total %d\n", m.promptTokens)
		out.WriteString("# HELP adp_agent_completion_tokens_total Completion token usage reported by the provider.\n# TYPE adp_agent_completion_tokens_total counter\n")
		writeMetricf(&out, "adp_agent_completion_tokens_total %d\n", m.completionTokens)
		out.WriteString("# HELP adp_agent_token_cost_usd_total Configured-price estimate of model token cost in USD.\n# TYPE adp_agent_token_cost_usd_total counter\n")
		writeMetricf(&out, "adp_agent_token_cost_usd_total %.8f\n", m.tokenCostUSD)
		out.WriteString("# HELP adp_agent_context_tokens_estimated_total Server-side conservative prompt-token estimates.\n# TYPE adp_agent_context_tokens_estimated_total counter\n")
		writeMetricf(&out, "adp_agent_context_tokens_estimated_total %d\n", m.contextEstimatedTokens)
		out.WriteString("# HELP adp_agent_context_over_budget_total Context projections rejected before calling the model.\n# TYPE adp_agent_context_over_budget_total counter\n")
		writeMetricf(&out, "adp_agent_context_over_budget_total %d\n", m.contextOverBudget)
		out.WriteString("# HELP adp_agent_context_snapshot_failures_total Failed writes of model-context audit snapshots.\n# TYPE adp_agent_context_snapshot_failures_total counter\n")
		writeMetricf(&out, "adp_agent_context_snapshot_failures_total %d\n", m.contextSnapshotFailures)
		out.WriteString("# HELP adp_agent_context_shadow_samples_total Context compression comparisons recorded without changing live requests.\n# TYPE adp_agent_context_shadow_samples_total counter\n")
		writeMetricf(&out, "adp_agent_context_shadow_samples_total %d\n", m.contextShadowSamples)
		out.WriteString("# HELP adp_agent_context_shadow_baseline_tokens_total Token estimates for uncompressed conversation context.\n# TYPE adp_agent_context_shadow_baseline_tokens_total counter\n")
		writeMetricf(&out, "adp_agent_context_shadow_baseline_tokens_total %d\n", m.contextShadowBaselineTokens)
		out.WriteString("# HELP adp_agent_context_shadow_compacted_tokens_total Token estimates for compacted conversation context.\n# TYPE adp_agent_context_shadow_compacted_tokens_total counter\n")
		writeMetricf(&out, "adp_agent_context_shadow_compacted_tokens_total %d\n", m.contextShadowCompactedTokens)
	}
	if _, err := w.Write([]byte(out.String())); err != nil {
		return
	}

	logEvent("metrics", "scrape", map[string]any{
		"jobs_total":           snapshot.JobsTotal,
		"workers_online":       snapshot.WorkersOnline,
		"incident_cases_total": snapshot.IncidentCasesTotal,
	})
}

func writeMetricf(out *strings.Builder, format string, args ...any) {
	_, _ = fmt.Fprintf(out, format, args...)
}
