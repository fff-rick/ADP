package policy

import (
	"fmt"
	"strings"

	"adp/internal/domain/model"
)

// Engine enforces execution policies: tool whitelist, template whitelist,
// and risk-level based gating.
type Engine struct {
	allowedTools       map[string]bool
	allowedTemplates   map[string]bool
	highRiskKeywords   []string
	approvalRiskLevels map[model.RiskLevel]bool
}

// NewEngine creates a deny-by-default policy engine. Authorization is supplied
// exclusively by the active managed policy configuration.
func NewEngine() *Engine {
	return &Engine{allowedTools: map[string]bool{}, allowedTemplates: map[string]bool{}, approvalRiskLevels: map[model.RiskLevel]bool{}}
}

// Configure replaces runtime policy settings from managed configuration.
func (e *Engine) Configure(allowedTools, allowedTemplates, highRiskKeywords []string, approvalRiskLevels []model.RiskLevel) {
	e.allowedTools = stringSet(allowedTools)
	e.allowedTemplates = stringSet(allowedTemplates)
	e.highRiskKeywords = highRiskKeywords
	e.approvalRiskLevels = make(map[model.RiskLevel]bool, len(approvalRiskLevels))
	for _, level := range approvalRiskLevels {
		e.approvalRiskLevels[level] = true
	}
}

func stringSet(values []string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		result[value] = true
	}
	return result
}

// ValidateTemplate checks whether a template code is allowed.
func (e *Engine) ValidateTemplate(code string) error {
	if !e.allowedTemplates[code] {
		return fmt.Errorf("template not in whitelist: %s", code)
	}
	return nil
}

// ValidateCommand checks whether the leading tool in a command is allowed.
func (e *Engine) ValidateCommand(cmd string) error {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return fmt.Errorf("command is empty")
	}

	tool := strings.Split(cmd, " ")[0]
	if !e.allowedTools[tool] {
		return fmt.Errorf("tool not in whitelist: %s", tool)
	}
	return nil
}

// AssessRisk returns a risk level for the given intent.
// High-risk intents (data deletion, service restart, config changes)
// require human approval before execution.
func (e *Engine) AssessRisk(intent model.TaskIntent) model.RiskLevel {
	if intent.RiskLevel == model.RiskLevelHigh {
		return model.RiskLevelHigh
	}

	combined := intent.Intent + " " + intent.TargetType
	for _, kw := range e.highRiskKeywords {
		if strings.Contains(strings.ToLower(combined), kw) {
			return model.RiskLevelHigh
		}
	}

	return intent.RiskLevel
}

// IsHighRisk is a convenience check.
func (e *Engine) IsHighRisk(level model.RiskLevel) bool {
	return level == model.RiskLevelHigh
}

func (e *Engine) MergeRisk(levels ...model.RiskLevel) model.RiskLevel {
	result := model.RiskLevelLow
	for _, level := range levels {
		switch level {
		case model.RiskLevelHigh:
			return model.RiskLevelHigh
		case model.RiskLevelMedium:
			if result != model.RiskLevelHigh {
				result = model.RiskLevelMedium
			}
		}
	}
	return result
}

func (e *Engine) RequiresManualApproval(level model.RiskLevel) bool {
	return e.approvalRiskLevels[level]
}
