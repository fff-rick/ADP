package parser

import (
	"context"
	"errors"
	"strings"
	"testing"

	"adp/internal/domain/model"
	"adp/internal/domain/policy"
	"adp/internal/domain/template"
	"adp/internal/infrastructure/llm"
)

func TestParseWithRules_MySQLBackup(t *testing.T) {
	p := newRuleParser(t, nil)

	tests := []struct {
		input          string
		wantIntent     string
		wantTargetType string
		wantRiskLevel  model.RiskLevel
	}{
		{"每天凌晨备份 MySQL 数据库", "create_scheduled_backup", "mysql", model.RiskLevelMedium},
		{"备份 MySQL 数据库 mydb", "create_scheduled_backup", "mysql", model.RiskLevelMedium},
		{"每天备份数据库", "create_scheduled_backup", "mysql", model.RiskLevelMedium},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			intent, err := p.Parse(context.Background(), tt.input)
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}
			if intent.Intent != tt.wantIntent {
				t.Errorf("intent = %s, want %s", intent.Intent, tt.wantIntent)
			}
			if intent.TargetType != tt.wantTargetType {
				t.Errorf("target_type = %s, want %s", intent.TargetType, tt.wantTargetType)
			}
			if intent.RiskLevel != tt.wantRiskLevel {
				t.Errorf("risk_level = %s, want %s", intent.RiskLevel, tt.wantRiskLevel)
			}
			if intent.MatchedTemplate != "mysql_backup" {
				t.Errorf("matched_template = %s, want mysql_backup", intent.MatchedTemplate)
			}
		})
	}
}

func TestParseWithRules_HTTPHealthCheck(t *testing.T) {
	p := newRuleParser(t, nil)

	tests := []string{
		"检查 HTTP 服务健康状态",
		"对网站做健康检查",
		"health check the service",
	}

	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			intent, err := p.Parse(context.Background(), input)
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}
			if intent.Intent != "health_check" {
				t.Errorf("intent = %s, want health_check", intent.Intent)
			}
			if intent.TargetType != "http_service" {
				t.Errorf("target_type = %s, want http_service", intent.TargetType)
			}
			if intent.RiskLevel != model.RiskLevelLow {
				t.Errorf("risk_level = %s, want low", intent.RiskLevel)
			}
		})
	}
}

func TestParseWithRules_NginxMultiStepDiagnosis(t *testing.T) {
	p := newRuleParser(t, nil)

	intent, err := p.Parse(context.Background(), "帮我检查 nginx 是否正常运行，并查看错误日志")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if intent.Intent != "diagnose" {
		t.Fatalf("intent = %s, want diagnose", intent.Intent)
	}
	if intent.TargetType != "nginx" {
		t.Fatalf("target_type = %s, want nginx", intent.TargetType)
	}
	if intent.MatchedTemplate != "" {
		t.Fatalf("matched_template = %s, want empty for multi-step diagnosis", intent.MatchedTemplate)
	}
}

func TestParseWithLLMJSONIntent(t *testing.T) {
	p := NewParser(staticLLMClient{
		response: `{"intent":"diagnose","target_type":"nginx","risk_level":"low","parameters":{"ServiceType":"nginx"}}`,
	}, template.NewEngine(), policy.NewEngine())

	intent, err := p.Parse(context.Background(), "帮我检查 nginx 是否正常运行，并查看错误日志")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if intent.Intent != "diagnose" || intent.TargetType != "nginx" {
		t.Fatalf("unexpected intent: %+v", intent)
	}
}

func TestParseGitHubConnectivityWithRules(t *testing.T) {
	p := newRuleParser(t, nil)
	intent, err := p.Parse(context.Background(), "测试主机与github的连通性")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if intent.Intent != "health_check" || intent.Parameters["URL"] != "https://github.com" {
		t.Fatalf("unexpected intent: %+v", intent)
	}
	if intent.ParseSource != "rule" {
		t.Fatalf("parse_source = %q, want rule", intent.ParseSource)
	}
}

func TestParseReportsLLMFailureWhenFallbackCannotParse(t *testing.T) {
	p := newRuleParser(t, staticLLMClient{err: errors.New("connection refused")})
	_, err := p.Parse(context.Background(), "unrecognizable task")
	if err == nil || !strings.Contains(err.Error(), "LLM parsing failed (llm call failed: connection refused)") {
		t.Fatalf("expected actionable LLM failure, got %v", err)
	}
}

func TestParseMarksRuleFallbackAfterLLMFailure(t *testing.T) {
	p := newRuleParser(t, staticLLMClient{err: errors.New("connection refused")})
	intent, err := p.Parse(context.Background(), "测试主机与github的连通性")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if intent.ParseSource != "rule_fallback" || intent.ParseWarning == "" {
		t.Fatalf("fallback diagnostic missing: %+v", intent)
	}
}

func TestParseWithRules_UnrecognizedInput(t *testing.T) {
	p := newRuleParser(t, nil)

	_, err := p.Parse(context.Background(), "random gibberish")
	if err == nil {
		t.Fatal("expected error for unrecognized input")
	}
}

func TestParseWithRules_EmptyInput(t *testing.T) {
	p := NewParser(nil, template.NewEngine(), policy.NewEngine())

	_, err := p.Parse(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for empty input")
	}
}

func TestScheduleExtraction(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"每天凌晨备份", "0 0 * * *"},
		{"每日备份", "0 0 * * *"},
		{"每小时检查", "0 * * * *"},
		{"每周备份", "0 0 * * 0"},
		{"立即备份", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := extractCron(tt.input)
			if got != tt.want {
				t.Errorf("extractCron(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

type staticLLMClient struct {
	response string
	err      error
}

func newRuleParser(t *testing.T, client llm.Client) *Parser {
	t.Helper()
	p := NewParser(client, template.NewEngine(), policy.NewEngine())
	err := p.SetRules([]IntentRule{
		{Pattern: `(mysql|数据库).*(备份|backup)|(备份|backup).*(mysql|数据库)`, Intent: "create_scheduled_backup", TargetType: "mysql", RiskLevel: model.RiskLevelMedium, MatchedTemplate: "mysql_backup"},
		{Pattern: `(github).*(连通|连接)|(连通|连接).*(github)`, Intent: "health_check", TargetType: "http_service", Parameters: map[string]string{"URL": "https://github.com", "Timeout": "10"}, MatchedTemplate: "http_health_check"},
		{Pattern: `健康检查|health.?check|(检查|检测).*(http|网站|服务)`, Intent: "health_check", TargetType: "http_service", MatchedTemplate: "http_health_check"},
		{Pattern: `nginx.*(日志|错误|运行)|检查.*nginx`, Intent: "diagnose", TargetType: "nginx"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func (c staticLLMClient) Chat(_ context.Context, _ []llm.Message) (string, error) {
	return c.response, c.err
}
