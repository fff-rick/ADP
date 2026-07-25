package model

import (
	"fmt"
	"regexp"
	"strings"
)

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
