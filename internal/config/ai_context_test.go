package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadAIContextUnifiedServices(t *testing.T) {
	path := writeTempAIContext(t, `
services:
  - name: redis-prod
    type: redis
    host: "10.0.0.2"
    port: "6380"
    logs:
      - name: server
        path: "/var/log/redis.log"
`)

	ctx, err := LoadAIContext(path)
	if err != nil {
		t.Fatalf("LoadAIContext() error = %v", err)
	}

	if len(ctx.Services) != 1 {
		t.Fatalf("services len = %d, want 1", len(ctx.Services))
	}
	if ctx.Services[0].Type != "redis" || ctx.Services[0].Host != "10.0.0.2" {
		t.Fatalf("unexpected service: %+v", ctx.Services[0])
	}

	params := map[string]string{"ServiceType": "redis"}
	ctx.FillDefaults(params, "redis_ping")
	if params["Host"] != "10.0.0.2" || params["Port"] != "6380" {
		t.Fatalf("redis defaults = %+v", params)
	}

	prompt := ctx.ToPromptSection()
	if !strings.Contains(prompt, "redis-prod (redis)") || !strings.Contains(prompt, "server:/var/log/redis.log") {
		t.Fatalf("prompt missing service details: %s", prompt)
	}
}

func TestLoadAIContextLegacyDefaults(t *testing.T) {
	path := writeTempAIContext(t, `
defaults:
  mysql:
    host: "127.0.0.1"
    port: "3306"
    user: "root"
  nginx:
    log_path: "/var/log/nginx/error.log"
    config_path: "/etc/nginx/nginx.conf"
`)

	ctx, err := LoadAIContext(path)
	if err != nil {
		t.Fatalf("LoadAIContext() error = %v", err)
	}

	if len(ctx.Services) != 2 {
		t.Fatalf("services len = %d, want 2", len(ctx.Services))
	}

	backupParams := map[string]string{"ServiceType": "mysql"}
	ctx.FillDefaults(backupParams, "mysql_backup")
	if backupParams["Host"] != "127.0.0.1" || backupParams["Port"] != "3306" || backupParams["User"] != "root" {
		t.Fatalf("mysql defaults = %+v", backupParams)
	}

	logParams := map[string]string{"ServiceType": "nginx"}
	ctx.FillDefaults(logParams, "read_log_tail")
	if logParams["LogFile"] != "/var/log/nginx/error.log" {
		t.Fatalf("log defaults = %+v", logParams)
	}
}

func writeTempAIContext(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ai_context.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write temp ai context: %v", err)
	}
	return path
}
