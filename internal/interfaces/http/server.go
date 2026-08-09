package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"adp/internal/application/agent"
	"adp/internal/domain/model"
	"adp/internal/domain/policy"
	"adp/internal/domain/template"
	"adp/internal/infrastructure/auth"
	"adp/internal/infrastructure/db"
	"adp/internal/infrastructure/llm"
	"adp/internal/infrastructure/workerstream"
	"adp/internal/module"
	"adp/internal/module/builtin"
)

type Config struct {
	Addr                  string
	AdminUsername         string
	AdminPassword         string
	AuthSecret            string
	WorkerSharedToken     string
	LLMBaseURL            string
	LLMAPIKey             string
	LLMModel              string
	AgentMaxSteps         int
	AgentAllowShell       bool
	ManagedConfigDir      string
	ManagedConfigSyncMode string
}

// HasLLM returns true if an LLM is configured.
func (c Config) HasLLM() bool { return c.LLMBaseURL != "" }

type Server struct {
	config       Config
	repo         db.Repository
	authService  *auth.Service
	templateEng  *template.Engine
	policyEng    *policy.Engine
	moduleReg    *module.Registry
	workerHub    *workerstream.Hub
	llmClient    llm.Client
	agentRuntime *agent.Runtime
	httpServer   *http.Server
}

