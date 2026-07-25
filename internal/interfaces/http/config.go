package api

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"gopkg.in/yaml.v3"

	"adp/internal/application/analyzer"
	"adp/internal/application/parser"
	"adp/internal/application/planner"
	"adp/internal/domain/model"
)

const (
	configKindTemplate      = "templates"
	configKindPolicy        = "policies"
	configKindPrompt        = "prompts"
	configKindDiagnosisPlan = "diagnosis_plans"
	configKindParserRules   = "parser_rules"
	configKindYAMLRules     = "yaml_rules"
	configKindAnalysisRules = "analysis_rules"
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

type promptConfigYAML struct {
	Code    string `yaml:"code"`
	Name    string `yaml:"name"`
	Content string `yaml:"content"`
}

type diagnosisPlanConfigYAML struct {
	TriggerType string              `yaml:"trigger_type"`
	Title       string              `yaml:"title"`
	Keywords    []string            `yaml:"keywords"`
	Steps       []diagnosisStepYAML `yaml:"steps"`
}

type parserRulesConfigYAML struct {
	ID    string              `yaml:"id"`
	Name  string              `yaml:"name"`
	Rules []parser.IntentRule `yaml:"rules"`
}

type yamlRulesConfigYAML struct {
	ID    string               `yaml:"id"`
	Name  string               `yaml:"name"`
	Rules []YAMLGenerationRule `yaml:"rules"`
}

type analysisRulesConfigYAML struct {
	ID    string                  `yaml:"id"`
	Name  string                  `yaml:"name"`
	Rules []analyzer.AnalysisRule `yaml:"rules"`
}

type diagnosisStepYAML struct {
	StepNo       int               `yaml:"step_no"`
	Name         string            `yaml:"name"`
	Description  string            `yaml:"description"`
	TemplateCode string            `yaml:"template_code"`
	Parameters   map[string]string `yaml:"parameters"`
	TimeoutSec   int               `yaml:"timeout_seconds"`
}

func (s *Server) handleManagedConfigActions(w http.ResponseWriter, r *http.Request) {
	kind, id := managedConfigPath(r.URL.Path)
	if kind == "" {
		writeError(w, http.StatusBadRequest, errors.New("config kind is required"))
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
		configs, err := s.repo.ListManagedConfigs(kind)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, configs)
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
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		user := currentUser(r)
		s.recordAudit("user", user.Username, "managed_config.deleted", "managed_config", kind+"/"+id, nil)
		writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	default:
		writeError(w, http.StatusMethodNotAllowed, errors.New("unsupported method"))
	}
}

