package api

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"gopkg.in/yaml.v3"

	"adp/internal/domain/model"
)

const (
	configKindTemplate = "templates"
	configKindPolicy   = "policies"
)

type managedConfigRequest struct {
	ID          string `json:"id,omitempty"`
	Name        string `json:"name,omitempty"`
	YAMLContent string `json:"yaml_content"`
	Active      *bool  `json:"active,omitempty"`
}
type templateConfigYAML struct {
	Code        string              `yaml:"code"`
	Name        string              `yaml:"name"`
	Description string              `yaml:"description"`
	ToolType    string              `yaml:"tool_type"`
	Command     string              `yaml:"command"`
	Parameters  []templateParamYAML `yaml:"parameters"`
	RiskLevel   model.RiskLevel     `yaml:"risk_level"`
}
type templateGroupConfigYAML struct {
	ID        string               `yaml:"id"`
	Name      string               `yaml:"name"`
	Templates []templateConfigYAML `yaml:"templates"`
}
type templateParamYAML struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Required    bool   `yaml:"required"`
	Default     string `yaml:"default"`
}
type policyConfigYAML struct {
	ID                 string   `yaml:"id"`
	Name               string   `yaml:"name"`
	AllowedTools       []string `yaml:"allowed_tools"`
	AllowedTemplates   []string `yaml:"allowed_templates"`
	HighRiskKeywords   []string `yaml:"high_risk_keywords"`
	ApprovalRiskLevels []string `yaml:"approval_risk_levels"`
}

func (s *Server) handleManagedConfigActions(w http.ResponseWriter, r *http.Request) {
	kind, id := managedConfigPath(r.URL.Path)
	if kind == "sync" {
		s.handleManagedConfigSync(w, r)
		return
	}
	if !isSupportedConfigKind(kind) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("unsupported config kind: %s", kind))
		return
	}
	switch r.Method {
	case http.MethodGet:
		if id != "" {
			cfg, err := s.repo.GetManagedConfig(kind, id)
			if err != nil {
				writeError(w, http.StatusNotFound, err)
				return
			}
			writeJSON(w, http.StatusOK, cfg)
			return
		}
		cfgs, err := s.repo.ListManagedConfigs(kind)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, cfgs)
	case http.MethodPost, http.MethodPut:
		s.handleSaveManagedConfig(w, r, kind, id)
	case http.MethodDelete:
		if id == "" {
			writeError(w, http.StatusBadRequest, errors.New("config id is required"))
			return
		}
		if err := s.repo.DeleteManagedConfig(kind, id); err != nil {
			writeError(w, http.StatusNotFound, err)
			return
		}
		if err := s.reloadManagedConfigs(); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	default:
		writeError(w, http.StatusMethodNotAllowed, errors.New("unsupported method"))
	}
}
func (s *Server) handleManagedConfigSync(w http.ResponseWriter, r *http.Request) {
	enforce := r.URL.Query().Get("enforce") == "true"
	report, err := s.syncManagedConfigs(s.config.ManagedConfigDir, enforce)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err = s.reloadManagedConfigs(); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, report)
}
func (s *Server) handleSaveManagedConfig(w http.ResponseWriter, r *http.Request, kind, pathID string) {
	var req managedConfigRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	id, name, err := managedConfigIdentity(kind, req.YAMLContent)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.ID != "" {
		id = req.ID
	}
	if pathID != "" {
		id = pathID
	}
	if req.Name != "" {
		name = req.Name
	}
	active := true
	if req.Active != nil {
		active = *req.Active
	}
	cfg, err := s.repo.SaveManagedConfig(model.ManagedConfig{ID: id, Kind: kind, Name: name, YAMLContent: req.YAMLContent, Active: active})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err = s.reloadManagedConfigs(); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, cfg)
}
func yamlSHA256(content string) string {
	sum := sha256.Sum256([]byte(content))
	return fmt.Sprintf("%x", sum)
}
func managedConfigPath(path string) (string, string) {
	parts := strings.Split(strings.Trim(strings.TrimPrefix(path, "/api/v1/configs/"), "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		return "", ""
	}
	if len(parts) == 1 {
		return parts[0], ""
	}
	return parts[0], parts[1]
}
func isSupportedConfigKind(kind string) bool {
	return kind == configKindTemplate || kind == configKindPolicy
}
func managedConfigIdentity(kind, raw string) (string, string, error) {
	switch kind {
	case configKindTemplate:
		var group templateGroupConfigYAML
		if err := yaml.Unmarshal([]byte(raw), &group); err != nil {
			return "", "", err
		}
		if group.ID == "" {
			return "", "", errors.New("template group id is required")
		}
		return group.ID, group.Name, nil
	case configKindPolicy:
		var p policyConfigYAML
		if err := yaml.Unmarshal([]byte(raw), &p); err != nil {
			return "", "", err
		}
		if p.ID == "" {
			p.ID = "default"
		}
		return p.ID, p.Name, nil
	}
	return "", "", errors.New("unsupported config kind")
}
func (s *Server) reloadManagedConfigs() error {
	cfgs, err := s.repo.ListManagedConfigs("")
	if err != nil {
		return err
	}
	for _, cfg := range cfgs {
		if cfg.Active {
			if err := s.applyManagedConfig(cfg); err != nil {
				return err
			}
		}
	}
	return nil
}
func (s *Server) applyManagedConfig(cfg model.ManagedConfig) error {
	switch cfg.Kind {
	case configKindPolicy:
		var p policyConfigYAML
		if err := yaml.Unmarshal([]byte(cfg.YAMLContent), &p); err != nil {
			return err
		}
		levels := make([]model.RiskLevel, len(p.ApprovalRiskLevels))
		for i, v := range p.ApprovalRiskLevels {
			levels[i] = model.RiskLevel(v)
		}
		s.policyEng.Configure(p.AllowedTools, p.AllowedTemplates, p.HighRiskKeywords, levels)
	}
	return nil
}
