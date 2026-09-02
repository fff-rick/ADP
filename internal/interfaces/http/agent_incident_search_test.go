package api

import (
	"context"
	"encoding/json"
	"testing"

	"adp/internal/domain/model"
)

func TestSearchIncidentCasesReturnsStructuredSanitizedHistoricalReferences(t *testing.T) {
	server := NewServer(Config{}, nil, nil)
	_, err := server.repo.UpsertIncidentCase("plan-rag-1", model.IncidentCase{
		Title: "Redis latency incident", TriggerType: "redis_latency", FaultType: "cpu_saturation",
		AlertSymptoms: "Redis P99 latency high", EnvironmentTags: []string{"prod", "redis"},
		EvidenceSummary: "password=super-secret; CPU reached 95%", RootCause: "hot key caused CPU saturation",
		ResolutionSteps: []string{"identify hot key", "apply rate limit"}, ResolutionResult: "latency returned to normal",
	})
	if err != nil {
		t.Fatal(err)
	}

	var search func(context.Context, json.RawMessage) (any, error)
	for _, tool := range server.agentTools() {
		if tool.Definition().Name == "search_incident_cases" {
			search = tool.Execute
			break
		}
	}
	if search == nil {
		t.Fatal("search_incident_cases tool not registered")
	}
	output, err := search(context.Background(), json.RawMessage(`{"query":"latency","environment_tags":["prod"],"limit":5}`))
	if err != nil {
		t.Fatal(err)
	}
	payload := output.(map[string]any)
	cases := payload["cases"].([]map[string]any)
	if len(cases) != 1 || cases[0]["source_id"] == "" {
		t.Fatalf("unexpected cases: %#v", cases)
	}
	if got := cases[0]["evidence_summary"].(string); got == "" || containsSecret(got) {
		t.Fatalf("evidence was not redacted: %q", got)
	}
	if historical, _ := payload["historical_only"].(bool); !historical {
		t.Fatalf("missing historical marker: %#v", payload)
	}
}

func TestSearchIncidentCasesExcludesPendingReview(t *testing.T) {
	server := NewServer(Config{}, nil, nil)
	candidate, err := server.repo.UpsertIncidentCase("run-review-1", model.IncidentCase{
		Title: "Unreviewed Redis latency incident", Summary: "latency", Status: model.IncidentCaseStatusPendingReview, SourceRunID: "run-review-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	var search func(context.Context, json.RawMessage) (any, error)
	for _, tool := range server.agentTools() {
		if tool.Definition().Name == "search_incident_cases" {
			search = tool.Execute
			break
		}
	}
	output, err := search(context.Background(), json.RawMessage(`{"query":"latency"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := len(output.(map[string]any)["cases"].([]map[string]any)); got != 0 {
		t.Fatalf("pending case leaked into retrieval: %d", got)
	}
	if _, err := server.repo.ReviewIncidentCase(candidate.ID, model.IncidentCaseStatusApproved, "admin", "verified", model.IncidentCase{}); err != nil {
		t.Fatal(err)
	}
	output, err = search(context.Background(), json.RawMessage(`{"query":"latency"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := len(output.(map[string]any)["cases"].([]map[string]any)); got != 1 {
		t.Fatalf("approved case missing from retrieval: %d", got)
	}
}

func TestSearchIncidentCasesFallsBackToMeaningfulTerms(t *testing.T) {
	server := NewServer(Config{}, nil, nil)
	_, err := server.repo.UpsertIncidentCase("run-polkit", model.IncidentCase{
		Title: "Nginx restart denied", EvidenceSummary: "Failed to restart nginx.service: Interactive authentication required.",
		RootCause: "Worker service account lacks polkit permission", Status: model.IncidentCaseStatusApproved,
	})
	if err != nil {
		t.Fatal(err)
	}
	var search func(context.Context, json.RawMessage) (any, error)
	for _, tool := range server.agentTools() {
		if tool.Definition().Name == "search_incident_cases" {
			search = tool.Execute
			break
		}
	}
	output, err := search(context.Background(), json.RawMessage(`{"query":"Interactive authentication required / systemctl 重启权限","limit":3}`))
	if err != nil {
		t.Fatal(err)
	}
	cases := output.(map[string]any)["cases"].([]map[string]any)
	if len(cases) != 1 || cases[0]["root_cause"] != "Worker service account lacks polkit permission" {
		t.Fatalf("term fallback failed: %#v", cases)
	}
}

func TestSearchIncidentCasesRelaxesModelSuppliedTypeHints(t *testing.T) {
	server := NewServer(Config{}, nil, nil)
	_, err := server.repo.UpsertIncidentCase("run-polkit-relaxed", model.IncidentCase{
		Title: "Nginx restart denied", EvidenceSummary: "Failed to restart nginx.service: Interactive authentication required.",
		RootCause: "Worker service account lacks polkit permission", Status: model.IncidentCaseStatusApproved,
	})
	if err != nil {
		t.Fatal(err)
	}
	output, err := server.searchIncidentCases(context.Background(), json.RawMessage(`{"query":"Interactive authentication required / nginx restart","trigger_type":"nginx_restart","fault_type":"service_restart"}`))
	if err != nil {
		t.Fatal(err)
	}
	payload := output.(map[string]any)
	if got := len(payload["cases"].([]map[string]any)); got != 1 {
		t.Fatalf("relaxed model hints should not suppress matching case: %#v", payload)
	}
	lexical := payload["retrieval"].(map[string]any)["lexical"].(map[string]any)
	if relaxed, _ := lexical["relaxed_model_hints"].(bool); !relaxed {
		t.Fatalf("missing retrieval provenance: %#v", payload)
	}
}

func containsSecret(value string) bool { return value == "password=super-secret; CPU reached 95%" }
