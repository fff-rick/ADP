package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	adpv1 "adp/api/proto/adp/v1"
	"adp/internal/config"
	"adp/internal/infrastructure/auth"
	"adp/internal/infrastructure/db"
	"adp/internal/infrastructure/worker"
	"adp/internal/infrastructure/workerstream"
	api "adp/internal/interfaces/http"

	"google.golang.org/grpc"
)

/*
Use: 命令名称
Short: 简短描述（help）
Long: 详细描述
*/
var rootCmd = &cobra.Command{
	Use:   "adp-server",
	Short: "ADP AI Diagnostic Platform - Server",
	Long:  `ADP Server provides the HTTP API, task scheduling, and AI-assisted diagnosis for the ADP platform.`,
}

/*
RunE: 要求返回一个错误
*/
var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the ADP server",
	RunE:  runServe,
}

func init() {
	// Server flags
	serveCmd.Flags().String("addr", ":8080", "HTTP listen address")
	serveCmd.Flags().String("worker-grpc-addr", ":9090", "Worker gRPC listen address")
	serveCmd.Flags().String("db-dsn", "", "PostgreSQL DSN (e.g., postgres://user:pass@localhost:5432/adp?sslmode=disable)")
	serveCmd.Flags().String("admin-username", "admin", "Initial admin username")
	serveCmd.Flags().String("admin-password", "", "Initial admin password (required)")
	serveCmd.Flags().String("auth-secret", "", "JWT signing secret (required)")
	serveCmd.Flags().String("worker-token", "", "Worker shared secret token (required)")
	serveCmd.Flags().String("agent-base-url", "", "Agent model API base URL")
	serveCmd.Flags().String("agent-api-key", "", "Agent model API key")
	serveCmd.Flags().String("agent-model", "", "Agent model name")
	serveCmd.Flags().Int("agent-max-steps", 12, "Maximum tool-calling steps per Agent run")
	serveCmd.Flags().String("managed-config-dir", "configs/managed", "Source-controlled managed YAML configuration directory")
	serveCmd.Flags().String("managed-config-sync-mode", "missing", "Managed config sync mode: missing or enforce")
	serveCmd.Flags().String("config", "", "Path to YAML config file")

	// Bind flags to viper.
	// env_format：ADP_ADDR
	_ = viper.BindPFlag("addr", serveCmd.Flags().Lookup("addr"))
	_ = viper.BindPFlag("worker.grpc_addr", serveCmd.Flags().Lookup("worker-grpc-addr"))
	_ = viper.BindPFlag("db.dsn", serveCmd.Flags().Lookup("db-dsn"))
	_ = viper.BindPFlag("auth.admin_username", serveCmd.Flags().Lookup("admin-username"))
	_ = viper.BindPFlag("auth.admin_password", serveCmd.Flags().Lookup("admin-password"))
	_ = viper.BindPFlag("auth.secret", serveCmd.Flags().Lookup("auth-secret"))
	_ = viper.BindPFlag("auth.worker_token", serveCmd.Flags().Lookup("worker-token"))
	_ = viper.BindPFlag("agent.base_url", serveCmd.Flags().Lookup("agent-base-url"))
	_ = viper.BindPFlag("agent.api_key", serveCmd.Flags().Lookup("agent-api-key"))
	_ = viper.BindPFlag("agent.model", serveCmd.Flags().Lookup("agent-model"))
	_ = viper.BindPFlag("agent.max_steps", serveCmd.Flags().Lookup("agent-max-steps"))
	_ = viper.BindPFlag("managed_config_dir", serveCmd.Flags().Lookup("managed-config-dir"))
	_ = viper.BindPFlag("managed_config_sync_mode", serveCmd.Flags().Lookup("managed-config-sync-mode"))

	// Viper config: env vars with ADP_ prefix, config file support
	viper.SetEnvPrefix("ADP")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))
	viper.AutomaticEnv()
	viper.SetConfigName("adp")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")
	viper.AddConfigPath("configs/server/")
	viper.AddConfigPath("/etc/adp/")
	viper.AddConfigPath("/etc/adp/server/")
	/*
			# 优先级从高到低：
		1. 命令行参数：--addr=:9090
		2. 环境变量：ADP_ADDR=:9090
		3. 配置文件：./adp.yaml、configs/server/adp.yaml、/etc/adp/adp.yaml 或 /etc/adp/server/adp.yaml
		4. 默认值：":8080"
	*/

	rootCmd.AddCommand(serveCmd)

	// Worker subcommand: allows the server binary to also run as a worker.
	workerCmd := &cobra.Command{
		Use:   "worker",
		Short: "Worker operations",
		Long:  `Run as an ADP worker agent.`,
	}
	workerRunCmd := &cobra.Command{
		Use:   "run",
		Short: "Start the ADP worker",
		RunE:  runWorkerAsSubcommand,
	}
	workerRunCmd.Flags().String("server-url", "http://127.0.0.1:8080", "ADP server URL")
	workerRunCmd.Flags().String("grpc-server-addr", "127.0.0.1:9090", "ADP worker gRPC server address")
	workerRunCmd.Flags().String("worker-token", "", "Worker shared secret token (required)")
	workerRunCmd.Flags().String("worker-token-file", "", "Path to a 0600 file containing the worker token")
	workerRunCmd.Flags().String("worker-name", "worker-1", "Worker name")
	workerRunCmd.Flags().String("worker-type", "shell", "Worker type")
	workerRunCmd.Flags().Duration("poll-interval", 5*time.Second, "Job poll interval")
	workerRunCmd.Flags().Duration("exec-timeout", 30*time.Second, "Command execution timeout")
	workerRunCmd.Flags().Duration("host-collect-interval", 60*time.Second, "Host info collection interval")
	workerRunCmd.Flags().Bool("log-to-db", false, "Send execution logs to server database")
	workerRunCmd.Flags().String("services-config", config.DefaultServicesConfigPath, "Worker-local services.cnf path")

	workerCmd.AddCommand(workerRunCmd)
	rootCmd.AddCommand(workerCmd)
}

