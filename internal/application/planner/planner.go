package planner

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"adp/internal/domain/model"
	"adp/internal/domain/template"
	"adp/internal/infrastructure/llm"
)

type PlanDefinition struct {
	Title    string
	Keywords []string
	Steps    []model.DiagnosisStep
}

// PlanStore persists diagnosis plans in memory.
type PlanStore struct {
	mu     sync.RWMutex
	plans  map[string]model.DiagnosisPlan
	nextID int
}

func NewPlanStore() *PlanStore {
	return &PlanStore{
		plans:  make(map[string]model.DiagnosisPlan),
		nextID: 1,
	}
}

func (s *PlanStore) Save(plan model.DiagnosisPlan) model.DiagnosisPlan {
	s.mu.Lock()
	defer s.mu.Unlock()
	if plan.ID == "" {
		plan.ID = fmt.Sprintf("plan-%06d", s.nextID)
		s.nextID++
	}
	s.plans[plan.ID] = plan
	return plan
}

func (s *PlanStore) Get(id string) (model.DiagnosisPlan, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	plan, ok := s.plans[id]
	return plan, ok
}

func (s *PlanStore) Update(id string, fn func(plan *model.DiagnosisPlan)) (model.DiagnosisPlan, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	plan, ok := s.plans[id]
	if !ok {
		return model.DiagnosisPlan{}, false
	}
	fn(&plan)
	s.plans[id] = plan
	return plan, true
}

// Planner generates diagnosis plans from fault descriptions.
type Planner struct {
	llmClient    llm.Client
	templates    *template.Engine
	store        *PlanStore
	customPlans  map[string]PlanDefinition
	systemPrompt string
}

func New(llmClient llm.Client, templates *template.Engine, store *PlanStore) *Planner {
	return &Planner{
		llmClient:   llmClient,
		templates:   templates,
		store:       store,
		customPlans: make(map[string]PlanDefinition),
	}
}

// SetSystemPrompt replaces the API-managed LLM planner prompt.
func (p *Planner) SetSystemPrompt(prompt string) { p.systemPrompt = strings.TrimSpace(prompt) }

// Store returns the plan store.
func (p *Planner) Store() *PlanStore {
	return p.store
}

// GeneratePlan creates a diagnosis plan from a natural language fault description.
func (p *Planner) GeneratePlan(ctx context.Context, description string) (*model.DiagnosisPlan, error) {
	description = strings.TrimSpace(description)
	if description == "" {
		return nil, fmt.Errorf("description is empty")
	}

	triggerType, predefined, ok := p.matchCustomPlan(description)
	if ok {
		return p.buildFromPredefined(description, triggerType, predefined), nil
	}

	if p.llmClient != nil {
		return p.buildFromLLM(ctx, description)
	}

	return nil, fmt.Errorf("no managed diagnosis plan matches the description (and LLM is not configured)")
}

func (p *Planner) matchCustomPlan(description string) (string, PlanDefinition, bool) {
	lower := strings.ToLower(description)
	for triggerType, definition := range p.customPlans {
		for _, keyword := range definition.Keywords {
			if keyword != "" && strings.Contains(lower, strings.ToLower(keyword)) {
				return triggerType, definition, true
			}
		}
	}
	return "", PlanDefinition{}, false
}

func (p *Planner) buildFromPredefined(description, triggerType string, predef PlanDefinition) *model.DiagnosisPlan {
	now := time.Now()
	steps := make([]model.DiagnosisStep, len(predef.Steps))
	copy(steps, predef.Steps)
	for i := range steps {
		steps[i].Status = model.JobStatusPending
	}

	plan := model.DiagnosisPlan{
		Title:       predef.Title,
		Description: description,
		TriggerType: triggerType,
		Steps:       steps,
		Status:      model.PlanStatusPending,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	plan = p.store.Save(plan)
	return &plan
}

// RegisterPlanDefinition registers or replaces a runtime diagnosis plan definition.
func (p *Planner) RegisterPlanDefinition(triggerType string, definition PlanDefinition) {
	triggerType = strings.TrimSpace(triggerType)
	if triggerType == "" {
		return
	}
	p.customPlans[triggerType] = definition
}

func (p *Planner) buildFromLLM(ctx context.Context, description string) (*model.DiagnosisPlan, error) {
	messages := []llm.Message{
		{Role: "system", Content: p.systemPrompt},
		{Role: "user", Content: description},
	}

	raw, err := p.llmClient.Chat(ctx, messages)
	if err != nil {
		return nil, fmt.Errorf("llm call failed: %w", err)
	}

	// Parse LLM response (simplified: expect JSON)
	raw = extractJSON(raw)
	// For now, return error if no predefined plan and LLM response can't be parsed.
	// Full LLM parsing would require a more robust approach.
	_ = raw
	return nil, fmt.Errorf("LLM-based plan generation is not enabled; add a managed diagnosis plan for this scenario")
}

func extractJSON(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```json")
		s = strings.TrimPrefix(s, "```")
		s = strings.TrimSuffix(s, "```")
		s = strings.TrimSpace(s)
	}
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start != -1 && end > start {
		s = s[start : end+1]
	}
	return s
}
