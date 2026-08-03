package builtin

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"adp/internal/domain/model"
	"adp/internal/module"
)

// HostDiagnostics collects a fixed, read-only health snapshot on the Worker host.
// It intentionally exposes no command or path parameter to the control plane.
type HostDiagnostics struct{}

func (m *HostDiagnostics) Code() string { return "host_diagnostics" }
func (m *HostDiagnostics) Name() string { return "宿主机健康快照" }
func (m *HostDiagnostics) Description() string {
	return "采集宿主机负载、内存和磁盘使用情况"
}
func (m *HostDiagnostics) ToolType() string           { return "shell" }
func (m *HostDiagnostics) RiskLevel() model.RiskLevel { return model.RiskLevelLow }
func (m *HostDiagnostics) RiskProfile() module.RiskProfile {
	return module.RiskProfile{Level: model.RiskLevelLow, Reversible: true, ImpactScope: "single_host"}
}
func (m *HostDiagnostics) Parameters() []module.ParamDef { return nil }
func (m *HostDiagnostics) Check(module.ExecContext) (module.CheckResult, error) {
	return module.CheckResult{NeedsChange: true, CurrentState: "will collect host diagnostics"}, nil
}
func (m *HostDiagnostics) Execute(ctx module.ExecContext) (module.Result, error) {
	parts := make([]string, 0, 3)
	for _, command := range [][]string{{"uptime"}, {"free", "-m"}, {"df", "-h"}} {
		out, err := exec.CommandContext(context.Background(), command[0], command[1:]...).CombinedOutput()
		if err != nil {
			return module.Result{Success: false, Output: fmt.Sprintf("%s: %s", command[0], string(out))}, nil
		}
		parts = append(parts, "$ "+strings.Join(command, " ")+"\n"+string(out))
	}
	return module.Result{Success: true, Output: strings.Join(parts, "\n"), Changed: false, Facts: map[string]string{"host_diagnostics": "collected"}}, nil
}
func (m *HostDiagnostics) DryRun(ctx module.ExecContext) (module.Result, error) {
	return module.Result{Success: true, Output: "would collect uptime, memory and disk usage"}, nil
}

// RestartService restarts only the systemd unit pinned in a Worker-local profile.
// The Agent never supplies the unit name or a shell command.
type RestartService struct{}

func (m *RestartService) Code() string { return "restart_service" }
func (m *RestartService) Name() string { return "重启受管服务" }
func (m *RestartService) Description() string {
	return "重启 Worker 本地 ServiceProfile 中固定的 systemd unit"
}
func (m *RestartService) ToolType() string           { return "shell" }
func (m *RestartService) RiskLevel() model.RiskLevel { return model.RiskLevelMedium }
func (m *RestartService) RiskProfile() module.RiskProfile {
	return module.RiskProfile{Level: model.RiskLevelMedium, Reversible: true, ImpactScope: "single_host", RequiresApproval: true}
}
func (m *RestartService) Parameters() []module.ParamDef {
	return []module.ParamDef{{Name: "ServiceProfile", Description: "Worker-local profile with a systemd unit", Required: true}, {Name: "ServiceType", Description: "Service type of the local profile", Required: true}}
}
func (m *RestartService) Check(ctx module.ExecContext) (module.CheckResult, error) {
	return module.CheckResult{NeedsChange: true, CurrentState: "restart requires explicit approval"}, nil
}
func (m *RestartService) Execute(ctx module.ExecContext) (module.Result, error) {
	if ctx.Service == nil || strings.TrimSpace(ctx.Service.Unit) == "" {
		return module.Result{Success: false}, fmt.Errorf("restart_service requires a Worker-local ServiceProfile with unit")
	}
	command := exec.CommandContext(context.Background(), "systemctl", "restart", ctx.Service.Unit)
	out, err := command.CombinedOutput()
	if err != nil {
		return module.Result{Success: false, Output: string(out)}, nil
	}
	return module.Result{Success: true, Output: fmt.Sprintf("restarted %s", ctx.Service.Unit), Changed: true}, nil
}
func (m *RestartService) DryRun(ctx module.ExecContext) (module.Result, error) {
	if ctx.Service == nil || ctx.Service.Unit == "" {
		return module.Result{Success: false}, fmt.Errorf("restart_service requires a Worker-local ServiceProfile with unit")
	}
	return module.Result{Success: true, Output: fmt.Sprintf("would restart %s", ctx.Service.Unit), Changed: true}, nil
}
