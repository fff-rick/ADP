package template

import (
	"bytes"
	"fmt"
	"text/template"

	"adp/internal/domain/model"
	"adp/internal/module"
)

// Engine manages command templates: registration, lookup, and rendering.
type Engine struct {
	templates map[string]model.CommandTemplate
}

// NewEngine creates an Engine preloaded with built-in templates.
func NewEngine() *Engine {
	return &Engine{templates: make(map[string]model.CommandTemplate)}
}

// RegisterModule converts a Module to a CommandTemplate and registers it.
// Does NOT overwrite existing templates (builtin templates take precedence).
func (e *Engine) RegisterModule(m module.Module) {
	if _, exists := e.templates[m.Code()]; exists {
		return // already registered, skip
	}
	params := m.Parameters()
	tmplParams := make([]model.TemplateParam, len(params))
	for i, p := range params {
		tmplParams[i] = model.TemplateParam{
			Name:        p.Name,
			Description: p.Description,
			Required:    p.Required,
			Default:     p.Default,
		}
	}
	e.templates[m.Code()] = model.CommandTemplate{
		Code:        m.Code(),
		Name:        m.Name(),
		Description: m.Description(),
		ToolType:    m.ToolType(),
		Command:     "", // module-based, not shell template
		Parameters:  tmplParams,
		RiskLevel:   m.RiskLevel(),
	}
}

// GetTemplate returns a template by code.
func (e *Engine) GetTemplate(code string) (model.CommandTemplate, bool) {
	t, ok := e.templates[code]
	return t, ok
}

// RegisterTemplate registers or replaces a command template.
func (e *Engine) RegisterTemplate(t model.CommandTemplate) {
	e.templates[t.Code] = t
}

// ListTemplates returns all registered templates.
func (e *Engine) ListTemplates() []model.CommandTemplate {
	result := make([]model.CommandTemplate, 0, len(e.templates))
	for _, t := range e.templates {
		result = append(result, t)
	}
	return result
}

// Render fills in template parameters and returns the concrete command.
func (e *Engine) Render(code string, params map[string]string) (model.CommandTemplate, string, error) {
	tmpl, ok := e.templates[code]
	if !ok {
		return model.CommandTemplate{}, "", fmt.Errorf("template not found: %s", code)
	}

	// Apply defaults and check required parameters.
	merged := make(map[string]string, len(tmpl.Parameters))
	for _, p := range tmpl.Parameters {
		val, provided := params[p.Name]
		if !provided || val == "" {
			if p.Default != "" {
				merged[p.Name] = p.Default
			} else if p.Required {
				return model.CommandTemplate{}, "", fmt.Errorf("required parameter missing: %s", p.Name)
			}
		} else {
			merged[p.Name] = val
		}
	}

	t, err := template.New("cmd").Parse(tmpl.Command)
	if err != nil {
		return model.CommandTemplate{}, "", fmt.Errorf("template parse error: %w", err)
	}

	var buf bytes.Buffer
	if err := t.Execute(&buf, merged); err != nil {
		return model.CommandTemplate{}, "", fmt.Errorf("template render error: %w", err)
	}

	return tmpl, buf.String(), nil
}
