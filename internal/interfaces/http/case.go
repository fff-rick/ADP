package api

import (
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"adp/internal/domain/model"

	"gopkg.in/yaml.v3"
)

const maxIncidentCaseMarkdownBytes = 1 << 20

func (s *Server) handleListIncidentCases(w http.ResponseWriter, r *http.Request) {
	filter := model.IncidentCaseFilter{
		Query:       strings.TrimSpace(r.URL.Query().Get("q")),
		TriggerType: strings.TrimSpace(r.URL.Query().Get("trigger_type")),
		FaultType:   strings.TrimSpace(r.URL.Query().Get("fault_type")),
		Limit:       parsePositiveInt(r.URL.Query().Get("limit")),
		Status:      model.IncidentCaseStatusApproved,
	}

	if s.repo != nil {
		cases, _ := s.repo.ListIncidentCases(filter)
		writeJSON(w, http.StatusOK, cases)
		return
	}
	writeJSON(w, http.StatusOK, []model.IncidentCase{})
}

func (s *Server) handleListPendingIncidentCases(w http.ResponseWriter, r *http.Request) {
	limit := parsePositiveInt(r.URL.Query().Get("limit"))
	if limit == 0 || limit > 50 {
		limit = 20
	}
	cases, err := s.repo.ListIncidentCases(model.IncidentCaseFilter{Status: model.IncidentCaseStatusPendingReview, Limit: limit})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, cases)
}

// handleImportIncidentCaseMarkdown imports one Markdown document as an
// unreviewed historical case. Uploads deliberately do not become searchable
// until an administrator reviews them, since imported prose is untrusted input
// to the Agent just like model-generated case candidates.
func (s *Server) handleImportIncidentCaseMarkdown(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxIncidentCaseMarkdownBytes+1024)
	if err := r.ParseMultipartForm(maxIncidentCaseMarkdownBytes); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("parse Markdown upload: %w", err))
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("Markdown file is required: %w", err))
		return
	}
	defer file.Close() //nolint:errcheck
	if strings.ToLower(filepath.Ext(header.Filename)) != ".md" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("file must use the .md extension"))
		return
	}
	content, err := io.ReadAll(io.LimitReader(file, maxIncidentCaseMarkdownBytes+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("read Markdown upload: %w", err))
		return
	}
	if len(content) > maxIncidentCaseMarkdownBytes {
		writeError(w, http.StatusRequestEntityTooLarge, fmt.Errorf("Markdown file exceeds %d bytes", maxIncidentCaseMarkdownBytes))
		return
	}
	incidentCase, err := parseMarkdownIncidentCase(string(content), header.Filename)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(content))
	sourceID := "markdown-import:" + hash
	incidentCase, err = s.repo.UpsertIncidentCase(sourceID, incidentCase)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("import incident case: %w", err))
		return
	}
	user := currentUser(r)
	s.recordAudit("user", user.Username, "incident_case.markdown_imported", "incident_case", incidentCase.ID, map[string]any{
		"filename": header.Filename, "bytes": len(content), "status": incidentCase.Status,
	})
	writeJSON(w, http.StatusCreated, incidentCase)
}

type incidentCaseMarkdownFrontMatter struct {
	Title           string   `yaml:"title"`
	TriggerType     string   `yaml:"trigger_type"`
	FaultType       string   `yaml:"fault_type"`
	EnvironmentTags []string `yaml:"environment_tags"`
}

