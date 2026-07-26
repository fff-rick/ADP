package builtin

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"adp/internal/domain/model"
	"adp/internal/module"
)

// ── redis_ping ──

type RedisPing struct{}

func (m *RedisPing) Code() string               { return "redis_ping" }
func (m *RedisPing) Name() string               { return "Redis PING" }
func (m *RedisPing) Description() string        { return "向 Redis 发送 PING 命令检查连通性" }
func (m *RedisPing) ToolType() string           { return "redis" }
func (m *RedisPing) RiskLevel() model.RiskLevel { return model.RiskLevelLow }
func (m *RedisPing) RiskProfile() module.RiskProfile {
	return module.RiskProfile{Level: model.RiskLevelLow, Reversible: true, ImpactScope: "single_host"}
}
func (m *RedisPing) Parameters() []module.ParamDef {
	return []module.ParamDef{{Name: "ServiceProfile", Description: "Worker services.cnf 中的 Redis Profile", Required: true}}
}
func (m *RedisPing) Check(_ module.ExecContext) (module.CheckResult, error) {
	return module.CheckResult{NeedsChange: true, CurrentState: "will check Redis connectivity"}, nil
}
func (m *RedisPing) Execute(ctx module.ExecContext) (module.Result, error) {
	out, err := runRedis(ctx, "PING")
	if err != nil {
		return module.Result{Success: false, Output: string(out)}, nil
	}
	result := strings.TrimSpace(string(out))
	success := result == "PONG"
	return module.Result{Success: success, Output: result, Changed: false}, nil
}
func (m *RedisPing) DryRun(ctx module.ExecContext) (module.Result, error) {
	return module.Result{Success: true, Output: "would run redis-cli using Worker-local ServiceProfile", Changed: false}, nil
}

// ── redis_info ──

type RedisInfo struct{}

func (m *RedisInfo) Code() string               { return "redis_info" }
func (m *RedisInfo) Name() string               { return "Redis INFO" }
func (m *RedisInfo) Description() string        { return "获取 Redis INFO 信息（内存、连接等）" }
func (m *RedisInfo) ToolType() string           { return "redis" }
func (m *RedisInfo) RiskLevel() model.RiskLevel { return model.RiskLevelLow }
func (m *RedisInfo) RiskProfile() module.RiskProfile {
	return module.RiskProfile{Level: model.RiskLevelLow, Reversible: true, ImpactScope: "single_host"}
}
func (m *RedisInfo) Parameters() []module.ParamDef {
	return []module.ParamDef{{Name: "ServiceProfile", Description: "Worker services.cnf 中的 Redis Profile", Required: true}}
}
func (m *RedisInfo) Check(_ module.ExecContext) (module.CheckResult, error) {
	return module.CheckResult{NeedsChange: true, CurrentState: "will collect Redis INFO"}, nil
}
func (m *RedisInfo) Execute(ctx module.ExecContext) (module.Result, error) {
	section := paramDefault(ctx.Params, "Section", "memory")
	out, err := runRedis(ctx, "INFO", section)
	if err != nil {
		return module.Result{Success: false, Output: string(out)}, nil
	}
	return module.Result{Success: true, Output: string(out), Changed: false, Facts: map[string]string{"redis_info": "collected"}}, nil
}
func (m *RedisInfo) DryRun(ctx module.ExecContext) (module.Result, error) {
	return module.Result{Success: true, Output: fmt.Sprintf("would redis-cli -h %s INFO memory", paramDefault(ctx.Params, "Host", "127.0.0.1")), Changed: false}, nil
}

// ── redis_slowlog_get ──

type RedisSlowlogGet struct{}

