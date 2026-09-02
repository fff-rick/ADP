package api

import (
	"strings"
	"sync"
	"time"

	"adp/internal/infrastructure/llm"
)

// agentMetrics is intentionally local and dependency-free. Prometheus scrapes
// aggregate values; durable run/job records remain the source of truth.
type agentMetrics struct {
	mu                                                                               sync.Mutex
	runs, successfulRuns, toolCalls, toolErrors, policyRejections, steps             int64
	approvalWait, modelLatency, toolLatency                                          time.Duration
	approvals, modelCalls, toolLatencyCalls                                          int64
	promptTokens, completionTokens, totalTokens                                      int64
	contextCalls, contextEstimatedTokens, contextOverBudget, contextSnapshotFailures int64
	contextShadowSamples, contextShadowBaselineTokens, contextShadowCompactedTokens  int64
	inputTokenCostPer1K, outputTokenCostPer1K, tokenCostUSD                          float64
}

func (m *agentMetrics) contextShadow(baseline, compacted int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.contextShadowSamples++
	m.contextShadowBaselineTokens += int64(baseline)
	m.contextShadowCompactedTokens += int64(compacted)
}

func (m *agentMetrics) context(estimate, budget int, snapshotErr error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.contextCalls++
	m.contextEstimatedTokens += int64(estimate)
	if budget > 0 && estimate > budget {
		m.contextOverBudget++
	}
	if snapshotErr != nil {
		m.contextSnapshotFailures++
	}
}

func (m *agentMetrics) model(latency time.Duration, usage llm.Usage) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.modelCalls++
	m.modelLatency += latency
	m.promptTokens += int64(usage.PromptTokens)
	m.completionTokens += int64(usage.CompletionTokens)
	m.totalTokens += int64(usage.TotalTokens)
	m.tokenCostUSD += float64(usage.PromptTokens)/1000*m.inputTokenCostPer1K + float64(usage.CompletionTokens)/1000*m.outputTokenCostPer1K
}
func (m *agentMetrics) tool(latency time.Duration, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.toolCalls++
	m.toolLatencyCalls++
	m.toolLatency += latency
	if err != nil {
		m.toolErrors++
		if strings.Contains(err.Error(), "template") || strings.Contains(err.Error(), "sensitive") || strings.Contains(err.Error(), "unauthorized") {
			m.policyRejections++
		}
	}
}
func (m *agentMetrics) complete(steps int, success bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.runs++
	m.steps += int64(steps)
	if success {
		m.successfulRuns++
	}
}
func (m *agentMetrics) approval(created time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.approvals++
	m.approvalWait += time.Since(created)
}

type agentMetricsSnapshot struct {
	runs, successfulRuns, toolCalls, toolErrors, policyRejections, steps                 int64
	approvalWait, modelLatency, toolLatency                                              time.Duration
	approvals, modelCalls, toolLatencyCalls, promptTokens, completionTokens, totalTokens int64
	contextCalls, contextEstimatedTokens, contextOverBudget, contextSnapshotFailures     int64
	contextShadowSamples, contextShadowBaselineTokens, contextShadowCompactedTokens      int64
	tokenCostUSD                                                                         float64
}

func (m *agentMetrics) snapshot() agentMetricsSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	return agentMetricsSnapshot{m.runs, m.successfulRuns, m.toolCalls, m.toolErrors, m.policyRejections, m.steps, m.approvalWait, m.modelLatency, m.toolLatency, m.approvals, m.modelCalls, m.toolLatencyCalls, m.promptTokens, m.completionTokens, m.totalTokens, m.contextCalls, m.contextEstimatedTokens, m.contextOverBudget, m.contextSnapshotFailures, m.contextShadowSamples, m.contextShadowBaselineTokens, m.contextShadowCompactedTokens, m.tokenCostUSD}
}

func (m *agentMetrics) dashboard() map[string]float64 {
	s := m.snapshot()
	runSuccessRate, avgSteps, approvalWait, modelLatency, toolLatency := 0.0, 0.0, 0.0, 0.0, 0.0
	if s.runs > 0 {
		runSuccessRate, avgSteps = float64(s.successfulRuns)/float64(s.runs), float64(s.steps)/float64(s.runs)
	}
	if s.approvals > 0 {
		approvalWait = s.approvalWait.Seconds() / float64(s.approvals)
	}
	if s.modelCalls > 0 {
		modelLatency = s.modelLatency.Seconds() / float64(s.modelCalls)
	}
	if s.toolLatencyCalls > 0 {
		toolLatency = s.toolLatency.Seconds() / float64(s.toolLatencyCalls)
	}
	return map[string]float64{"run_success_rate": runSuccessRate, "tool_errors": float64(s.toolErrors), "policy_rejections": float64(s.policyRejections), "avg_steps": avgSteps, "approval_wait_seconds": approvalWait, "model_latency_seconds": modelLatency, "tool_latency_seconds": toolLatency, "total_tokens": float64(s.totalTokens), "token_cost_usd": s.tokenCostUSD}
}
