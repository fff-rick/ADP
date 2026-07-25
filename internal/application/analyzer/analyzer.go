package analyzer

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"adp/internal/domain/model"
	"adp/internal/infrastructure/llm"
)

// Analyzer examines diagnosis results and produces an AnalysisReport.
type Analyzer struct {
	llmClient    llm.Client
	systemPrompt string
	rules        []AnalysisRule
}

// AnalysisRule is an API-managed LLM fallback conclusion for a diagnosis type.
type AnalysisRule struct {
	TriggerType    string   `yaml:"trigger_type"`
	FaultType      string   `yaml:"fault_type"`
	PossibleCauses []string `yaml:"possible_causes"`
	Suggestions    []string `yaml:"suggestions"`
	Confidence     float64  `yaml:"confidence"`
}

func New(llmClient llm.Client) *Analyzer {
	return &Analyzer{llmClient: llmClient}
}

// SetSystemPrompt replaces the analyzer system prompt at runtime.
func (a *Analyzer) SetSystemPrompt(prompt string) {
	prompt = strings.TrimSpace(prompt)
	if prompt != "" {
		a.systemPrompt = prompt
	}
}

// SetRules replaces the managed LLM fallback rules.
func (a *Analyzer) SetRules(rules []AnalysisRule) error {
	for i, rule := range rules {
		if strings.TrimSpace(rule.TriggerType) == "" || strings.TrimSpace(rule.FaultType) == "" || len(rule.PossibleCauses) == 0 || len(rule.Suggestions) == 0 {
			return fmt.Errorf("analysis rule %d requires trigger_type, fault_type, possible_causes and suggestions", i+1)
		}
	}
	a.rules = rules
	return nil
}

// Analyze takes a completed diagnosis plan and produces an analysis report.
func (a *Analyzer) Analyze(ctx context.Context, plan model.DiagnosisPlan) (*model.AnalysisReport, error) {
	if len(plan.Steps) == 0 {
		return nil, fmt.Errorf("plan has no steps")
	}

	if a.llmClient != nil {
		return a.analyzeWithLLM(ctx, plan)
	}

	return a.analyzeWithRules(plan), nil
}

func (a *Analyzer) analyzeWithLLM(ctx context.Context, plan model.DiagnosisPlan) (*model.AnalysisReport, error) {
	var summary strings.Builder
	summary.WriteString(fmt.Sprintf("Diagnosis plan: %s (%s)\n\n", plan.Title, plan.TriggerType)) //nolint:staticcheck

	for _, step := range plan.Steps {
		summary.WriteString(fmt.Sprintf("Step %d: %s (%s)\n", step.StepNo, step.Name, step.Description)) //nolint:staticcheck
		if step.Result != nil {
			summary.WriteString(fmt.Sprintf("  stdout: %s\n", truncate(step.Result.Stdout, 500)))                         //nolint:staticcheck
			summary.WriteString(fmt.Sprintf("  stderr: %s\n", truncate(step.Result.Stderr, 500)))                         //nolint:staticcheck
			summary.WriteString(fmt.Sprintf("  exit_code: %d, success: %v\n", step.Result.ExitCode, step.Result.Success)) //nolint:staticcheck
		}
	}

	messages := []llm.Message{
		{Role: "system", Content: a.systemPrompt},
		{Role: "user", Content: summary.String()},
	}

	raw, err := a.llmClient.Chat(ctx, messages)
	if err != nil {
		return a.analyzeWithRules(plan), nil
	}

	report, err := parseLLMReport(raw, plan.ID)
	if err != nil {
		return a.analyzeWithRules(plan), nil
	}
	return report, nil
}

func parseLLMReport(raw string, planID string) (*model.AnalysisReport, error) {
	payload := extractJSON(raw)
	var report model.AnalysisReport
	if err := json.Unmarshal([]byte(payload), &report); err != nil {
		return nil, fmt.Errorf("parse llm analysis: %w", err)
	}
	if strings.TrimSpace(report.FaultType) == "" {
		return nil, fmt.Errorf("llm analysis missing fault_type")
	}
	if len(report.PossibleCauses) == 0 {
		return nil, fmt.Errorf("llm analysis missing possible_causes")
	}
	if len(report.Suggestions) == 0 {
		return nil, fmt.Errorf("llm analysis missing suggestions")
	}
	report.PlanID = planID
	report.RawAnalysis = raw
	report.CreatedAt = time.Now()
	return &report, nil
}

func extractJSON(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```json")
		s = strings.TrimPrefix(s, "```")
		s = strings.TrimSuffix(s, "```")
		s = strings.TrimSpace(s)
	}
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start != -1 && end > start {
		return s[start : end+1]
	}
	return s
}

func (a *Analyzer) analyzeWithRules(plan model.DiagnosisPlan) *model.AnalysisReport {
	for _, rule := range a.rules {
		if rule.TriggerType == plan.TriggerType {
			return &model.AnalysisReport{PlanID: plan.ID, FaultType: rule.FaultType, PossibleCauses: append([]string(nil), rule.PossibleCauses...), Suggestions: append([]string(nil), rule.Suggestions...), Confidence: rule.Confidence, RawAnalysis: "managed analysis rule", CreatedAt: time.Now()}
		}
	}
	return &model.AnalysisReport{PlanID: plan.ID, FaultType: "unknown", PossibleCauses: []string{"没有匹配的受管分析规则"}, Suggestions: []string{"请配置 analysis_rules 或检查 LLM 服务"}, CreatedAt: time.Now()}
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
