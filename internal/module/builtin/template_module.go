package builtin

import (
	"bytes"
	"fmt"
	"os/exec"
	"text/template"
	"time"

	"adp/internal/domain/model"
	"adp/internal/module"
)

// TemplateModule is the universal executor for YAML-defined operation templates.
// It renders a Go text/template command string with parameters and executes it as
// a shell command. This replaces the old hardcoded per-operation Go modules.
type TemplateModule struct {
	tmpl model.CommandTemplate
}

func (m *TemplateModule) Code() string               { return m.tmpl.Code }
func (m *TemplateModule) Name() string               { return m.tmpl.Name }
func (m *TemplateModule) Description() string        { return m.tmpl.Description }
func (m *TemplateModule) ToolType() string           { return m.tmpl.ToolType }
func (m *TemplateModule) RiskLevel() model.RiskLevel { return m.tmpl.RiskLevel }
func (m *TemplateModule) Command() string            { return m.tmpl.Command }

func (m *TemplateModule) RiskProfile() module.RiskProfile {
	switch m.tmpl.RiskLevel {
	case model.RiskLevelHigh:
		return module.RiskProfile{Level: model.RiskLevelHigh, Reversible: false, ImpactScope: "cluster_wide", RequiresApproval: true}
	case model.RiskLevelMedium:
		return module.RiskProfile{Level: model.RiskLevelMedium, Reversible: true, ImpactScope: "single_host"}
	default:
		return module.RiskProfile{Level: model.RiskLevelLow, Reversible: true, ImpactScope: "single_host"}
	}
}

func (m *TemplateModule) Parameters() []module.ParamDef {
	out := make([]module.ParamDef, len(m.tmpl.Parameters))
	for i, p := range m.tmpl.Parameters {
		out[i] = module.ParamDef{
			Name:        p.Name,
			Description: p.Description,
			Required:    p.Required,
			Default:     p.Default,
		}
	}
	return out
}

func (m *TemplateModule) Check(ctx module.ExecContext) (module.CheckResult, error) {
	return module.CheckResult{NeedsChange: true}, nil
}

func (m *TemplateModule) Execute(ctx module.ExecContext) (module.Result, error) {
	cmd, err := m.renderCommand(ctx.Params)
	if err != nil {
		return module.Result{Success: false}, err
	}
	return runShellCommand(cmd, ctx.Timeout)
}

func (m *TemplateModule) DryRun(ctx module.ExecContext) (module.Result, error) {
	cmd, err := m.renderCommand(ctx.Params)
	if err != nil {
		return module.Result{Success: false}, err
	}
	return module.Result{Success: true, Output: cmd}, nil
}

func (m *TemplateModule) renderCommand(params map[string]string) (string, error) {
	tmpl, err := template.New(m.tmpl.Code).Parse(m.tmpl.Command)
	if err != nil {
		return "", fmt.Errorf("template parse error for %s: %w", m.tmpl.Code, err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, params); err != nil {
		return "", fmt.Errorf("template render error for %s: %w", m.tmpl.Code, err)
	}
	return buf.String(), nil
}

// RenderCommand is the public entry point used by the Server to pre-render a
// template into job.Command before dispatching.
func (m *TemplateModule) RenderCommand(params map[string]string) (string, error) {
	return m.renderCommand(params)
}

func runShellCommand(cmd string, timeout time.Duration) (module.Result, error) {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	c := exec.Command("sh", "-c", cmd)
	var out bytes.Buffer
	c.Stdout = &out
	c.Stderr = &out
	if err := c.Start(); err != nil {
		return module.Result{Success: false, Output: err.Error()}, err
	}
	done := make(chan error, 1)
	go func() { done <- c.Wait() }()
	select {
	case err := <-done:
		output := out.String()
		if err != nil {
			return module.Result{Success: false, Output: output, Changed: false}, nil
		}
		return module.Result{Success: c.ProcessState.ExitCode() == 0, Output: output, Changed: true}, nil
	case <-time.After(timeout):
		_ = c.Process.Kill()
		return module.Result{Success: false, Output: "command timed out"}, fmt.Errorf("command timed out after %s", timeout)
	}
}

// NewTemplateModule creates a TemplateModule from a model.CommandTemplate.
func NewTemplateModule(tmpl model.CommandTemplate) *TemplateModule {
	return &TemplateModule{tmpl: tmpl}
}
