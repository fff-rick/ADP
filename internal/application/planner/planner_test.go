package planner

import (
	"context"
	"testing"

	"adp/internal/domain/model"
	"adp/internal/domain/template"
)

func TestGeneratePlan_NginxUnreachable(t *testing.T) {
	p := newConfiguredPlanner()

	tests := []string{
		"nginx 无法访问",
		"网站打不开了",
		"HTTP 服务不可用，帮忙看看",
		"nginx is unreachable",
	}

	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			plan, err := p.GeneratePlan(context.Background(), input)
			if err != nil {
				t.Fatalf("GeneratePlan() error = %v", err)
			}
			if plan.TriggerType != "nginx_unreachable" {
				t.Errorf("trigger_type = %s, want nginx_unreachable", plan.TriggerType)
			}
			if len(plan.Steps) != 4 {
				t.Errorf("len(steps) = %d, want 4", len(plan.Steps))
			}
			if plan.Status != model.PlanStatusPending {
				t.Errorf("status = %s, want pending", plan.Status)
			}

			expectedSteps := []string{"check_process", "check_port", "read_log_tail", "http_health_check"}
			for i, code := range expectedSteps {
				if plan.Steps[i].TemplateCode != code {
					t.Errorf("step %d template = %s, want %s", i+1, plan.Steps[i].TemplateCode, code)
				}
			}
		})
	}
}

func TestGeneratePlan_RedisSlow(t *testing.T) {
	p := newConfiguredPlanner()

	tests := []string{
		"Redis 响应速度过慢",
		"缓存服务变慢了",
		"redis 性能问题",
	}

	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			plan, err := p.GeneratePlan(context.Background(), input)
			if err != nil {
				t.Fatalf("GeneratePlan() error = %v", err)
			}
			if plan.TriggerType != "redis_slow" {
				t.Errorf("trigger_type = %s, want redis_slow", plan.TriggerType)
			}
			if len(plan.Steps) != 4 {
				t.Errorf("len(steps) = %d, want 4", len(plan.Steps))
			}

			expectedSteps := []string{"redis_ping", "redis_info", "redis_slowlog_get", "redis_client_list"}
			for i, code := range expectedSteps {
				if plan.Steps[i].TemplateCode != code {
					t.Errorf("step %d template = %s, want %s", i+1, plan.Steps[i].TemplateCode, code)
				}
			}
		})
	}
}

func TestGeneratePlan_UnrecognizedInput(t *testing.T) {
	p := New(nil, template.NewEngine(), NewPlanStore())

	_, err := p.GeneratePlan(context.Background(), "something completely random")
	if err == nil {
		t.Fatal("expected error for unrecognized input")
	}
}

func TestGeneratePlan_EmptyInput(t *testing.T) {
	p := New(nil, template.NewEngine(), NewPlanStore())

	_, err := p.GeneratePlan(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for empty input")
	}
}

func TestPlanStore(t *testing.T) {
	store := NewPlanStore()

	plan := model.DiagnosisPlan{
		Title:       "test",
		Description: "test plan",
		TriggerType: "test",
		Steps:       []model.DiagnosisStep{},
		Status:      model.PlanStatusPending,
	}

	saved := store.Save(plan)
	if saved.ID == "" {
		t.Fatal("expected plan to get an ID")
	}

	retrieved, ok := store.Get(saved.ID)
	if !ok {
		t.Fatal("expected plan to be retrievable")
	}
	if retrieved.Title != "test" {
		t.Errorf("title = %s, want test", retrieved.Title)
	}

	_, ok = store.Get("nonexistent")
	if ok {
		t.Fatal("expected false for nonexistent plan")
	}
}

func newConfiguredPlanner() *Planner {
	p := New(nil, template.NewEngine(), NewPlanStore())
	p.RegisterPlanDefinition("nginx_unreachable", PlanDefinition{Title: "Nginx", Keywords: []string{"nginx", "网站", "http", "unreachable"}, Steps: []model.DiagnosisStep{
		{TemplateCode: "check_process"}, {TemplateCode: "check_port"}, {TemplateCode: "read_log_tail"}, {TemplateCode: "http_health_check"},
	}})
	p.RegisterPlanDefinition("redis_slow", PlanDefinition{Title: "Redis", Keywords: []string{"redis", "缓存", "慢", "性能"}, Steps: []model.DiagnosisStep{
		{TemplateCode: "redis_ping"}, {TemplateCode: "redis_info"}, {TemplateCode: "redis_slowlog_get"}, {TemplateCode: "redis_client_list"},
	}})
	return p
}
