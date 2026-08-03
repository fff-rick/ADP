package config

import "time"

// ServerConfig holds all configuration for the ADP server.
type ServerConfig struct {
	Addr                  string
	WorkerGRPCAddr        string
	DBDSN                 string
	AdminUsername         string
	AdminPassword         string
	AuthSecret            string
	WorkerSharedToken     string
	LLMBaseURL            string
	LLMAPIKey             string
	LLMModel              string
	AgentMaxSteps         int
	ManagedConfigDir      string
	ManagedConfigSyncMode string
}

// WorkerConfig holds all configuration for the ADP worker.
type WorkerConfig struct {
	ServerURL           string
	GRPCServerAddr      string
	WorkerToken         string
	Name                string
	Type                string
	PollInterval        time.Duration
	ExecTimeout         time.Duration
	HostCollectInterval time.Duration
	LogToDB             bool
	ServicesConfigPath  string
}
