package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"adp/internal/domain/model"
)

// handleGenerateYAML uses AI to convert NL input into YAML job definition.
func (s *Server) handleGenerateYAML(w http.ResponseWriter, r *http.Request) {
	var req parseTaskRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.Input == "" {
		writeError(w, http.StatusBadRequest, errors.New("input is required"))
		return
	}

	yamlResult, parsed, usedAI, aiErr := s.generateYAMLFromInput(r, req.Input)

	resp := map[string]any{
		"yaml":        yamlResult,
		"used_ai":     usedAI,
		"description": req.Input,
	}
	if aiErr != nil {
		resp["ai_error"] = aiErr.Error()
	}
	if parsed != nil {
		resp["parsed_name"] = parsed.Name
		resp["task_count"] = len(parsed.Tasks)
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) generateYAMLFromInput(_ *http.Request, input string) (string, *YAMLJobSpec, bool, error) {
	var aiErr error
	// Try LLM if configured.
	if s.config.LLMBaseURL != "" {
		prompt := s.injectAIContextIntoPrompt(s.promptOrDefault("yaml", ""))
		if strings.TrimSpace(prompt) == "" {
			aiErr = errors.New("YAML generator prompt is not configured")
		} else {
			yamlStr, err := callLLMForYAML(s.config.LLMBaseURL, s.config.LLMAPIKey, s.config.LLMModel, prompt, input)
			if err != nil {
				aiErr = err
			} else {
				yamlStr = stripMarkdownFence(yamlStr)
				spec := &YAMLJobSpec{}
				if err := yaml.Unmarshal([]byte(yamlStr), spec); err != nil {
					aiErr = fmt.Errorf("LLM returned invalid YAML: %w", err)
				} else if len(spec.Tasks) == 0 {
					aiErr = errors.New("LLM returned YAML without tasks")
				} else if err := s.validateAndFixYAML(spec); err != nil {
					aiErr = fmt.Errorf("LLM YAML validation failed: %w", err)
				} else {
					return yamlStr, spec, true, nil
				}
			}
		}
	}

	yamlStr, spec, ruleErr := s.ruleBasedYAML(input)
	if ruleErr != nil {
		if aiErr != nil {
			return "", nil, false, fmt.Errorf("%v; YAML rule fallback failed: %w", aiErr, ruleErr)
		}
		return "", nil, false, ruleErr
	}
	s.validateAndFixYAML(spec) //nolint:errcheck
	return yamlStr, spec, false, aiErr
}

func callLLMForYAML(baseURL, apiKey, model, systemPrompt, input string) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"model": model,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": input},
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create LLM request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("call LLM: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		var apiErr struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(body, &apiErr); err == nil && apiErr.Error.Message != "" {
			return "", fmt.Errorf("LLM API status %d: %s", resp.StatusCode, apiErr.Error.Message)
		}
		return "", fmt.Errorf("LLM API status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode LLM response: %w", err)
	}
	if len(result.Choices) == 0 {
		return "", errors.New("LLM returned no choices")
	}
	content := strings.TrimSpace(result.Choices[0].Message.Content)
	if content == "" {
		return "", errors.New("LLM returned empty content")
	}
	return content, nil
}

