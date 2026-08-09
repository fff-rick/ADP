package policy

import (
	"fmt"
	"strings"

	"adp/internal/domain/model"
)

// Engine enforces execution policies: tool blocklist, template whitelist,
// and risk-level based gating.
type Engine struct {
	blockedTools       map[string]bool
	allowedTemplates   map[string]bool
	highRiskKeywords   []string
	approvalRiskLevels map[model.RiskLevel]bool
}

// NewEngine creates a deny-by-default policy engine. Authorization is supplied
// exclusively by the active managed policy configuration.
func NewEngine() *Engine {
	return &Engine{blockedTools: map[string]bool{}, allowedTemplates: map[string]bool{}, approvalRiskLevels: map[model.RiskLevel]bool{}}
}

// Configure replaces runtime policy settings from managed configuration.
func (e *Engine) Configure(blockedTools, allowedTemplates, highRiskKeywords []string, approvalRiskLevels []model.RiskLevel) {
	e.blockedTools = stringSet(blockedTools)
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

// ValidateCommand checks whether the leading tool in a command is blocked.
// Returns nil if the tool is NOT in the blocklist.
func (e *Engine) ValidateCommand(cmd string) error {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return fmt.Errorf("command is empty")
	}

	tool := strings.Split(cmd, " ")[0]
	if e.blockedTools[tool] {
		return fmt.Errorf("tool is blocked: %s", tool)
	}
	return nil
}

// AssessCommandRisk evaluates risk for a raw shell command by scanning for
// high-risk keywords. Used for agent-shell mode.
func (e *Engine) AssessCommandRisk(command string) model.RiskLevel {
	combined := strings.ToLower(command)
	for _, kw := range e.highRiskKeywords {
		if strings.Contains(combined, kw) {
			return model.RiskLevelHigh
		}
	}
	return model.RiskLevelLow
}

// AssessRisk returns a risk level for the given intent.
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

// BlockedTools returns a copy of the blocked tools set for use by workers.
func (e *Engine) BlockedTools() map[string]bool {
	out := make(map[string]bool, len(e.blockedTools))
	for k, v := range e.blockedTools {
		out[k] = v
	}
	return out
}
