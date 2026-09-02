package config

import "time"

// ServerConfig holds all configuration for the ADP server.
type ServerConfig struct {
	Addr                         string
	WorkerGRPCAddr               string
	DBDSN                        string
	AdminUsername                string
	AdminPassword                string
	AuthSecret                   string
	WorkerSharedToken            string
	LLMBaseURL                   string
	LLMAPIKey                    string
	LLMModel                     string
	AgentMaxSteps                int
	AgentContextWindowTokens     int
	AgentReservedOutputTokens    int
	AgentContextHardUsageRatio   float64
	AgentToolEvidenceMaxTokens   int
	AgentContextShadowEnabled    bool
	AgentInputTokenCostUSDPer1K  float64
	AgentOutputTokenCostUSDPer1K float64
	ManagedConfigDir             string
	ManagedConfigSyncMode        string
	RAGEnabled                   bool
	RAGEmbeddingBaseURL          string
	RAGEmbeddingAPIKey           string
	RAGEmbeddingModel            string
	RAGEmbeddingDimensions       int
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
