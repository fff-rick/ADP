package model

import (
	"fmt"
	"regexp"
	"strings"
)

const MaxSafeTextLength = 4096

var secretValuePattern = regexp.MustCompile(`(?i)(?:password|passwd|secret|token|api[_-]?key|private[_-]?key|authorization)\s*(?:=|:|\s)\s*(?:bearer\s+)?[^\s,;]+`)

// SanitizeText protects Agent transcripts, audit records and SSE payloads from
// credentials while keeping enough context for operators to diagnose failures.
func SanitizeText(value string) string {
	value = secretValuePattern.ReplaceAllString(value, "[REDACTED]")
	if len(value) > MaxSafeTextLength {
		return value[:MaxSafeTextLength] + "…[truncated]"
	}
	return value
}

// SanitizeMap recursively produces a safe, size-bounded copy for persistence
// and presentation. Values with sensitive field names are never retained.
func SanitizeMap(value map[string]any) map[string]any {
	result := make(map[string]any, len(value))
	for key, item := range value {
		result[key] = sanitizeValue(key, item)
	}
	return result
}

func sanitizeValue(key string, value any) any {
	normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "_", ""), "-", ""))
	for _, word := range []string{"password", "passwd", "secret", "token", "apikey", "privatekey", "authorization", "dsn", "connectionstring", "connectionurl"} {
		if strings.Contains(normalized, word) {
			return "[REDACTED]"
		}
	}
	switch v := value.(type) {
	case string:
		return SanitizeText(v)
	case map[string]any:
		return SanitizeMap(v)
	case []map[string]any:
		out := make([]map[string]any, len(v))
		for i := range v {
			out[i] = SanitizeMap(v[i])
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i := range v {
			out[i] = sanitizeValue("", v[i])
		}
		return out
	default:
		return value
	}
}

var serviceProfilePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// ValidateServiceProfile ensures the control plane can reference only a
// named Worker-local profile, never a path or connection string.
func ValidateServiceProfile(templateCode string, params map[string]string) error {
	name := strings.TrimSpace(params["ServiceProfile"])
	if name == "" {
		return nil
	}
	if !serviceProfilePattern.MatchString(name) {
		return fmt.Errorf("template %q has invalid ServiceProfile", templateCode)
	}
	if strings.TrimSpace(params["ServiceType"]) == "" {
		return fmt.Errorf("template %q requires ServiceType when ServiceProfile is set", templateCode)
	}
	return nil
}

// ValidateNoInlineSecrets prevents credentials from being persisted in job
// parameters, audit records, or worker payloads. Workers must obtain such
// credentials from their locally mounted secret files instead.
func ValidateNoInlineSecrets(params map[string]string) error {
	for key, value := range params {
		if strings.TrimSpace(value) == "" {
			continue
		}
		normalized := strings.ToLower(strings.ReplaceAll(key, "_", ""))
		if normalized == "credentialsfile" || normalized == "credentialsprofile" {
			return fmt.Errorf("parameter %q is not allowed; use the Worker-local ServiceProfile", key)
		}
		if strings.HasSuffix(normalized, "ref") || strings.HasSuffix(normalized, "file") {
			continue
		}
		for _, sensitive := range []string{"password", "passwd", "secret", "token", "apikey", "privatekey", "dsn", "connectionstring", "connectionurl"} {
			if strings.Contains(normalized, sensitive) {
				return fmt.Errorf("inline sensitive parameter %q is not allowed; configure credentials on the worker and pass a *_ref or *_file reference", key)
			}
		}
	}
	return nil
}