// NewServer creates a new ADP server.
// If repo is nil, an in-memory fallback is used.
// If authSvc is nil, a default auth service is created from the config.
func NewServer(cfg Config, repo db.Repository, authSvc *auth.Service) *Server {
	if repo == nil {
		repo = db.NewMemoryRepository()
	}
	moduleReg := builtin.NewRegistry()
	templateEng := template.NewEngine()
	// Sync module registry templates into the template engine for backward compat.
	for _, m := range moduleReg.List() {
		templateEng.RegisterModule(m)
	}
	policyEng := policy.NewEngine()

	var llmClient llm.Client
	if cfg.LLMBaseURL != "" {
		llmClient = llm.NewHTTPClient(cfg.LLMBaseURL, cfg.LLMAPIKey, cfg.LLMModel)
	}

	if authSvc == nil {
		authSvc = auth.NewService(cfg.AdminUsername, cfg.AdminPassword, cfg.AuthSecret)
	}

	server := &Server{
		config:      cfg,
		repo:        repo,
		authService: authSvc,
		templateEng: templateEng,
		policyEng:   policyEng,
		moduleReg:   moduleReg,
		workerHub:   workerstream.NewHub(),
		llmClient:   llmClient,
	}
	server.agentRuntime = agent.New(llmClient, server.agentTools(), cfg.AgentMaxSteps, 0)

	if _, err := server.syncManagedConfigs(cfg.ManagedConfigDir, cfg.ManagedConfigSyncMode == "enforce"); err != nil {
		log.Printf("WARNING: failed to bootstrap managed configs: %v", err)
	}
	if err := server.reloadManagedConfigs(); err != nil {
		log.Printf("WARNING: failed to load managed configs: %v", err)
	}

	mux := http.NewServeMux()
	// UI pages
	mux.HandleFunc("GET /", server.handleDashboardPage)
	mux.HandleFunc("GET /login", server.handleDashboardPage)
	mux.HandleFunc("GET /users", server.handleDashboardPage)
	mux.HandleFunc("GET /workers", server.handleDashboardPage)
	mux.HandleFunc("GET /jobs", server.handleDashboardPage)
	mux.HandleFunc("GET /tasks", server.handleDashboardPage)
	mux.HandleFunc("GET /configs", server.handleDashboardPage)
	mux.Handle("GET /static/", server.staticAssetsHandler())
	// Health & metrics
	mux.HandleFunc("GET /healthz", server.handleHealthz)
	mux.HandleFunc("GET /metrics", server.handleMetrics)
	// Auth
	mux.HandleFunc("POST /api/v1/auth/login", server.handleLogin)
	// Users
	mux.HandleFunc("GET /api/v1/users", server.withUserAuth(server.handleListUsers))
	mux.HandleFunc("POST /api/v1/users", server.withUserAuth(server.handleCreateUser))
	mux.HandleFunc("DELETE /api/v1/users/", server.withUserAuth(server.handleDeleteUser))
	mux.HandleFunc("PUT /api/v1/users/", server.withUserAuth(server.handleChangePassword))
	// Dashboard
	mux.HandleFunc("GET /api/v1/dashboard/summary", server.withUserAuth(server.handleDashboardSummary))
	// Jobs
	mux.HandleFunc("POST /api/v1/jobs", server.withUserAuth(server.handleCreateJob))
	mux.HandleFunc("GET /api/v1/jobs", server.withUserAuth(server.handleListJobs))
	mux.HandleFunc("GET /api/v1/jobs/", server.withUserAuth(server.handleJobActions))
	mux.HandleFunc("POST /api/v1/jobs/", server.withUserAuth(server.handleJobActions))
	mux.HandleFunc("DELETE /api/v1/jobs/", server.withUserAuth(server.handleJobActions))
	mux.HandleFunc("POST /api/v1/jobs/yaml", server.withUserAuth(server.handleCreateJobFromYAML))
	// Workers
	mux.HandleFunc("POST /api/v1/workers", server.withUserAuth(server.handleCreateWorker))
	mux.HandleFunc("GET /api/v1/workers", server.withUserAuth(server.handleListWorkers))
	// Stop/restart/delete — user-authenticated (operator action).
	mux.HandleFunc("POST /api/v1/workers/{id}/stop", server.withUserAuth(server.handleWorkerUserAction))
	mux.HandleFunc("POST /api/v1/workers/{id}/restart", server.withUserAuth(server.handleWorkerUserAction))
	mux.HandleFunc("DELETE /api/v1/workers/{id}", server.withUserAuth(server.handleWorkerUserAction))
	// Worker self-service — worker-authenticated.
	mux.HandleFunc("POST /api/v1/workers/register", server.withWorkerAuth(server.handleRegisterWorker))
	mux.HandleFunc("POST /api/v1/workers/", server.withWorkerAuth(server.handleWorkerActions))
	mux.HandleFunc("PUT /api/v1/workers/", server.withWorkerAuth(server.handleWorkerActions))
	// Controlled operations agent.
	mux.HandleFunc("POST /api/v1/agent/runs", server.withUserAuth(server.handleAgentRun))
	// Conversations
	mux.HandleFunc("GET /api/v1/conversations", server.withUserAuth(server.handleListConversations))
	mux.HandleFunc("POST /api/v1/conversations", server.withUserAuth(server.handleCreateConversation))
	mux.HandleFunc("GET /api/v1/conversations/", server.withUserAuth(server.handleConversationActions))
	mux.HandleFunc("DELETE /api/v1/conversations/", server.withUserAuth(server.handleConversationActions))
	// Approvals
	mux.HandleFunc("GET /api/v1/approvals/jobs", server.withUserAuth(server.handleListPendingApprovalJobs))
	mux.HandleFunc("POST /api/v1/approvals/jobs/", server.withUserAuth(server.handleApproveJob))
	// Audit
	mux.HandleFunc("GET /api/v1/audit/logs", server.withUserAuth(server.handleListAuditLogs))
	// Cases
	mux.HandleFunc("GET /api/v1/cases", server.withUserAuth(server.handleListIncidentCases))
	mux.HandleFunc("GET /api/v1/cases/suggestions", server.withUserAuth(server.handleSuggestIncidentCases))
	// Runtime managed configs: modules/templates and policies.
	mux.HandleFunc("GET /api/v1/configs/", server.withAdminAuth(server.handleManagedConfigActions))
	mux.HandleFunc("POST /api/v1/configs/", server.withAdminAuth(server.handleManagedConfigActions))
	mux.HandleFunc("PUT /api/v1/configs/", server.withAdminAuth(server.handleManagedConfigActions))
	mux.HandleFunc("DELETE /api/v1/configs/", server.withAdminAuth(server.handleManagedConfigActions))
	// Worker logs (worker auth)
	mux.HandleFunc("POST /api/v1/job-logs", server.withWorkerAuth(server.handleAddWorkerLog))

	server.httpServer = &http.Server{
		Addr:              cfg.Addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	return server
}

type managedConfigSyncReport struct {
	Imported int      `json:"imported"`
	Updated  int      `json:"updated"`
	InSync   int      `json:"in_sync"`
	Drifted  []string `json:"drifted,omitempty"`
}

// syncManagedConfigs reconciles source-controlled YAML with runtime state.
// In missing mode it only imports absent configurations; enforce mode makes
// source YAML authoritative, which is intended for GitOps deployments.
func (s *Server) syncManagedConfigs(dir string, enforce bool) (managedConfigSyncReport, error) {
	report := managedConfigSyncReport{}
	if strings.TrimSpace(dir) == "" {
		return report, nil
	}
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || (filepath.Ext(path) != ".yaml" && filepath.Ext(path) != ".yml") {
			return nil
		}
		kind := filepath.Base(filepath.Dir(path))
		if !isSupportedConfigKind(kind) {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		id, name, err := managedConfigIdentity(kind, string(content))
		if err != nil {
			return fmt.Errorf("validate %s: %w", path, err)
		}
		existing, getErr := s.repo.GetManagedConfig(kind, id)
		if getErr != nil {
			_, err = s.repo.SaveManagedConfig(model.ManagedConfig{ID: id, Kind: kind, Name: name, YAMLContent: string(content), Active: true})
			if err == nil {
				report.Imported++
			}
			return err
		}
		if yamlSHA256(existing.YAMLContent) == yamlSHA256(string(content)) {
			report.InSync++
			return nil
		}
		key := kind + "/" + id
		if !enforce {
			report.Drifted = append(report.Drifted, key)
			return nil
		}
		_, err = s.repo.SaveManagedConfig(model.ManagedConfig{ID: id, Kind: kind, Name: name, YAMLContent: string(content), Active: true, CreatedAt: existing.CreatedAt})
		if err == nil {
			report.Updated++
		}
		return err
	})
	return report, err
}

