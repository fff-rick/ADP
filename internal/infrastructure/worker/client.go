package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	adpv1 "adp/api/proto/adp/v1"
	"adp/internal/config"
	"adp/internal/domain/model"
	"adp/internal/module"
	"adp/internal/module/builtin"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

// Client is the transport-facing Agent. It owns registration, heartbeats,
// control messages and result delivery; Runner owns execution.
type Client struct {
	serverURL           string
	grpcServerAddr      string
	workerToken         string
	name                string
	workerType          string
	pollInterval        time.Duration
	httpClient          *http.Client
	registeredID        string
	execTimeout         time.Duration
	hostCollectInterval time.Duration
	logToDB             bool
	moduleReg           *module.Registry // module registry for idempotent execution
	serviceConfigPath   string
	serviceCatalog      *config.ServiceCatalog
	runner              *Runner
	runnerMu            sync.RWMutex
}

// Agent is the preferred name for the control-plane half of a Worker. Client
// remains an alias so existing integrations and the server subcommand keep
// compiling during the migration.
type Agent = Client

// NewAgent creates an Agent with its own local Runner.
func NewAgent(serverURL, workerToken, name, workerType string, pollInterval time.Duration) *Agent {
	return NewClient(serverURL, workerToken, name, workerType, pollInterval)
}

// NewClient creates a new worker client.
func NewClient(serverURL, workerToken, name, workerType string, pollInterval time.Duration) *Client {
	return &Client{
		serverURL:           strings.TrimRight(serverURL, "/"),
		grpcServerAddr:      "127.0.0.1:9090",
		workerToken:         workerToken,
		name:                name,
		workerType:          workerType,
		pollInterval:        pollInterval,
		httpClient:          &http.Client{Timeout: 5 * time.Second},
		execTimeout:         30 * time.Second,
		hostCollectInterval: 60 * time.Second,
		moduleReg:           builtin.NewRegistry(),
		serviceConfigPath:   config.DefaultServicesConfigPath,
		runner:              NewRunner(workerType),
	}
}

// SetExecTimeout sets the command execution timeout.
func (c *Client) SetExecTimeout(d time.Duration) { c.execTimeout = d; c.runner.SetExecTimeout(d) }

// SetGRPCServerAddr sets the worker gRPC server address.
func (c *Client) SetGRPCServerAddr(addr string) {
	addr = strings.TrimSpace(addr)
	if addr != "" {
		c.grpcServerAddr = addr
	}
}

// SetHostCollectInterval sets the host info collection interval.
func (c *Client) SetHostCollectInterval(d time.Duration) { c.hostCollectInterval = d }

// SetLogToDB enables or disables sending job logs to the server database.
func (c *Client) SetLogToDB(enabled bool) { c.logToDB = enabled }

// SetServicesConfigPath sets the only Worker-local service configuration file.
func (c *Client) SetServicesConfigPath(path string) {
	if strings.TrimSpace(path) != "" {
		c.serviceConfigPath = path
		c.runner.SetServicesConfigPath(path)
	}
}

// Run starts the worker main loop.
func (c *Client) Run() error {
	if err := c.runner.Load(); err != nil {
		return err
	}
	for {
		if err := c.runGRPCStream(); err != nil {
			log.Printf("[worker:%s] gRPC stream disconnected: %v", c.registeredID, err)
			time.Sleep(5 * time.Second)
			continue
		}
		return nil
	}
}

