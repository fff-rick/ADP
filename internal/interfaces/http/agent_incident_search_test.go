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

func containsSecret(value string) bool { return value == "password=super-secret; CPU reached 95%" }
