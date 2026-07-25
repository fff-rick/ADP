package analyzer

import (
	"context"
	"errors"
	"testing"

	"adp/internal/domain/model"
	"adp/internal/infrastructure/llm"
)

func TestAnalyze_NginxUnreachable_ProcessDown(t *testing.T) {
	a := newConfiguredAnalyzer(t, nil)

	plan := model.DiagnosisPlan{
		ID:          "plan-000001",
		Title:       "Nginx 不可访问诊断",
		TriggerType: "nginx_unreachable",
		Steps: []model.DiagnosisStep{
			{
				StepNo: 1, Name: "检查 Nginx 进程存活状态", TemplateCode: "check_process",
				Result: &model.StepResult{Stdout: "", Stderr: "", ExitCode: 1, Success: false},
			},
			{
				StepNo: 2, Name: "检查 80 端口监听状态", TemplateCode: "check_port",
				Result: &model.StepResult{Stdout: "", Stderr: "", ExitCode: 1, Success: false},
			},
			{
				StepNo: 3, Name: "读取 Nginx 错误日志", TemplateCode: "read_log_tail",
				Result: &model.StepResult{Stdout: "permission denied", Stderr: "", ExitCode: 0, Success: true},
			},
			{
				StepNo: 4, Name: "HTTP 健康检查", TemplateCode: "http_health_check",
				Result: &model.StepResult{Stdout: "", Stderr: "Connection refused", ExitCode: 7, Success: false},
			},
		},
	}

	report, err := a.Analyze(context.Background(), plan)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if report == nil {
		t.Fatal("expected non-nil report")
	}
	if report.FaultType == "" {
		t.Error("expected non-empty fault_type")
	}
	if len(report.PossibleCauses) == 0 {
		t.Error("expected at least one possible cause")
	}
	if len(report.Suggestions) == 0 {
		t.Error("expected at least one suggestion")
	}

	t.Logf("Fault type: %s", report.FaultType)
	t.Logf("Causes: %v", report.PossibleCauses)
	t.Logf("Suggestions: %v", report.Suggestions)
	t.Logf("Confidence: %.2f", report.Confidence)
}

func TestAnalyze_RedisSlow(t *testing.T) {
	a := newConfiguredAnalyzer(t, nil)

	plan := model.DiagnosisPlan{
		ID:          "plan-000002",
		Title:       "Redis 响应慢诊断",
		TriggerType: "redis_slow",
		Steps: []model.DiagnosisStep{
			{
				StepNo: 1, Name: "Redis 存活检查", TemplateCode: "redis_ping",
				Result: &model.StepResult{Stdout: "PONG", Stderr: "", ExitCode: 0, Success: true},
			},
			{
				StepNo: 2, Name: "Redis 内存使用分析", TemplateCode: "redis_info",
				Result: &model.StepResult{Stdout: "used_memory_human:1.5G\nused_memory_peak_human:2.0G", Stderr: "", ExitCode: 0, Success: true},
			},
			{
				StepNo: 3, Name: "Redis 慢日志查询", TemplateCode: "redis_slowlog_get",
				Result: &model.StepResult{Stdout: "1) 1) (integer) 1234\n   2) (integer) 1234567\n   3) (integer) 500000\n   4) 1) \"KEYS\"\n      2) \"*\"", Stderr: "", ExitCode: 0, Success: true},
			},
			{
				StepNo: 4, Name: "Redis 当前连接数检查", TemplateCode: "redis_client_list",
				Result: &model.StepResult{Stdout: stringsRepeat("id=xxx addr=xxx\n", 60), Stderr: "", ExitCode: 0, Success: true},
			},
		},
	}

	report, err := a.Analyze(context.Background(), plan)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}

	t.Logf("Fault type: %s", report.FaultType)
	t.Logf("Causes: %v", report.PossibleCauses)
	t.Logf("Suggestions: %v", report.Suggestions)
	t.Logf("Confidence: %.2f", report.Confidence)
}

func TestAnalyze_WithLLMUsesParsedReport(t *testing.T) {
	a := newConfiguredAnalyzer(t, staticLLMClient{
		response: `{
			"fault_type": "AI 判断的 Redis 内存压力",
			"possible_causes": ["Redis maxmemory 设置过低"],
			"suggestions": ["调整 maxmemory 并检查淘汰策略"],
			"confidence": 0.82
		}`,
	})

	report, err := a.Analyze(context.Background(), sampleRedisPlan())
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if report.FaultType != "AI 判断的 Redis 内存压力" {
		t.Fatalf("expected LLM report to be used, got fault_type=%q", report.FaultType)
	}
	if report.PlanID != "plan-llm" {
		t.Fatalf("expected plan id to be set, got %q", report.PlanID)
	}
	if report.RawAnalysis == "" {
		t.Fatal("expected raw LLM analysis to be preserved")
	}
}

func TestAnalyze_WithInvalidLLMFallsBackToRules(t *testing.T) {
	a := newConfiguredAnalyzer(t, staticLLMClient{response: "not json"})

	report, err := a.Analyze(context.Background(), sampleRedisPlan())
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if report.FaultType != "Redis 响应慢" {
		t.Fatalf("expected rule-based fallback report, got fault_type=%q", report.FaultType)
	}
}

func TestAnalyze_WithLLMErrorFallsBackToRules(t *testing.T) {
	a := newConfiguredAnalyzer(t, staticLLMClient{err: errors.New("llm unavailable")})

	report, err := a.Analyze(context.Background(), sampleRedisPlan())
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if report.FaultType != "Redis 响应慢" {
		t.Fatalf("expected rule-based fallback report, got fault_type=%q", report.FaultType)
	}
}

type staticLLMClient struct {
	response string
	err      error
}

func (c staticLLMClient) Chat(_ context.Context, _ []llm.Message) (string, error) {
	if c.err != nil {
		return "", c.err
	}
	return c.response, nil
}

func sampleRedisPlan() model.DiagnosisPlan {
	return model.DiagnosisPlan{
		ID:          "plan-llm",
		Title:       "Redis 响应慢诊断",
		TriggerType: "redis_slow",
		Steps: []model.DiagnosisStep{
			{
				StepNo: 1, Name: "Redis 存活检查", TemplateCode: "redis_ping",
				Result: &model.StepResult{Stdout: "PONG", ExitCode: 0, Success: true},
			},
			{
				StepNo: 2, Name: "Redis 内存使用分析", TemplateCode: "redis_info",
				Result: &model.StepResult{Stdout: "used_memory_human:1.5G", ExitCode: 0, Success: true},
			},
		},
	}
}

func stringsRepeat(s string, n int) string {
	result := ""
	for i := 0; i < n; i++ {
		result += s
	}
	return result
}

func newConfiguredAnalyzer(t *testing.T, client llm.Client) *Analyzer {
	t.Helper()
	a := New(client)
	if err := a.SetRules([]AnalysisRule{
		{TriggerType: "nginx_unreachable", FaultType: "Nginx 服务异常", PossibleCauses: []string{"服务或网络异常"}, Suggestions: []string{"检查服务状态"}, Confidence: 0.5},
		{TriggerType: "redis_slow", FaultType: "Redis 响应慢", PossibleCauses: []string{"性能异常"}, Suggestions: []string{"检查慢日志"}, Confidence: 0.5},
	}); err != nil {
		t.Fatal(err)
	}
	return a
}