func (m *RedisSlowlogGet) Code() string               { return "redis_slowlog_get" }
func (m *RedisSlowlogGet) Name() string               { return "Redis 慢查询" }
func (m *RedisSlowlogGet) Description() string        { return "获取 Redis 慢查询日志" }
func (m *RedisSlowlogGet) ToolType() string           { return "redis" }
func (m *RedisSlowlogGet) RiskLevel() model.RiskLevel { return model.RiskLevelLow }
func (m *RedisSlowlogGet) RiskProfile() module.RiskProfile {
	return module.RiskProfile{Level: model.RiskLevelLow, Reversible: true, ImpactScope: "single_host"}
}
func (m *RedisSlowlogGet) Parameters() []module.ParamDef {
	return []module.ParamDef{
		{Name: "ServiceProfile", Description: "Worker services.cnf 中的 Redis Profile", Required: true},
		{Name: "Count", Description: "获取条数", Required: false, Default: "10"},
	}
}
func (m *RedisSlowlogGet) Check(_ module.ExecContext) (module.CheckResult, error) {
	return module.CheckResult{NeedsChange: true, CurrentState: "will fetch Redis slowlog"}, nil
}
func (m *RedisSlowlogGet) Execute(ctx module.ExecContext) (module.Result, error) {
	count := paramDefault(ctx.Params, "Count", "10")
	out, err := runRedis(ctx, "SLOWLOG", "GET", count)
	if err != nil {
		return module.Result{Success: false, Output: string(out)}, nil
	}
	result := strings.TrimSpace(string(out))
	hasEntries := result != "" && result != "(empty array)" && result != "(empty list or set)"
	return module.Result{Success: true, Output: result, Changed: false, Facts: map[string]string{"has_slowlog": fmt.Sprintf("%t", hasEntries)}}, nil
}
func (m *RedisSlowlogGet) DryRun(ctx module.ExecContext) (module.Result, error) {
	return module.Result{Success: true, Output: fmt.Sprintf("would run redis-cli SLOWLOG GET %s using Worker-local ServiceProfile", paramDefault(ctx.Params, "Count", "10")), Changed: false}, nil
}

// ── redis_client_list ──

type RedisClientList struct{}

func (m *RedisClientList) Code() string               { return "redis_client_list" }
func (m *RedisClientList) Name() string               { return "Redis 客户端列表" }
func (m *RedisClientList) Description() string        { return "获取 Redis 客户端连接列表" }
func (m *RedisClientList) ToolType() string           { return "redis" }
func (m *RedisClientList) RiskLevel() model.RiskLevel { return model.RiskLevelLow }
func (m *RedisClientList) RiskProfile() module.RiskProfile {
	return module.RiskProfile{Level: model.RiskLevelLow, Reversible: true, ImpactScope: "single_host"}
}
func (m *RedisClientList) Parameters() []module.ParamDef {
	return []module.ParamDef{{Name: "ServiceProfile", Description: "Worker services.cnf 中的 Redis Profile", Required: true}}
}
func (m *RedisClientList) Check(_ module.ExecContext) (module.CheckResult, error) {
	return module.CheckResult{NeedsChange: true, CurrentState: "will fetch Redis client list"}, nil
}
func (m *RedisClientList) Execute(ctx module.ExecContext) (module.Result, error) {
	out, err := runRedis(ctx, "CLIENT", "LIST")
	if err != nil {
		return module.Result{Success: false, Output: string(out)}, nil
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	return module.Result{Success: true, Output: string(out), Changed: false, Facts: map[string]string{"client_count": fmt.Sprintf("%d", len(lines))}}, nil
}
func (m *RedisClientList) DryRun(ctx module.ExecContext) (module.Result, error) {
	return module.Result{Success: true, Output: "would run redis-cli CLIENT LIST using Worker-local ServiceProfile", Changed: false}, nil
}

func runRedis(ctx module.ExecContext, args ...string) ([]byte, error) {
	if ctx.Service == nil || ctx.Service.Type != "redis" {
		return nil, fmt.Errorf("redis task requires a Worker-local redis ServiceProfile")
	}
	command := exec.Command("redis-cli", append([]string{"-h", ctx.Service.Host, "-p", ctx.Service.Port}, args...)...)
	command.Env = os.Environ()
	if ctx.Service.Password != "" {
		command.Env = append(command.Env, "REDISCLI_AUTH="+ctx.Service.Password)
	}
	return command.CombinedOutput()
}