func stripMarkdownFence(value string) string {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "```") {
		return value
	}
	lines := strings.Split(value, "\n")
	if len(lines) >= 2 {
		lines = lines[1:]
	}
	if len(lines) > 0 && strings.HasPrefix(strings.TrimSpace(lines[len(lines)-1]), "```") {
		lines = lines[:len(lines)-1]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// YAMLGenerationRule is the API-managed fallback mapping for YAML generation.
type YAMLGenerationRule struct {
	Keywords   []string   `yaml:"keywords"`
	Name       string     `yaml:"name"`
	Tasks      []YAMLTask `yaml:"tasks"`
	WorkerType string     `yaml:"worker_type"`
	Workers    []string   `yaml:"workers"`
}

func (s *Server) SetYAMLRules(rules []YAMLGenerationRule) error {
	for i, rule := range rules {
		if len(rule.Keywords) == 0 || strings.TrimSpace(rule.Name) == "" || len(rule.Tasks) == 0 {
			return fmt.Errorf("YAML rule %d requires keywords, name and tasks", i+1)
		}
	}
	s.yamlRules = rules
	return nil
}

func (s *Server) ruleBasedYAML(input string) (string, *YAMLJobSpec, error) {
	lower := strings.ToLower(input)
	for _, rule := range s.yamlRules {
		for _, keyword := range rule.Keywords {
			if strings.Contains(lower, strings.ToLower(keyword)) {
				spec := &YAMLJobSpec{Name: rule.Name, Tasks: rule.Tasks, WorkerType: rule.WorkerType, Workers: rule.Workers}
				if spec.WorkerType == "" {
					spec.WorkerType = "shell"
				}
				if len(spec.Workers) == 0 {
					spec.Workers = []string{"all"}
				}
				data, err := yaml.Marshal(spec)
				return string(data), spec, err
			}
		}
	}
	return "", nil, errors.New("no managed YAML generation rule matches the input")
}

// handleSaveYAML saves a YAML definition to the database.
func (s *Server) handleSaveYAML(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		YAMLContent string `json:"yaml_content"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.YAMLContent == "" {
		writeError(w, http.StatusBadRequest, errors.New("yaml_content is required"))
		return
	}
	if req.Name == "" {
		req.Name = "untitled"
	}

	jy, err := s.repo.SaveJobYAML(model.JobYAML{
		Name:        req.Name,
		Description: req.Description,
		YAMLContent: req.YAMLContent,
		Source:      "manual",
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, jy)
}

// handleListYAMLs returns all stored YAML definitions.
func (s *Server) handleListYAMLs(w http.ResponseWriter, _ *http.Request) {
	yamls, _ := s.repo.ListJobYAMLs()
	writeJSON(w, http.StatusOK, yamls)
}

// handleRunYAML creates jobs from a stored YAML definition.
func (s *Server) handleRunYAML(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/yamls/")
	id = strings.TrimSuffix(id, "/run")
	if id == "" {
		writeError(w, http.StatusBadRequest, errors.New("yaml id is required"))
		return
	}

	jy, err := s.repo.GetJobYAML(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}

	// Use the existing YAML job creation logic.
	var spec YAMLJobSpec
	if err := yaml.Unmarshal([]byte(jy.YAMLContent), &spec); err != nil {
		writeError(w, http.StatusBadRequest, errors.New("invalid stored yaml: "+err.Error()))
		return
	}
	if err := s.validateAndFixYAML(&spec); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.createJobsFromSpec(w, r, spec)
}

// createJobsFromSpec creates jobs from a YAMLJobSpec and writes the response.
func (s *Server) createJobsFromSpec(w http.ResponseWriter, r *http.Request, spec YAMLJobSpec) {
	if s.repo == nil {
		writeError(w, http.StatusInternalServerError, errors.New("no store configured"))
		return
	}

	workerIDs := spec.Workers
	if len(workerIDs) == 1 && strings.ToLower(workerIDs[0]) == "all" {
		allWorkers, _ := s.repo.ListWorkers()
		workerIDs = nil
		for _, w := range allWorkers {
			if w.WorkerType == spec.WorkerType && w.Status == model.WorkerStatusOnline {
				workerIDs = append(workerIDs, w.ID)
			}
		}
	}

	var results []model.Job
	targets := workerIDs
	if len(targets) == 0 {
		targets = []string{""}
	}
	for _, task := range spec.Tasks {
		if err := model.ValidateNoInlineSecrets(task.Parameters); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		tmpl, cmd, err := s.templateEng.Render(task.Template, task.Parameters)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if err := s.policyEng.ValidateTemplate(task.Template); err != nil {
			writeError(w, http.StatusForbidden, err)
			return
		}
		if err := s.policyEng.ValidateCommand(cmd); err != nil {
			writeError(w, http.StatusForbidden, err)
			return
		}
		for _, wid := range targets {
			jobStatus := model.JobStatusPending
			name := fmt.Sprintf("[yaml:%s] %s", spec.Name, task.Name)
			if wid != "" {
				name = fmt.Sprintf("[yaml:%s][w:%s] %s", spec.Name, wid, task.Name)
			}
			job := model.Job{
				Name: name, WorkerType: spec.WorkerType, Command: cmd,
				Status: jobStatus, RiskLevel: tmpl.RiskLevel,
				ApprovalRequired: false, ApprovalStatus: model.ApprovalStatusNotRequired,
				TemplateCode: task.Template, Parameters: cloneStringMap(task.Parameters), SourceType: "yaml_job",
			}
			j, err := s.repo.CreateJob(job)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err)
				return
			}
			if wid != "" {
				if dispatched, err := s.dispatchJobToWorker(j.ID, wid); err == nil {
					j = dispatched
					s.workerHub.PushJob(wid, j)
				}
			}
			results = append(results, j)
		}
	}
	writeJSON(w, http.StatusCreated, map[string]any{"jobs": results, "total": len(results)})
}

// handleYAMLActions routes YAML collection endpoints.
func (s *Server) handleYAMLActions(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/yamls/")
	if strings.HasSuffix(path, "/run") {
		s.handleRunYAML(w, r)
		return
	}
	if r.Method == http.MethodDelete {
		s.handleDeleteYAML(w, r)
		return
	}
	writeError(w, http.StatusNotFound, errors.New("unsupported yaml route"))
}

// validModules is the whitelist of allowed template codes.
// validateAndFixYAML validates a parsed YAML spec and fills in missing parameters.
func (s *Server) validateAndFixYAML(spec *YAMLJobSpec) error {
	if len(spec.Tasks) == 0 {
		return errors.New("tasks list is empty")
	}
	for i, task := range spec.Tasks {
		if task.Parameters == nil {
			task.Parameters = make(map[string]string)
			spec.Tasks[i].Parameters = task.Parameters
		}
		if _, ok := s.templateEng.GetTemplate(task.Template); !ok {
			return fmt.Errorf("task %d: unknown template '%s'", i+1, task.Template)
		}
		// Fill defaults from AI context.
		if s.aiContext != nil {
			s.aiContext.FillDefaults(task.Parameters, task.Template)
		}
		// Validate required params by checking the module's parameter definitions.
		if mod, err := s.moduleReg.Get(task.Template); err == nil {
			for _, p := range mod.Parameters() {
				if p.Required && task.Parameters[p.Name] == "" {
					// Auto-fill from context or use default.
					if p.Default != "" {
						task.Parameters[p.Name] = p.Default
					}
				}
			}
		}
		if err := model.ValidateServiceProfile(task.Template, task.Parameters); err != nil {
			return fmt.Errorf("task %d: %w", i+1, err)
		}
	}
	if spec.WorkerType == "" {
		spec.WorkerType = "shell"
	}
	return nil
}

// injectAIContextIntoPrompt prepends AI context configuration to the base prompt.
func (s *Server) injectAIContextIntoPrompt(basePrompt string) string {
	if s.aiContext == nil {
		return basePrompt
	}
	return s.aiContext.ToPromptSection() + "\n" + basePrompt
}

// handleDeleteYAML deletes a stored YAML.
func (s *Server) handleDeleteYAML(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/yamls/")
	if id == "" {
		writeError(w, http.StatusBadRequest, errors.New("yaml id is required"))
		return
	}
	if err := s.repo.DeleteJobYAML(id); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
