package worker

import (
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"

	"adp/internal/config"
	"adp/internal/domain/model"
)

// Default blocked tools — commands starting with these are never executed directly
// by the Worker. Template-based jobs bypass this check; only agent_shell jobs are
// subject to the blocklist.
var defaultBlockedTools = map[string]bool{
	"rm": true, "dd": true, "mkfs": true, "fdisk": true,
	"shutdown": true, "reboot": true, "halt": true, "poweroff": true,
	"kill": true, "killall": true, "pkill": true,
	"chmod": true, "chown": true, "passwd": true,
	"useradd": true, "userdel": true, "groupadd": true,
	"iptables": true, "ip6tables": true,
}

// Runner is the unprivileged execution half of a worker. It has no transport
// or server credentials: the Agent owns registration, heartbeats and result
// delivery, while the Runner owns local authorization and command execution.
type Runner struct {
	workerType        string
	execTimeout       time.Duration
	serviceConfigPath string
	serviceCatalog    *config.ServiceCatalog
	blockedTools      map[string]bool
}

func NewRunner(workerType string) *Runner {
	return &Runner{
		workerType:        workerType,
		execTimeout:       30 * time.Second,
		serviceConfigPath: config.DefaultServicesConfigPath,
		blockedTools:      defaultBlockedTools,
	}
}

func (r *Runner) SetExecTimeout(d time.Duration)  { r.execTimeout = d }

func (r *Runner) SetServicesConfigPath(path string) {
	if strings.TrimSpace(path) != "" {
		r.serviceConfigPath = path
	}
}

// SetBlockedTools overrides the default blocked-tools list.
func (r *Runner) SetBlockedTools(blocked map[string]bool) {
	if blocked != nil {
		r.blockedTools = blocked
	}
}

func (r *Runner) Load() error {
	catalog, err := config.LoadServiceCatalog(r.serviceConfigPath)
	if err != nil {
		return fmt.Errorf("load services config: %w", err)
	}
	r.serviceCatalog = catalog
	return nil
}

func (r *Runner) Execute(job model.Job, workerID string) (bool, string) {
	if err := r.Authorize(job); err != nil {
		log.Printf("[worker:%s][job:%s] 拒绝执行: %v", workerID, job.ID, err)
		return false, "authorization denied: " + err.Error()
	}

	cmd := strings.TrimSpace(job.Command)
	if cmd == "" {
		return false, "job has no command to execute"
	}

	// Resolve ServiceProfile parameters if present.
	params, _, err := r.resolveServiceProfile(job.Parameters)
	if err != nil {
		return false, fmt.Sprintf("service profile: %v", err)
	}

	// Substitute any remaining {{.Param}} references with resolved values.
	cmd = r.substituteParams(cmd, params)

	return r.executeShellCommand(cmd)
}

func (r *Runner) Authorize(job model.Job) error {
	workerType := model.NormalizeWorkerType(r.workerType)
	if !model.WorkerCanRunType(workerType, job.WorkerType) {
		return fmt.Errorf("worker type %q cannot run job type %q", r.workerType, job.WorkerType)
	}

	if strings.TrimSpace(job.Command) == "" {
		return fmt.Errorf("job has no command to execute")
	}

	// Template-based jobs (agent, template, yaml_job, manual_job) are
	// pre-authorized — the template itself was reviewed and approved.
	if job.SourceType == "agent" || job.SourceType == "template" ||
		job.SourceType == "yaml_job" || job.SourceType == "manual_job" ||
		job.SourceType == "manual_dispatch" {
		return nil
	}

	// agent_shell: Agent-crafted commands — blocklist check.
	if job.SourceType == "agent_shell" {
		tool := strings.Split(strings.TrimSpace(job.Command), " ")[0]
		if r.blockedTools[tool] {
			return fmt.Errorf("blocked tool: %s", tool)
		}
		return nil
	}

	return fmt.Errorf("unknown job source type: %s", job.SourceType)
}

func (r *Runner) executeShellCommand(cmd string) (bool, string) {
	c := exec.Command("sh", "-c", cmd)
	var out strings.Builder
	c.Stdout = &out
	c.Stderr = &out

	if err := c.Start(); err != nil {
		return false, err.Error()
	}

	done := make(chan error, 1)
	go func() { done <- c.Wait() }()

	select {
	case err := <-done:
		output := out.String()
		if err != nil {
			return false, output
		}
		return c.ProcessState.ExitCode() == 0, output
	case <-time.After(r.execTimeout):
		_ = c.Process.Kill()
		return false, "command timed out after " + r.execTimeout.String()
	}
}

func (r *Runner) resolveServiceProfile(params map[string]string) (map[string]string, *config.RuntimeServiceProfile, error) {
	p := cloneStringMap(params)
	name := strings.TrimSpace(p["ServiceProfile"])
	if name == "" {
		return p, nil, nil
	}
	if r.serviceCatalog == nil {
		return nil, nil, fmt.Errorf("services config is not loaded")
	}
	serviceType := strings.ToLower(strings.TrimSpace(p["ServiceType"]))
	if serviceType == "" {
		return nil, nil, fmt.Errorf("ServiceType is required when ServiceProfile is set")
	}
	profile, err := r.serviceCatalog.Resolve(name, serviceType)
	if err != nil {
		return nil, nil, err
	}
	if profile.Host != "" {
		p["Host"] = profile.Host
	}
	if profile.Port != "" {
		p["Port"] = profile.Port
	}
	if profile.User != "" {
		p["User"] = profile.User
	}
	if profile.URL != "" {
		p["URL"] = profile.URL
	}
	if profile.Process != "" {
		p["Process"] = profile.Process
		p["ProcessName"] = profile.Process
	}
	if profile.LogFile != "" {
		p["LogFile"] = profile.LogFile
	}
	if profile.Unit != "" {
		p["Unit"] = profile.Unit
	}
	return p, &profile, nil
}

func (r *Runner) substituteParams(cmd string, params map[string]string) string {
	for k, v := range params {
		cmd = strings.ReplaceAll(cmd, "{{."+k+"}}", v)
	}
	return cmd
}

// CollectHostInfo gathers host-level information for heartbeats.
func CollectHostInfo() model.HostInfo {
	info := model.HostInfo{}
	if hostname, err := os.Hostname(); err == nil {
		info.Hostname = hostname
	}
	if conn, err := net.Dial("udp", "8.8.8.8:80"); err == nil {
		if addr, ok := conn.LocalAddr().(*net.UDPAddr); ok {
			info.IPAddress = addr.IP.String()
		}
		_ = conn.Close()
	}
	info.CPUUsage, info.StorageUsage = readCPUUsage(), readDiskUsage()
	return info
}