func (s *Server) Start() error {
	return s.httpServer.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

func (s *Server) Handler() http.Handler {
	return s.httpServer.Handler
}

func (s *Server) Repository() db.Repository {
	return s.repo
}

func (s *Server) WorkerHub() *workerstream.Hub {
	return s.workerHub
}

// ── Generic handlers ──

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleJobActions(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet:
		s.handleGetJob(w, r)
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/dispatch"):
		s.handleDispatchJob(w, r)
	case r.Method == http.MethodDelete:
		s.handleDeleteJob(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("unsupported method"))
	}
}

// ── Helpers ──

func bearerToken(header string) string {
	if !strings.HasPrefix(header, "Bearer ") {
		return ""
	}
	return strings.TrimPrefix(header, "Bearer ")
}

func decodeJSON(r *http.Request, target any) error {
	defer r.Body.Close() //nolint:errcheck
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

func writeJSON(w http.ResponseWriter, statusCode int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, statusCode int, err error) {
	writeJSON(w, statusCode, map[string]string{"error": err.Error()})
}

func cloneStringMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func (s *Server) String() string {
	return fmt.Sprintf("server(addr=%s)", s.config.Addr)
}

func logEvent(component, action string, fields map[string]any) {
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var parts []string
	parts = append(parts, "level=INFO")
	parts = append(parts, fmt.Sprintf("component=%s", component))
	parts = append(parts, fmt.Sprintf("action=%s", action))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", key, fields[key]))
	}
	log.Print(strings.Join(parts, " "))
}