func runWorkerAsSubcommand(cmd *cobra.Command, _ []string) error {
	serverURL, _ := cmd.Flags().GetString("server-url")
	grpcServerAddr, _ := cmd.Flags().GetString("grpc-server-addr")
	workerToken, _ := cmd.Flags().GetString("worker-token")
	workerTokenFile, _ := cmd.Flags().GetString("worker-token-file")
	if strings.TrimSpace(workerTokenFile) != "" {
		contents, err := os.ReadFile(workerTokenFile)
		if err != nil {
			return fmt.Errorf("read worker token file: %w", err)
		}
		workerToken = strings.TrimSpace(string(contents))
	}
	if unsafeSecretValue(workerToken) {
		return errors.New("worker-token is required; supply it through a protected runtime secret")
	}
	workerName, _ := cmd.Flags().GetString("worker-name")
	workerType, _ := cmd.Flags().GetString("worker-type")
	pollInterval, _ := cmd.Flags().GetDuration("poll-interval")
	execTimeout, _ := cmd.Flags().GetDuration("exec-timeout")
	hostCollectInterval, _ := cmd.Flags().GetDuration("host-collect-interval")
	logToDB, _ := cmd.Flags().GetBool("log-to-db")
	servicesConfig, _ := cmd.Flags().GetString("services-config")

	client := worker.NewAgent(serverURL, workerToken, workerName, workerType, pollInterval)
	client.SetGRPCServerAddr(grpcServerAddr)
	client.SetExecTimeout(execTimeout)
	client.SetHostCollectInterval(hostCollectInterval)
	client.SetLogToDB(logToDB)
	client.SetServicesConfigPath(servicesConfig)

	return client.Run()
}