func (c *Client) runGRPCStream() error {
	conn, err := grpc.NewClient(c.grpcServerAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("create grpc client: %w", err)
	}
	defer func() { _ = conn.Close() }()

	ctx := metadata.AppendToOutgoingContext(context.Background(), "x-worker-token", c.workerToken)
	stream, err := adpv1.NewWorkerServiceClient(conn).Stream(ctx)
	if err != nil {
		return fmt.Errorf("open worker stream: %w", err)
	}

	var sendMu sync.Mutex
	send := func(msg *adpv1.WorkerEnvelope) error {
		sendMu.Lock()
		defer sendMu.Unlock()
		return stream.Send(msg)
	}

	if err := send(&adpv1.WorkerEnvelope{
		Payload: &adpv1.WorkerEnvelope_Register{
			Register: &adpv1.RegisterRequest{
				Name:       c.name,
				WorkerType: c.workerType,
			},
		},
	}); err != nil {
		return fmt.Errorf("send register: %w", err)
	}

	registered, err := stream.Recv()
	if err != nil {
		return fmt.Errorf("receive register response: %w", err)
	}
	if registered.GetRegistered() == nil {
		return fmt.Errorf("unexpected register response: %T", registered.GetPayload())
	}
	c.registeredID = registered.GetRegistered().GetWorkerId()
	log.Printf("[worker:%s] gRPC 注册成功 name=%s type=%s", c.registeredID, registered.GetRegistered().GetName(), registered.GetRegistered().GetWorkerType())

	heartbeatTicker := time.NewTicker(10 * time.Second)
	defer heartbeatTicker.Stop()

	done := make(chan error, 1)
	go func() {
		for range heartbeatTicker.C {
			if err := send(c.heartbeatEnvelope()); err != nil {
				done <- err
				return
			}
		}
	}()

	if err := send(c.heartbeatEnvelope()); err != nil {
		return fmt.Errorf("send initial heartbeat: %w", err)
	}

	for {
		select {
		case err := <-done:
			return err
		default:
		}

		msg, err := stream.Recv()
		if errorsIsEOF(err) {
			return io.EOF
		}
		if err != nil {
			return err
		}

		switch payload := msg.GetPayload().(type) {
		case *adpv1.ServerEnvelope_Job:
			job := protoJobToModel(payload.Job)
			log.Printf("[worker:%s][job:%s] 收到 gRPC 推送任务", c.registeredID, job.ID)
			go func() {
				success, output := c.executeJobLocally(job)
				if err := send(&adpv1.WorkerEnvelope{
					Payload: &adpv1.WorkerEnvelope_JobResult{
						JobResult: &adpv1.JobResult{
							WorkerId: c.registeredID,
							JobId:    job.ID,
							Command:  job.Command,
							Success:  success,
							Output:   output,
							LogToDb:  c.logToDB,
						},
					},
				}); err != nil {
					log.Printf("[worker:%s][job:%s] 回传结果失败: %v", c.registeredID, job.ID, err)
				}
			}()
		case *adpv1.ServerEnvelope_Command:
			if c.handleCommand(payload.Command, send) {
				return nil
			}
		case *adpv1.ServerEnvelope_Error:
			log.Printf("[worker:%s] server error: %s", c.registeredID, payload.Error)
		}
	}
}

// executeJob runs a single job through the module system or shell fallback.
func (c *Client) executeJob(job model.Job) {
	success, output := c.executeJobLocally(job)
	c.complete(job.ID, success, output) //nolint:errcheck
}

func (c *Client) executeJobLocally(job model.Job) (bool, string) {
	c.runnerMu.RLock()
	runner := c.runner
	c.runnerMu.RUnlock()
	return runner.Execute(job, c.registeredID)
}

// authorizeJob is the data-plane authorization boundary. Server-side routing
// is useful for scheduling, but a worker must reject a mismatched or forged
// job independently because it is the process that would execute the command.
//
// A non-shell worker may execute only a registered module for its own service
// type. In particular, it never executes a raw shell command: parsing a
// command line for "mysql" or "redis" would be bypassable with pipes, shell
// substitutions, or a second command after a semicolon.
func (c *Client) authorizeJob(job model.Job) error {
	c.runnerMu.RLock()
	runner := c.runner
	c.runnerMu.RUnlock()
	return runner.Authorize(job)
}

// restartRunner creates a fresh execution runtime without dropping the
// Agent's control-plane connection. In the next phase this same boundary can
// be backed by a child process instead of an in-process Runner.
func (c *Client) restartRunner() error {
	runner := NewRunner(c.workerType)
	runner.SetExecTimeout(c.execTimeout)
	runner.SetServicesConfigPath(c.serviceConfigPath)
	if err := runner.Load(); err != nil {
		return err
	}
	c.runnerMu.Lock()
	c.runner = runner
	c.runnerMu.Unlock()
	return nil
}

func (c *Client) heartbeatEnvelope() *adpv1.WorkerEnvelope {
	info := CollectHostInfo()
	return &adpv1.WorkerEnvelope{
		Payload: &adpv1.WorkerEnvelope_Heartbeat{
			Heartbeat: &adpv1.Heartbeat{
				WorkerId: c.registeredID,
				HostInfo: &adpv1.HostInfo{
					Hostname:     info.Hostname,
					IpAddress:    info.IPAddress,
					CpuUsage:     info.CPUUsage,
					StorageUsage: info.StorageUsage,
				},
			},
		},
	}
}

