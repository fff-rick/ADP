package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

func TestManagedConfigRequiresAdmin(t *testing.T) {
	server := NewServer(Config{Addr: ":0", AdminUsername: "admin", AdminPassword: "admin123", AuthSecret: "secret", WorkerSharedToken: "worker-secret"}, nil, nil)
	app := httptest.NewServer(server.httpServer.Handler)
	defer app.Close()
	if _, err := server.authService.CreateUser("operator", "operator123", "operator"); err != nil {
		t.Fatal(err)
	}
	token, _, err := server.authService.Login("operator", "operator123")
	if err != nil {
		t.Fatal(err)
	}
	status := mustJSONRequest(t, app.Client(), http.MethodPost, app.URL+"/api/v1/configs/prompts", token, map[string]any{"yaml_content": "code: task_parser\ncontent: test\n"}, nil)
	if status != http.StatusForbidden {
		t.Fatalf("operator config save status = %d, want %d", status, http.StatusForbidden)
	}
}

func TestManagedConfigBootstrapAndEnforceSync(t *testing.T) {
	dir := t.TempDir()
	promptDir := filepath.Join(dir, "prompts")
	if err := os.Mkdir(promptDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(promptDir, "task_parser.yaml")
	if err := os.WriteFile(path, []byte("code: task_parser\nname: parser\ncontent: source-v1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := NewServer(Config{Addr: ":0", AdminUsername: "admin", AdminPassword: "admin123", AuthSecret: "secret", WorkerSharedToken: "worker-secret", ManagedConfigDir: dir}, nil, nil)
	cfg, err := server.repo.GetManagedConfig("prompts", "task_parser")
	if err != nil || cfg.YAMLContent == "" {
		t.Fatalf("bootstrap config = %+v, err=%v", cfg, err)
	}
	if err := os.WriteFile(path, []byte("code: task_parser\nname: parser\ncontent: source-v2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := server.syncManagedConfigs(dir, false)
	if err != nil || len(report.Drifted) != 1 {
		t.Fatalf("missing-mode report=%+v err=%v", report, err)
	}
	report, err = server.syncManagedConfigs(dir, true)
	if err != nil || report.Updated != 1 {
		t.Fatalf("enforce report=%+v err=%v", report, err)
	}
	cfg, _ = server.repo.GetManagedConfig("prompts", "task_parser")
	if cfg.YAMLContent == "" || !strings.Contains(cfg.YAMLContent, "source-v2") {
		t.Fatalf("enforced config = %+v", cfg)
	}
}
