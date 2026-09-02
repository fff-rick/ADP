package api

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"adp/internal/domain/model"
)

func TestAdminCanImportMarkdownIncidentCaseForReview(t *testing.T) {
	server := NewServer(Config{AdminUsername: "admin", AdminPassword: "admin123", AuthSecret: "test-secret"}, nil, nil)
	app := httptest.NewServer(server.Handler())
	defer app.Close()
	token, _, err := server.authService.Login("admin", "admin123")
	if err != nil {
		t.Fatal(err)
	}
	var payload bytes.Buffer
	writer := multipart.NewWriter(&payload)
	part, err := writer.CreateFormFile("file", "nginx-polkit.md")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte("---\ntrigger_type: nginx_restart\nfault_type: permission\nenvironment_tags: [prod, nginx]\n---\n# Nginx 重启权限不足\n\n## 症状\n重启失败。\n\n## 根因\nWorker 没有 polkit 权限。\n\n## 处置步骤\n- 配置 sudoers\n- 重启后验证\n"))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, app.URL+"/api/v1/cases/import", &payload)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := app.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("import status = %d", resp.StatusCode)
	}
	var imported model.IncidentCase
	if err := json.NewDecoder(resp.Body).Decode(&imported); err != nil {
		t.Fatal(err)
	}
	if imported.Status != model.IncidentCaseStatusPendingReview || imported.Title != "Nginx 重启权限不足" || imported.RootCause != "Worker 没有 polkit 权限。" || len(imported.ResolutionSteps) != 2 {
		t.Fatalf("unexpected imported case: %#v", imported)
	}
	if cases, err := server.repo.ListIncidentCases(model.IncidentCaseFilter{Status: model.IncidentCaseStatusApproved}); err != nil || len(cases) != 0 {
		t.Fatalf("imported case must not be searchable before review: %#v, %v", cases, err)
	}
}

func TestMarkdownCaseImportRequiresAdminAndMarkdownFile(t *testing.T) {
	server := NewServer(Config{AdminUsername: "admin", AdminPassword: "admin123", AuthSecret: "test-secret"}, nil, nil)
	app := httptest.NewServer(server.Handler())
	defer app.Close()
	request, err := http.NewRequest(http.MethodPost, app.URL+"/api/v1/cases/import", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := app.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close() //nolint:errcheck
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusUnauthorized)
	}
}
