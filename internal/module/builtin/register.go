package builtin

import "adp/internal/module"

// RegisterAll registers all built-in modules into the given registry.
func RegisterAll(reg *module.Registry) {
	reg.Register(&MySQLBackup{})
	reg.Register(&CheckProcess{})
	reg.Register(&CheckPort{})
	reg.Register(&HTTPHealthCheck{})
	reg.Register(&ReadLogTail{})
	reg.Register(&HostDiagnostics{})
	reg.Register(&RestartService{})
	reg.Register(&RedisPing{})
	reg.Register(&RedisInfo{})
	reg.Register(&RedisSlowlogGet{})
	reg.Register(&RedisClientList{})
}

// NewRegistry creates a registry pre-populated with all built-in modules.
func NewRegistry() *module.Registry {
	reg := module.NewRegistry()
	RegisterAll(reg)
	return reg
}
