package module

import (
	"fmt"
	"sync"
	"time"

	"adp/internal/config"
	"adp/internal/domain/model"
)

// ExecContext holds the execution environment for a module.
type ExecContext struct {
	Params     map[string]string
	WorkerInfo model.HostInfo
	Timeout    time.Duration
	Service    *config.RuntimeServiceProfile
}

// Result is the outcome of a module execution.
type Result struct {
	Success bool
	Output  string
	Changed bool              // whether the execution actually changed state
	Facts   map[string]string // collected information after execution
}

// CheckResult indicates whether a task needs to be executed.
type CheckResult struct {
	NeedsChange  bool   // true if Execute should run
	CurrentState string // human-readable description of current state
}

// ParamDef describes a module parameter.
type ParamDef struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Required    bool   `json:"required"`
	Default     string `json:"default,omitempty"`
}

// RiskProfile defines the risk characteristics of a module.
type RiskProfile struct {
	Level            model.RiskLevel
	Reversible       bool
	ImpactScope      string // "single_host" | "cluster_wide"
	RequiresApproval bool
	MaxAutoRetry     int
}

// Module is the pluggable execution unit interface.
// Each module represents a single operational capability (e.g., mysql_backup, check_port).
type Module interface {
	// Identity
	Code() string
	Name() string
	Description() string
	ToolType() string

	// Parameters
	Parameters() []ParamDef

	// Risk
	RiskLevel() model.RiskLevel
	RiskProfile() RiskProfile

	// Execution
	// Check returns whether the task needs to be executed (idempotency check).
	Check(ctx ExecContext) (CheckResult, error)
	// Execute performs the actual operation.
	Execute(ctx ExecContext) (Result, error)
	// DryRun simulates execution without making changes.
	DryRun(ctx ExecContext) (Result, error)
}

// Registry is a thread-safe registry of modules.
type Registry struct {
	mu      sync.RWMutex
	modules map[string]Module
}

// NewRegistry creates a new module registry.
func NewRegistry() *Registry {
	return &Registry{modules: make(map[string]Module)}
}

// Register adds a module to the registry.
func (r *Registry) Register(m Module) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.modules[m.Code()] = m
}

// Clear removes all modules from the registry.
func (r *Registry) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.modules = make(map[string]Module)
}

// Get returns a module by code.
func (r *Registry) Get(code string) (Module, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.modules[code]
	if !ok {
		return nil, fmt.Errorf("module not found: %s", code)
	}
	return m, nil
}

// List returns all registered modules.
func (r *Registry) List() []Module {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]Module, 0, len(r.modules))
	for _, m := range r.modules {
		result = append(result, m)
	}
	return result
}

// ListAsTemplates converts all registered modules to CommandTemplate format for API compatibility.
func (r *Registry) ListAsTemplates() []model.CommandTemplate {
	r.mu.RLock()
	defer r.mu.RUnlock()
	templates := make([]model.CommandTemplate, 0, len(r.modules))
	for _, m := range r.modules {
		params := m.Parameters()
		tmpl := model.CommandTemplate{
			Code:        m.Code(),
			Name:        m.Name(),
			Description: m.Description(),
			ToolType:    m.ToolType(),
			Command:     "", // modules don't expose raw command
			Parameters:  make([]model.TemplateParam, len(params)),
			RiskLevel:   m.RiskLevel(),
		}
		for i, p := range params {
			tmpl.Parameters[i] = model.TemplateParam{
				Name:        p.Name,
				Description: p.Description,
				Required:    p.Required,
				Default:     p.Default,
			}
		}
		templates = append(templates, tmpl)
	}
	return templates
}
