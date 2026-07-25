package parser

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"adp/internal/config"
	"adp/internal/domain/model"
	"adp/internal/domain/policy"
	"adp/internal/domain/template"
	"adp/internal/infrastructure/llm"
)

func buildSystemPrompt(ctx *config.AIContext) string {
	var sb strings.Builder

	// Part 1: Environment context (if available)
	if ctx != nil {
		sb.WriteString(ctx.ToPromptSection())
	}

	return sb.String()
}

// BuildTaskIntentPrompt returns the enhanced prompt for task intent parsing.
func BuildTaskIntentPrompt(ctx *config.AIContext) string {
	return buildSystemPrompt(ctx)
}

// BuildYAMLPrompt is kept for compatibility. New code should use BuildTaskIntentPrompt.
func BuildYAMLPrompt(ctx *config.AIContext) string {
	return BuildTaskIntentPrompt(ctx)
}

// IntentRule is an API-managed fallback rule. Rules select only an approved
// template; they never carry executable commands.
type IntentRule struct {
	Pattern         string            `yaml:"pattern"`
	Intent          string            `yaml:"intent"`
	TargetType      string            `yaml:"target_type"`
	RiskLevel       model.RiskLevel   `yaml:"risk_level"`
	Parameters      map[string]string `yaml:"parameters"`
	MatchedTemplate string            `yaml:"matched_template"`
}

type compiledIntentRule struct {
	IntentRule
	pattern *regexp.Regexp
}

// Parser converts natural language into structured TaskIntent.
type Parser struct {
	llmClient    llm.Client
	templates    *template.Engine
	policy       *policy.Engine
	systemPrompt string
	rules        []compiledIntentRule
}

// SetRules validates and replaces the managed rules at runtime.
func (p *Parser) SetRules(rules []IntentRule) error {
	compiled := make([]compiledIntentRule, 0, len(rules))
	for i, rule := range rules {
		if strings.TrimSpace(rule.Pattern) == "" || strings.TrimSpace(rule.Intent) == "" || strings.TrimSpace(rule.TargetType) == "" {
			return fmt.Errorf("rule %d requires pattern, intent and target_type", i+1)
		}
		re, err := regexp.Compile("(?i)" + rule.Pattern)
		if err != nil {
			return fmt.Errorf("rule %d has invalid pattern: %w", i+1, err)
		}
		if rule.RiskLevel == "" {
			rule.RiskLevel = model.RiskLevelLow
		}
		compiled = append(compiled, compiledIntentRule{IntentRule: rule, pattern: re})
	}
	p.rules = compiled
	return nil
}

// NewParser creates a Parser. llmClient may be nil; rule-based fallback is used in that case.
func NewParser(llmClient llm.Client, templates *template.Engine, policy *policy.Engine) *Parser {
	return &Parser{
		llmClient:    llmClient,
		templates:    templates,
		policy:       policy,
		systemPrompt: buildSystemPrompt(nil),
	}
}

// SetAIContext injects AI context configuration into the parser's system prompt.
func (p *Parser) SetAIContext(ctx *config.AIContext) {
	if ctx != nil {
		p.systemPrompt = buildSystemPrompt(ctx)
	}
}

// SetSystemPrompt replaces the parser system prompt at runtime.
func (p *Parser) SetSystemPrompt(prompt string) {
	prompt = strings.TrimSpace(prompt)
	if prompt != "" {
		p.systemPrompt = prompt
	}
}

// Parse converts natural language input into a TaskIntent.
func (p *Parser) Parse(ctx context.Context, input string) (*model.TaskIntent, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return nil, fmt.Errorf("input is empty")
	}

	var intent *model.TaskIntent
	var err error

	if p.llmClient != nil {
		intent, err = p.parseWithLLM(ctx, input)
		if err != nil {
			// Keep the original LLM error: callers must be able to distinguish a
			// genuine unsupported request from a failed model connection.
			llmErr := err
			intent, err = p.parseWithRules(input)
			if err != nil {
				return nil, fmt.Errorf("LLM parsing failed (%v); rule-based fallback also failed: %w", llmErr, err)
			}
			intent.ParseSource = "rule_fallback"
			intent.ParseWarning = "LLM parsing failed; result was produced by the rule fallback: " + truncate(llmErr.Error(), 200)
		}
	} else {
		intent, err = p.parseWithRules(input)
		if err == nil {
			intent.ParseSource = "rule"
		}
	}

	if err != nil {
		return nil, fmt.Errorf("parse failed: %w", err)
	}

	if intent.Intent == "unsupported" {
		return nil, fmt.Errorf("unrecognized or unsupported task: %s", input)
	}
	if intent.ParseSource == "" {
		intent.ParseSource = "llm"
	}

	return intent, nil
}

func (p *Parser) parseWithLLM(ctx context.Context, input string) (*model.TaskIntent, error) {
	messages := []llm.Message{
		{Role: "system", Content: p.systemPrompt},
		{Role: "user", Content: input},
	}

	raw, err := p.llmClient.Chat(ctx, messages)
	if err != nil {
		return nil, fmt.Errorf("llm call failed: %w", err)
	}

	// Extract JSON from response (handle markdown code blocks).
	raw = extractJSON(raw)

	var intent model.TaskIntent
	if err := json.Unmarshal([]byte(raw), &intent); err != nil {
		return nil, fmt.Errorf("failed to parse LLM response as JSON: %w (raw=%s)", err, truncate(raw, 200))
	}

	if intent.Intent == "" {
		return nil, fmt.Errorf("LLM returned empty intent")
	}

	return &intent, nil
}

func (p *Parser) parseWithRules(input string) (*model.TaskIntent, error) {
	lower := strings.ToLower(input)
	for _, r := range p.rules {
		if r.pattern.MatchString(lower) {
			params := make(map[string]string, len(r.Parameters))
			for k, v := range r.Parameters {
				params[k] = v
			}
			return &model.TaskIntent{Intent: r.Intent, TargetType: r.TargetType, RiskLevel: r.RiskLevel, Parameters: params, MatchedTemplate: r.MatchedTemplate, Schedule: extractCron(lower)}, nil
		}
	}

	return nil, fmt.Errorf("unable to parse with rule-based parser: %s", input)
}

// extractJSON extracts JSON from a string that may contain markdown fences.
func extractJSON(s string) string {
	s = strings.TrimSpace(s)

	if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```json")
		s = strings.TrimPrefix(s, "```")
		s = strings.TrimSuffix(s, "```")
		s = strings.TrimSpace(s)
	}

	// Find the outermost { }.
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start != -1 && end > start {
		s = s[start : end+1]
	}

	return s
}

func extractCron(input string) string {
	if strings.Contains(input, "每天") || strings.Contains(input, "每日") || strings.Contains(input, "daily") {
		return "0 0 * * *"
	}
	if strings.Contains(input, "每小时") || strings.Contains(input, "hourly") {
		return "0 * * * *"
	}
	if strings.Contains(input, "每周") || strings.Contains(input, "weekly") {
		return "0 0 * * 0"
	}
	return ""
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