// parseMarkdownIncidentCase accepts a regular Markdown document. Optional YAML
// front matter can carry title, trigger_type, fault_type and environment_tags.
// The document body remains searchable as the summary/evidence, while common
// Chinese and English headings are extracted into their structured fields.
func parseMarkdownIncidentCase(source, filename string) (model.IncidentCase, error) {
	source = strings.TrimSpace(strings.TrimPrefix(source, "\ufeff"))
	if source == "" {
		return model.IncidentCase{}, fmt.Errorf("Markdown file is empty")
	}
	front, body, err := splitMarkdownFrontMatter(source)
	if err != nil {
		return model.IncidentCase{}, err
	}
	sections, title := markdownSections(body)
	if front.Title != "" {
		title = front.Title
	}
	if strings.TrimSpace(title) == "" {
		title = strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename))
	}
	body = strings.TrimSpace(body)
	caseValue := model.IncidentCase{
		Title:           title,
		TriggerType:     front.TriggerType,
		FaultType:       front.FaultType,
		EnvironmentTags: front.EnvironmentTags,
		Summary:         body,
		EvidenceSummary: body,
		Status:          model.IncidentCaseStatusPendingReview,
	}
	if value := markdownSection(sections, "症状", "告警症状", "alert symptoms", "symptoms"); value != "" {
		caseValue.AlertSymptoms = value
	}
	if value := markdownSection(sections, "证据", "证据摘要", "evidence", "evidence summary"); value != "" {
		caseValue.EvidenceSummary = value
	}
	if value := markdownSection(sections, "根因", "原因", "root cause", "cause"); value != "" {
		caseValue.RootCause = value
	}
	if value := markdownSection(sections, "处置结果", "结果", "resolution result", "result"); value != "" {
		caseValue.ResolutionResult = value
	}
	if value := markdownSection(sections, "处置步骤", "解决步骤", "resolution steps", "steps"); value != "" {
		caseValue.ResolutionSteps = markdownList(value)
	}
	return sanitizeIncidentCaseUpdates(caseValue), nil
}

func splitMarkdownFrontMatter(source string) (incidentCaseMarkdownFrontMatter, string, error) {
	if !strings.HasPrefix(source, "---\n") {
		return incidentCaseMarkdownFrontMatter{}, source, nil
	}
	end := strings.Index(source[4:], "\n---\n")
	if end < 0 {
		return incidentCaseMarkdownFrontMatter{}, "", fmt.Errorf("Markdown front matter is not closed")
	}
	end += 4
	var front incidentCaseMarkdownFrontMatter
	if err := yaml.Unmarshal([]byte(source[4:end]), &front); err != nil {
		return incidentCaseMarkdownFrontMatter{}, "", fmt.Errorf("parse Markdown front matter: %w", err)
	}
	return front, source[end+5:], nil
}

func markdownSections(body string) (map[string]string, string) {
	sections := make(map[string][]string)
	current := ""
	title := ""
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "# ") && title == "" {
			title = strings.TrimSpace(strings.TrimPrefix(trimmed, "# "))
			continue
		}
		if strings.HasPrefix(trimmed, "## ") || strings.HasPrefix(trimmed, "### ") {
			current = strings.ToLower(strings.TrimSpace(strings.TrimLeft(trimmed, "#")))
			continue
		}
		if current != "" {
			sections[current] = append(sections[current], line)
		}
	}
	out := make(map[string]string, len(sections))
	for heading, lines := range sections {
		out[heading] = strings.TrimSpace(strings.Join(lines, "\n"))
	}
	return out, title
}

func markdownSection(sections map[string]string, names ...string) string {
	for _, name := range names {
		if value := sections[strings.ToLower(name)]; value != "" {
			return value
		}
	}
	return ""
}

func markdownList(value string) []string {
	var items []string
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "-*0123456789. "))
		if line != "" {
			items = append(items, line)
		}
	}
	if len(items) == 0 && strings.TrimSpace(value) != "" {
		return []string{strings.TrimSpace(value)}
	}
	return items
}

func (s *Server) handleListFailedIncidentCaseEmbeddings(w http.ResponseWriter, r *http.Request) {
	limit := parsePositiveInt(r.URL.Query().Get("limit"))
	statuses, err := s.repo.ListFailedIncidentCaseEmbeddings(limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, statuses)
}

type incidentCaseReviewRequest struct {
	Action  string             `json:"action"`
	Note    string             `json:"note,omitempty"`
	Updates model.IncidentCase `json:"updates,omitempty"`
}