func (s *Server) handleSaveManagedConfig(w http.ResponseWriter, r *http.Request, kind, pathID string) {
	var req managedConfigRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	req.YAMLContent = strings.TrimSpace(req.YAMLContent)
	if req.YAMLContent == "" {
		writeError(w, http.StatusBadRequest, errors.New("yaml_content is required"))
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

	previous, _ := s.repo.GetManagedConfig(kind, id)
	cfg, err := s.repo.SaveManagedConfig(model.ManagedConfig{
		ID:          id,
		Kind:        kind,
		Name:        name,
		YAMLContent: req.YAMLContent,
		Active:      active,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := s.reloadManagedConfigs(); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	user := currentUser(r)
	s.recordAudit("user", user.Username, "managed_config.saved", "managed_config", kind+"/"+cfg.ID, map[string]any{
		"kind":            kind,
		"active":          active,
		"created":         previous.ID == "",
		"previous_sha256": yamlSHA256(previous.YAMLContent),
		"current_sha256":  yamlSHA256(cfg.YAMLContent),
	})
	writeJSON(w, http.StatusCreated, cfg)
}

func yamlSHA256(content string) string {
	if content == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(content))
	return fmt.Sprintf("%x", sum)
}

func managedConfigPath(path string) (string, string) {
	path = strings.TrimPrefix(path, "/api/v1/configs/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		return "", ""
	}
	if len(parts) == 1 {
		return parts[0], ""
	}
	return parts[0], parts[1]
}

func isSupportedConfigKind(kind string) bool {
	switch kind {
	case configKindTemplate, configKindPolicy, configKindPrompt, configKindDiagnosisPlan, configKindParserRules, configKindYAMLRules, configKindAnalysisRules:
		return true
	default:
		return false
	}
}

func managedConfigIdentity(kind, raw string) (string, string, error) {
	switch kind {
	case configKindTemplate:
		var cfg templateConfigYAML
		if err := yaml.Unmarshal([]byte(raw), &cfg); err != nil {
			return "", "", fmt.Errorf("invalid template yaml: %w", err)
		}
		if cfg.Code == "" {
			return "", "", errors.New("template code is required")
		}
		return cfg.Code, fallbackName(cfg.Name, cfg.Code), nil
	case configKindPolicy:
		var cfg policyConfigYAML
		if err := yaml.Unmarshal([]byte(raw), &cfg); err != nil {
			return "", "", fmt.Errorf("invalid policy yaml: %w", err)
		}
		if cfg.ID == "" {
			cfg.ID = "default"
		}
		return cfg.ID, fallbackName(cfg.Name, cfg.ID), nil
	case configKindPrompt:
		var cfg promptConfigYAML
		if err := yaml.Unmarshal([]byte(raw), &cfg); err != nil {
			return "", "", fmt.Errorf("invalid prompt yaml: %w", err)
		}
		if cfg.Code == "" {
			return "", "", errors.New("prompt code is required")
		}
		if strings.TrimSpace(cfg.Content) == "" {
			return "", "", errors.New("prompt content is required")
		}
		return cfg.Code, fallbackName(cfg.Name, cfg.Code), nil
	case configKindDiagnosisPlan:
		var cfg diagnosisPlanConfigYAML
		if err := yaml.Unmarshal([]byte(raw), &cfg); err != nil {
			return "", "", fmt.Errorf("invalid diagnosis plan yaml: %w", err)
		}
		if cfg.TriggerType == "" {
			return "", "", errors.New("trigger_type is required")
		}
		if len(cfg.Steps) == 0 {
			return "", "", errors.New("diagnosis plan steps are required")
		}
		return cfg.TriggerType, fallbackName(cfg.Title, cfg.TriggerType), nil
	case configKindParserRules:
		var cfg parserRulesConfigYAML
		if err := yaml.Unmarshal([]byte(raw), &cfg); err != nil {
			return "", "", fmt.Errorf("invalid parser rules yaml: %w", err)
		}
		if cfg.ID == "" {
			cfg.ID = "task_parser"
		}
		if len(cfg.Rules) == 0 {
			return "", "", errors.New("parser rules are required")
		}
		return cfg.ID, fallbackName(cfg.Name, cfg.ID), nil
	case configKindYAMLRules:
		var cfg yamlRulesConfigYAML
		if err := yaml.Unmarshal([]byte(raw), &cfg); err != nil {
			return "", "", fmt.Errorf("invalid YAML rules config: %w", err)
		}
		if cfg.ID == "" {
			cfg.ID = "yaml_generator"
		}
		if len(cfg.Rules) == 0 {
			return "", "", errors.New("YAML generation rules are required")
		}
		return cfg.ID, fallbackName(cfg.Name, cfg.ID), nil
	case configKindAnalysisRules:
		var cfg analysisRulesConfigYAML
		if err := yaml.Unmarshal([]byte(raw), &cfg); err != nil {
			return "", "", fmt.Errorf("invalid analysis rules config: %w", err)
		}
		if cfg.ID == "" {
			cfg.ID = "diagnosis_analyzer"
		}
		if len(cfg.Rules) == 0 {
			return "", "", errors.New("analysis rules are required")
		}
		return cfg.ID, fallbackName(cfg.Name, cfg.ID), nil
	default:
		return "", "", fmt.Errorf("unsupported config kind: %s", kind)
	}
}

func fallbackName(name, fallback string) string {
	name = strings.TrimSpace(name)
	if name != "" {
		return name
	}
	return fallback
}

func (s *Server) reloadManagedConfigs() error {
	if s.repo == nil {
		return nil
	}
	configs, err := s.repo.ListManagedConfigs("")
	if err != nil {
		return err
	}
	for _, cfg := range configs {
		if !cfg.Active {
			continue
		}
		if err := s.applyManagedConfig(cfg); err != nil {
			return fmt.Errorf("apply %s/%s: %w", cfg.Kind, cfg.ID, err)
		}
	}
	return nil
}

func (s *Server) applyManagedConfig(cfg model.ManagedConfig) error {
	switch cfg.Kind {
	case configKindTemplate:
		var raw templateConfigYAML
		if err := yaml.Unmarshal([]byte(cfg.YAMLContent), &raw); err != nil {
			return err
		}
		tmpl := model.CommandTemplate{
			Code:        raw.Code,
			Name:        raw.Name,
			Description: raw.Description,
			ToolType:    raw.ToolType,
			Command:     raw.Command,
			Parameters:  convertTemplateParams(raw.Parameters),
			RiskLevel:   raw.RiskLevel,
		}
		if tmpl.ToolType == "" {
			tmpl.ToolType = "shell"
		}
		if tmpl.RiskLevel == "" {
			tmpl.RiskLevel = model.RiskLevelLow
		}
		if tmpl.Code == "" || tmpl.Command == "" {
			return errors.New("template code and command are required")
		}
		s.templateEng.RegisterTemplate(tmpl)
	case configKindPolicy:
		var raw policyConfigYAML
		if err := yaml.Unmarshal([]byte(cfg.YAMLContent), &raw); err != nil {
			return err
		}
		levels := make([]model.RiskLevel, 0, len(raw.ApprovalRiskLevels))
		for _, level := range raw.ApprovalRiskLevels {
			levels = append(levels, model.RiskLevel(level))
		}
		s.policyEng.Configure(raw.AllowedTools, raw.AllowedTemplates, raw.HighRiskKeywords, levels)
	case configKindPrompt:
		var raw promptConfigYAML
		if err := yaml.Unmarshal([]byte(cfg.YAMLContent), &raw); err != nil {
			return err
		}
		s.applyPromptConfig(raw.Code, raw.Content)
	case configKindDiagnosisPlan:
		var raw diagnosisPlanConfigYAML
		if err := yaml.Unmarshal([]byte(cfg.YAMLContent), &raw); err != nil {
			return err
		}
		s.planner.RegisterPlanDefinition(raw.TriggerType, planner.PlanDefinition{
			Title:    raw.Title,
			Keywords: raw.Keywords,
			Steps:    convertDiagnosisSteps(raw.Steps),
		})
	case configKindParserRules:
		var raw parserRulesConfigYAML
		if err := yaml.Unmarshal([]byte(cfg.YAMLContent), &raw); err != nil {
			return err
		}
		if err := s.taskParser.SetRules(raw.Rules); err != nil {
			return err
		}
	case configKindYAMLRules:
		var raw yamlRulesConfigYAML
		if err := yaml.Unmarshal([]byte(cfg.YAMLContent), &raw); err != nil {
			return err
		}
		if err := s.SetYAMLRules(raw.Rules); err != nil {
			return err
		}
	case configKindAnalysisRules:
		var raw analysisRulesConfigYAML
		if err := yaml.Unmarshal([]byte(cfg.YAMLContent), &raw); err != nil {
			return err
		}
		if err := s.analyzer.SetRules(raw.Rules); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported config kind: %s", cfg.Kind)
	}
	return nil
}

func convertTemplateParams(params []templateParamYAML) []model.TemplateParam {
	result := make([]model.TemplateParam, 0, len(params))
	for _, p := range params {
		result = append(result, model.TemplateParam{
			Name:        p.Name,
			Description: p.Description,
			Required:    p.Required,
			Default:     p.Default,
		})
	}
	return result
}

func convertDiagnosisSteps(steps []diagnosisStepYAML) []model.DiagnosisStep {
	result := make([]model.DiagnosisStep, 0, len(steps))
	for i, step := range steps {
		stepNo := step.StepNo
		if stepNo == 0 {
			stepNo = i + 1
		}
		result = append(result, model.DiagnosisStep{
			StepNo:       stepNo,
			Name:         step.Name,
			Description:  step.Description,
			TemplateCode: step.TemplateCode,
			Parameters:   step.Parameters,
			TimeoutSec:   step.TimeoutSec,
			Status:       model.JobStatusPending,
		})
	}
	return result
}

func (s *Server) applyPromptConfig(code, content string) {
	code = strings.TrimSpace(code)
	content = strings.TrimSpace(content)
	switch code {
	case "task_parser", "parser":
		s.taskParser.SetSystemPrompt(content)
	case "diagnosis_analyzer", "analyzer":
		s.analyzer.SetSystemPrompt(content)
	case "diagnosis_planner", "planner":
		s.planner.SetSystemPrompt(content)
	case "yaml", "yaml_generator":
		s.systemPrompts["yaml"] = content
	default:
		s.systemPrompts[code] = content
	}
}

func (s *Server) promptOrDefault(code, fallback string) string {
	if prompt := strings.TrimSpace(s.systemPrompts[code]); prompt != "" {
		return prompt
	}
	return fallback
}
