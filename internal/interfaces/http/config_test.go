package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"adp/internal/application/parser"
	"adp/internal/domain/model"
)

func installTestParserRules(t *testing.T, server *Server) {
	t.Helper()
	if err := server.taskParser.SetRules([]parser.IntentRule{{
		Pattern: `(mysql|数据库).*(备份|backup)|(备份|backup).*(mysql|数据库)`, Intent: "create_scheduled_backup", TargetType: "mysql", RiskLevel: model.RiskLevelMedium, MatchedTemplate: "mysql_backup", Parameters: map[string]string{"ServiceType": "mysql"},
	}}); err != nil {
		t.Fatal(err)
	}
	codes := []string{"mysql_backup", "http_health_check", "check_process", "check_port", "read_log_tail", "redis_ping", "redis_info", "redis_slowlog_get", "redis_client_list"}
	for _, code := range codes {
		server.templateEng.RegisterTemplate(model.CommandTemplate{Code: code, Name: code, ToolType: "shell", Command: "echo ok"})
	}
	server.policyEng.Configure([]string{"echo"}, codes, []string{"delete", "drop"}, []model.RiskLevel{model.RiskLevelMedium, model.RiskLevelHigh})
}

func TestManagedTemplateConfigReload(t *testing.T) {
	server := NewServer(Config{
		Addr:              ":0",
		AdminUsername:     "admin",
		AdminPassword:     "admin123",
		AuthSecret:        "secret",
		WorkerSharedToken: "worker-secret",
	}, nil, nil)
	app := httptest.NewServer(server.httpServer.Handler)
	defer app.Close()

	token, _, err := server.authService.Login("admin", "admin123")
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}

	status := mustJSONRequest(t, app.Client(), http.MethodPost, app.URL+"/api/v1/configs/templates", token, map[string]any{
		"yaml_content": `code: custom_echo
name: Custom Echo
description: Echo from managed config
tool_type: shell
command: echo {{.Message}}
risk_level: low
parameters:
  - name: Message
    required: true
`,
	}, nil)
	if status != http.StatusCreated {
		t.Fatalf("save config status = %d, want %d", status, http.StatusCreated)
	}

	var templates []model.CommandTemplate
	status = mustJSONRequest(t, app.Client(), http.MethodGet, app.URL+"/api/v1/templates", token, nil, &templates)
	if status != http.StatusOK {
		t.Fatalf("templates status = %d, want %d", status, http.StatusOK)
	}
	for _, tmpl := range templates {
		if tmpl.Code == "custom_echo" && tmpl.Command == "echo {{.Message}}" {
			return
		}
	}
	t.Fatalf("managed template was not loaded: %+v", templates)
}

func TestManagedParserRulesReload(t *testing.T) {
	server := NewServer(Config{Addr: ":0", AdminUsername: "admin", AdminPassword: "admin123", AuthSecret: "secret", WorkerSharedToken: "worker-secret"}, nil, nil)
	app := httptest.NewServer(server.httpServer.Handler)
	defer app.Close()
	token, _, err := server.authService.Login("admin", "admin123")
	if err != nil {
		t.Fatal(err)
	}
	status := mustJSONRequest(t, app.Client(), http.MethodPost, app.URL+"/api/v1/configs/parser_rules", token, map[string]any{"yaml_content": `id: task_parser
rules:
  - pattern: 'example-service'
    intent: health_check
    target_type: http_service
    risk_level: low
    matched_template: http_health_check
    parameters:
      URL: https://example.com
      Timeout: "10"
`}, nil)
	if status != http.StatusCreated {
		t.Fatalf("save config status = %d", status)
	}
	intent, err := server.taskParser.Parse(t.Context(), "check example-service")
	if err != nil {
		t.Fatal(err)
	}
	if intent.Parameters["URL"] != "https://example.com" || intent.MatchedTemplate != "http_health_check" {
		t.Fatalf("managed rule not applied: %+v", intent)
	}
}

func TestManagedYAMLRulesReload(t *testing.T) {
	server := NewServer(Config{Addr: ":0", AdminUsername: "admin", AdminPassword: "admin123", AuthSecret: "secret", WorkerSharedToken: "worker-secret"}, nil, nil)
	app := httptest.NewServer(server.httpServer.Handler)
	defer app.Close()
	token, _, err := server.authService.Login("admin", "admin123")
	if err != nil {
		t.Fatal(err)
	}
	status := mustJSONRequest(t, app.Client(), http.MethodPost, app.URL+"/api/v1/configs/yaml_rules", token, map[string]any{"yaml_content": `id: yaml_generator
rules:
  - keywords: [sample-service]
    name: Sample check
    tasks:
      - name: Check
        template: http_health_check
        parameters: {URL: https://example.com, Timeout: "10"}
`}, nil)
	if status != http.StatusCreated {
		t.Fatalf("save config status = %d", status)
	}
	_, spec, err := server.ruleBasedYAML("check sample-service")
	if err != nil || spec.Name != "Sample check" {
		t.Fatalf("managed YAML rule not applied: spec=%+v err=%v", spec, err)
	}
}