func (c *Client) handleCommand(command string, send func(*adpv1.WorkerEnvelope) error) bool {
	switch command {
	case "stop":
		_ = c.sendCommandAck(send, command, true)
		log.Printf("[worker:%s] 收到停止指令(gRPC)，退出", c.registeredID)
		return true
	case "restart":
		if err := c.restartRunner(); err != nil {
			log.Printf("[worker:%s] 重启 Runner 失败: %v", c.registeredID, err)
			_ = c.sendCommandAck(send, command, false)
			return false
		}
		_ = c.sendCommandAck(send, command, true)
		log.Printf("[worker:%s] Runner 已重启，Agent 保持连接", c.registeredID)
		return false
	case "force_stop":
		_ = c.sendCommandAck(send, command, true)
		log.Printf("[worker:%s] 收到强制停止指令(gRPC)，立即退出", c.registeredID)
		os.Exit(1)
	}
	return false
}

func (c *Client) sendCommandAck(send func(*adpv1.WorkerEnvelope) error, command string, success bool) error {
	return send(&adpv1.WorkerEnvelope{
		Payload: &adpv1.WorkerEnvelope_CommandAck{
			CommandAck: &adpv1.CommandAck{
				WorkerId: c.registeredID,
				Command:  command,
				Success:  success,
			},
		},
	})
}

func protoJobToModel(job *adpv1.Job) model.Job {
	if job == nil {
		return model.Job{}
	}
	return model.Job{
		ID:           job.GetId(),
		Name:         job.GetName(),
		WorkerType:   job.GetWorkerType(),
		Command:      job.GetCommand(),
		TemplateCode: job.GetTemplateCode(),
		Parameters:   cloneStringMap(job.GetParameters()),
	}
}

func errorsIsEOF(err error) bool {
	return err == io.EOF
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// executeCommand runs a shell command with timeout and returns combined output.
func (c *Client) executeCommand(cmd string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), c.execTimeout)
	defer cancel()

	out, err := exec.CommandContext(ctx, "sh", "-c", cmd).CombinedOutput()
	if err != nil {
		return fmt.Sprintf("%s\n[exit_error: %v]", string(out), err), false
	}
	return string(out), true
}

// truncate shortens a string to maxLen characters.
func truncate(s string, maxLen int) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxLen {
		return strings.ReplaceAll(s, "\n", "\\n")
	}
	return strings.ReplaceAll(s[:maxLen], "\n", "\\n") + "..."
}

// collectHostInfo gathers host-level information.
func (c *Client) collectHostInfo() model.HostInfo {
	info := model.HostInfo{}

	hostname, err := os.Hostname()
	if err == nil {
		info.Hostname = hostname
	}

	// Get outbound IP.
	if conn, err := net.Dial("udp", "8.8.8.8:80"); err == nil {
		if addr, ok := conn.LocalAddr().(*net.UDPAddr); ok {
			info.IPAddress = addr.IP.String()
		}
		_ = conn.Close()
	}

	// CPU usage from /proc/stat (Linux).
	info.CPUUsage = readCPUUsage()

	// Disk usage for / mount.
	info.StorageUsage = readDiskUsage()

	return info
}

// readCPUUsage reads CPU usage from /proc/stat (Linux only).
func readCPUUsage() float64 {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0
	}
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 5 {
			return 0
		}
		var total, idle float64
		for i, f := range fields[1:] {
			var v float64
			if _, err := fmt.Sscanf(f, "%f", &v); err != nil {
				return 0
			}
			total += v
			if i == 3 { // idle is 4th field (0-indexed 3)
				idle = v
			}
		}
		if total > 0 {
			return (1 - idle/total) * 100
		}
	}
	return 0
}

// readDiskUsage reads disk usage for the root filesystem.
func readDiskUsage() float64 {
	var stat syscall.Statfs_t
	if err := syscall.Statfs("/", &stat); err != nil {
		return 0
	}
	total := stat.Blocks * uint64(stat.Bsize)
	free := stat.Bfree * uint64(stat.Bsize)
	if total > 0 {
		return float64(total-free) / float64(total) * 100
	}
	return 0
}

func (c *Client) complete(jobID string, success bool, output string) error {
	path := fmt.Sprintf("/api/v1/workers/%s/jobs/%s/complete", c.registeredID, jobID)
	return c.postJSON(path, map[string]any{
		"success": success,
		"output":  output,
	}, nil)
}

func (c *Client) postJSON(path string, body any, target any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, c.serverURL+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Worker-Token", c.workerToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= http.StatusBadRequest {
		var apiErr map[string]string
		_ = json.NewDecoder(resp.Body).Decode(&apiErr)
		return fmt.Errorf("request failed: %s", apiErr["error"])
	}

	if target != nil {
		return json.NewDecoder(resp.Body).Decode(target)
	}

	return nil
}
