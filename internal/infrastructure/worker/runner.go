package worker

import (
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"time"

	"adp/internal/config"
	"adp/internal/domain/model"
	"adp/internal/module"
	"adp/internal/module/builtin"
)

// Runner is the unprivileged execution half of a worker. It has no transport
// or server credentials: the Agent owns registration, heartbeats and result
// delivery, while the Runner owns local authorization and command execution.
type Runner struct {
	workerType        string
	execTimeout       time.Duration
	moduleReg         *module.Registry
	serviceConfigPath string
	serviceCatalog    *config.ServiceCatalog
}

func NewRunner(workerType string) *Runner {
	return &Runner{
		workerType:        workerType,
		execTimeout:       30 * time.Second,
		moduleReg:         builtin.NewRegistry(),
		serviceConfigPath: config.DefaultServicesConfigPath,
	}
}

func (r *Runner) SetExecTimeout(d time.Duration) { r.execTimeout = d }

func (r *Runner) SetServicesConfigPath(path string) {
	if strings.TrimSpace(path) != "" {
		r.serviceConfigPath = path
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
	mod, _ := r.moduleReg.Get(job.TemplateCode)
	params, service, err := r.resolveServiceProfile(job.TemplateCode, job.Parameters)
	if err != nil {
		return false, fmt.Sprintf("service profile: %v", err)
	}
	ctx := module.ExecContext{Params: params, WorkerInfo: CollectHostInfo(), Timeout: r.execTimeout, Service: service}
	cr, checkErr := mod.Check(ctx)
	if checkErr == nil && !cr.NeedsChange {
		return true, cr.CurrentState
	}
	result, execErr := mod.Execute(ctx)
	if execErr != nil {
		return false, fmt.Sprintf("%s\nerror: %v", result.Output, execErr)
	}
	return result.Success, result.Output
}

func (r *Runner) Authorize(job model.Job) error {
	workerType := model.NormalizeWorkerType(r.workerType)
	if !model.WorkerCanRunType(workerType, job.WorkerType) {
		return fmt.Errorf("worker type %q cannot run job type %q", r.workerType, job.WorkerType)
	}
	if strings.TrimSpace(job.TemplateCode) == "" {
		return fmt.Errorf("raw commands are not accepted; a registered module is required")
	}
	if workerType != "shell" && model.NormalizeWorkerType(job.Parameters["ServiceType"]) != workerType {
		return fmt.Errorf("typed worker %q requires ServiceType=%q", workerType, workerType)
	}
	mod, err := r.moduleReg.Get(job.TemplateCode)
	if err != nil {
		return fmt.Errorf("template %q is not an allowed module for typed workers", job.TemplateCode)
	}
	if model.NormalizeWorkerType(mod.ToolType()) != workerType {
		return fmt.Errorf("template %q belongs to worker type %q, not %q", job.TemplateCode, mod.ToolType(), workerType)
	}
	return nil
}

func (r *Runner) resolveServiceProfile(templateCode string, source map[string]string) (map[string]string, *config.RuntimeServiceProfile, error) {
	params := cloneStringMap(source)
	name := strings.TrimSpace(params["ServiceProfile"])
	if name == "" {
		return params, nil, nil
	}
	if r.serviceCatalog == nil {
		return nil, nil, fmt.Errorf("services config is not loaded")
	}
	serviceType := strings.ToLower(strings.TrimSpace(params["ServiceType"]))
	if serviceType == "" {
		return nil, nil, fmt.Errorf("template %q requires ServiceType when ServiceProfile is set", templateCode)
	}
	profile, err := r.serviceCatalog.Resolve(name, serviceType)
	if err != nil {
		return nil, nil, err
	}
	if profile.Host != "" {
		params["Host"] = profile.Host
	}
	if profile.Port != "" {
		params["Port"] = profile.Port
	}
	if profile.User != "" {
		params["User"] = profile.User
	}
	if profile.URL != "" {
		params["URL"] = profile.URL
	}
	if profile.Process != "" {
		params["Process"], params["ProcessName"] = profile.Process, profile.Process
	}
	if profile.LogFile != "" {
		params["LogFile"] = profile.LogFile
	}
	return params, &profile, nil
}

// CollectHostInfo is shared runtime data produced by the Runner, not the
// transport agent. This keeps the Agent independent of host probing details.
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