func (s *Server) handleIncidentCaseActions(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/cases/")
	if strings.HasSuffix(id, "/embedding/retry") && r.Method == http.MethodPost {
		caseID := strings.TrimSuffix(id, "/embedding/retry")
		if err := s.repo.RetryIncidentCaseEmbedding(caseID); err != nil {
			writeError(w, http.StatusNotFound, err)
			return
		}
		user := currentUser(r)
		s.recordAudit("user", user.Username, "incident_case.embedding_retry", "incident_case", caseID, nil)
		writeJSON(w, http.StatusOK, map[string]string{"status": "queued"})
		return
	}
	if !strings.HasSuffix(id, "/review") || r.Method != http.MethodPost {
		writeError(w, http.StatusNotFound, strconv.ErrSyntax)
		return
	}
	id = strings.TrimSuffix(id, "/review")
	if id == "" {
		writeError(w, http.StatusNotFound, strconv.ErrSyntax)
		return
	}
	var req incidentCaseReviewRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	var status model.IncidentCaseStatus
	switch strings.TrimSpace(req.Action) {
	case "approve":
		status = model.IncidentCaseStatusApproved
	case "reject":
		status = model.IncidentCaseStatusRejected
	default:
		writeError(w, http.StatusBadRequest, strconv.ErrSyntax)
		return
	}
	user := currentUser(r)
	incidentCase, err := s.repo.ReviewIncidentCase(id, status, user.Username, model.SanitizeText(req.Note), sanitizeIncidentCaseUpdates(req.Updates))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	if err := s.queueApprovedCaseEmbedding(incidentCase); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	s.recordAudit("user", user.Username, "incident_case.reviewed", "incident_case", id, map[string]any{"status": status})
	writeJSON(w, http.StatusOK, incidentCase)
}

func sanitizeIncidentCaseUpdates(c model.IncidentCase) model.IncidentCase {
	c.Title = model.SanitizeText(c.Title)
	c.TriggerType = model.SanitizeText(c.TriggerType)
	c.FaultType = model.SanitizeText(c.FaultType)
	c.Summary = model.SanitizeText(c.Summary)
	c.AlertSymptoms = model.SanitizeText(c.AlertSymptoms)
	c.EvidenceSummary = model.SanitizeText(c.EvidenceSummary)
	c.RootCause = model.SanitizeText(c.RootCause)
	c.ResolutionResult = model.SanitizeText(c.ResolutionResult)
	c.PossibleCauses = sanitizeCaseStrings(c.PossibleCauses)
	c.Suggestions = sanitizeCaseStrings(c.Suggestions)
	c.EnvironmentTags = sanitizeCaseStrings(c.EnvironmentTags)
	c.ResolutionSteps = sanitizeCaseStrings(c.ResolutionSteps)
	return c
}
func sanitizeCaseStrings(values []string) []string {
	for i := range values {
		values[i] = model.SanitizeText(values[i])
	}
	return values
}

func (s *Server) handleSuggestIncidentCases(w http.ResponseWriter, r *http.Request) {
	description := strings.TrimSpace(r.URL.Query().Get("description"))
	triggerType := strings.TrimSpace(r.URL.Query().Get("trigger_type"))
	faultType := strings.TrimSpace(r.URL.Query().Get("fault_type"))
	limit := parsePositiveInt(r.URL.Query().Get("limit"))
	if limit == 0 {
		limit = 3
	}

	if s.repo != nil {
		cases, _ := s.repo.FindSimilarIncidentCases(description, triggerType, faultType, limit)
		writeJSON(w, http.StatusOK, map[string]any{
			"reference_cases":  cases,
			"historical_hints": buildHistoricalHints(cases),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"reference_cases":  []model.IncidentCase{},
		"historical_hints": []string{},
	})
}

func buildHistoricalHints(cases []model.IncidentCase) []string {
	hints := make([]string, 0, len(cases))
	for _, incidentCase := range cases {
		if len(incidentCase.Suggestions) == 0 {
			continue
		}
		hints = append(hints, incidentCase.Title+": "+incidentCase.Suggestions[0])
	}
	return hints
}

func parsePositiveInt(value string) int {
	if value == "" {
		return 0
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return 0
	}
	return parsed
}
