package model

import (
	"strings"
	"testing"
)

func TestSanitizeMapRedactsAndBoundsValues(t *testing.T) {
	safe := SanitizeMap(map[string]any{"api_key": "abc", "output": "token: xyz\n" + strings.Repeat("x", MaxSafeTextLength+1), "nested": map[string]any{"password": "hidden"}})
	if safe["api_key"] != "[REDACTED]" || safe["nested"].(map[string]any)["password"] != "[REDACTED]" {
		t.Fatalf("secret leaked: %#v", safe)
	}
	if strings.Contains(safe["output"].(string), "xyz") || len(safe["output"].(string)) > MaxSafeTextLength+len("…[truncated]") {
		t.Fatalf("unsafe output: %#v", safe["output"])
	}
}

func TestSanitizeMapHandlesStringCollections(t *testing.T) {
	safe := SanitizeMap(map[string]any{
		"headers": map[string]string{"authorization": "Bearer secret", "note": "token: hidden"},
		"lines":   []string{"password=hidden", "safe"},
	})
	headers := safe["headers"].(map[string]string)
	lines := safe["lines"].([]string)
	if headers["authorization"] != "[REDACTED]" || strings.Contains(headers["note"], "hidden") || strings.Contains(lines[0], "hidden") {
		t.Fatalf("secret leaked from typed collection: %#v", safe)
	}
}