// Execute runs the root command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func runServe(cmd *cobra.Command, _ []string) error {
	// Load config file if specified.
	// 参数优先
	configPath, _ := cmd.Flags().GetString("config")

	// viper配置文件
	if configPath != "" {
		viper.SetConfigFile(configPath)
		if err := viper.ReadInConfig(); err != nil {
			return fmt.Errorf("read config file %s: %w", configPath, err)
		}
		log.Printf("loaded config from %s", configPath)
	} else if err := viper.ReadInConfig(); err == nil {
		log.Printf("loaded config from %s", viper.ConfigFileUsed())
	} else {
		var notFound viper.ConfigFileNotFoundError
		if !errors.As(err, &notFound) {
			return fmt.Errorf("read config file: %w", err)
		}
	}

	cfg := config.ServerConfig{
		Addr:                  viper.GetString("addr"),
		WorkerGRPCAddr:        viper.GetString("worker.grpc_addr"),
		DBDSN:                 viper.GetString("db.dsn"),
		AdminUsername:         viper.GetString("auth.admin_username"),
		AdminPassword:         viper.GetString("auth.admin_password"),
		AuthSecret:            viper.GetString("auth.secret"),
		WorkerSharedToken:     viper.GetString("auth.worker_token"),
		LLMBaseURL:            viper.GetString("agent.base_url"),
		LLMAPIKey:             viper.GetString("agent.api_key"),
		LLMModel:              viper.GetString("agent.model"),
		AgentMaxSteps:         viper.GetInt("agent.max_steps"),
		ManagedConfigDir:      viper.GetString("managed_config_dir"),
		ManagedConfigSyncMode: viper.GetString("managed_config_sync_mode"),
	}
	if err := validateRuntimeConfig(cfg); err != nil {
		return err
	}

	// Initialize repository.
	var repo db.Repository
	if cfg.DBDSN != "" {
		pgRepo, err := db.NewPostgresRepository(cfg.DBDSN)
		if err != nil {
			return fmt.Errorf("connect to database: %w", err)
		}
		defer pgRepo.Close() //nolint:errcheck
		repo = pgRepo
		log.Printf("connected to PostgreSQL database")
	} else {
		log.Printf("no DB DSN configured, using in-memory repository")
		// In-memory fallback: created inside NewServer.
	}

	// Initialize auth service and optionally link to database.
	authService := auth.NewService(cfg.AdminUsername, cfg.AdminPassword, cfg.AuthSecret)
	if repo != nil {
		authService.SetUserStore(repo)
		if err := authService.SeedUserStore(); err != nil {
			log.Printf("WARNING: failed to seed admin user to database: %v", err)
		}
	}

	// Create the HTTP server using the existing API.
	svr := api.NewServer(api.Config{
		Addr:                  cfg.Addr,
		AdminUsername:         cfg.AdminUsername,
		AdminPassword:         cfg.AdminPassword,
		AuthSecret:            cfg.AuthSecret,
		WorkerSharedToken:     cfg.WorkerSharedToken,
		LLMBaseURL:            cfg.LLMBaseURL,
		LLMAPIKey:             cfg.LLMAPIKey,
		LLMModel:              cfg.LLMModel,
		AgentMaxSteps:         cfg.AgentMaxSteps,
		ManagedConfigDir:      cfg.ManagedConfigDir,
		ManagedConfigSyncMode: cfg.ManagedConfigSyncMode,
	}, repo, authService)

	grpcListener, err := net.Listen("tcp", cfg.WorkerGRPCAddr)
	if err != nil {
		return fmt.Errorf("listen worker grpc %s: %w", cfg.WorkerGRPCAddr, err)
	}
	grpcServer := grpc.NewServer()
	adpv1.RegisterWorkerServiceServer(grpcServer, workerstream.NewService(svr.Repository(), cfg.WorkerSharedToken, svr.WorkerHub()))

	go func() {
		log.Printf("ADP server listening on %s", cfg.Addr)
		if err := svr.Start(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server start failed: %v", err)
		}
	}()
	go func() {
		log.Printf("ADP worker gRPC listening on %s", cfg.WorkerGRPCAddr)
		if err := grpcServer.Serve(grpcListener); err != nil {
			log.Fatalf("worker grpc start failed: %v", err)
		}
	}()

	// Graceful shutdown.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if repo != nil {
		if err := repo.Ping(); err == nil {
			log.Printf("database connection healthy, shutting down")
		}
	}

	// Close the database.
	if closer, ok := repo.(interface{ Close() error }); ok {
		_ = closer.Close()
	}

	grpcServer.GracefulStop()
	return svr.Shutdown(ctx)
}

func validateRuntimeConfig(cfg config.ServerConfig) error {
	missing := make([]string, 0, 3)
	if unsafeSecretValue(cfg.AdminPassword) {
		missing = append(missing, "ADP_AUTH_ADMIN_PASSWORD")
	}
	if unsafeSecretValue(cfg.AuthSecret) {
		missing = append(missing, "ADP_AUTH_SECRET")
	}
	if unsafeSecretValue(cfg.WorkerSharedToken) {
		missing = append(missing, "ADP_AUTH_WORKER_TOKEN")
	}
	if len(missing) > 0 {
		return fmt.Errorf("required runtime secrets are missing: %s", strings.Join(missing, ", "))
	}
	if strings.EqualFold(os.Getenv("ADP_ENV"), "production") && strings.TrimSpace(cfg.DBDSN) == "" {
		return errors.New("ADP_DB_DSN is required when ADP_ENV=production")
	}
	if mode := strings.TrimSpace(cfg.ManagedConfigSyncMode); mode != "" && mode != "missing" && mode != "enforce" {
		return fmt.Errorf("managed config sync mode must be missing or enforce, got %q", mode)
	}
	return nil
}

func unsafeSecretValue(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return value == "" || strings.Contains(value, "<set-in-secret-manager>") || strings.Contains(value, "change-me")
}

// sqlDB is imported to satisfy the interface check; the actual DB handle is inside PostgresRepository.
var _ = (*sql.DB)(nil)
