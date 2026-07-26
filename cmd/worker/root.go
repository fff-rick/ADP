package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"adp/internal/config"
	"adp/internal/infrastructure/worker"
)

var workerRootCmd = &cobra.Command{
	Use:   "adp-worker",
	Short: "ADP Worker Agent",
	Long:  `ADP Worker polls the ADP Server for jobs, executes shell commands, and reports results.`,
}

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Start the ADP worker",
	RunE:  runWorker,
}

func init() {
	runCmd.Flags().String("server-url", "http://127.0.0.1:8080", "ADP server URL")
	runCmd.Flags().String("grpc-server-addr", "127.0.0.1:9090", "ADP worker gRPC server address")
	runCmd.Flags().String("worker-token", "", "Worker shared secret token (required)")
	runCmd.Flags().String("worker-token-file", "", "Path to a 0600 file containing the worker token")
	runCmd.Flags().String("worker-name", "worker-1", "Worker name")
	runCmd.Flags().String("worker-type", "shell", "Worker type")
	runCmd.Flags().Duration("poll-interval", 5*time.Second, "Job poll interval")
	runCmd.Flags().Duration("exec-timeout", 30*time.Second, "Command execution timeout")
	runCmd.Flags().Duration("host-collect-interval", 60*time.Second, "Host info collection interval")
	runCmd.Flags().Bool("log-to-db", false, "Send execution logs to server database")
	runCmd.Flags().String("services-config", config.DefaultServicesConfigPath, "Worker-local services.cnf path")
	runCmd.Flags().String("config", "", "Path to YAML config file")

	_ = viper.BindPFlag("server_url", runCmd.Flags().Lookup("server-url"))
	_ = viper.BindPFlag("grpc_server_addr", runCmd.Flags().Lookup("grpc-server-addr"))
	_ = viper.BindPFlag("worker_token", runCmd.Flags().Lookup("worker-token"))
	_ = viper.BindPFlag("worker_token_file", runCmd.Flags().Lookup("worker-token-file"))
	_ = viper.BindPFlag("worker_name", runCmd.Flags().Lookup("worker-name"))
	_ = viper.BindPFlag("worker_type", runCmd.Flags().Lookup("worker-type"))
	_ = viper.BindPFlag("poll_interval", runCmd.Flags().Lookup("poll-interval"))
	_ = viper.BindPFlag("exec_timeout", runCmd.Flags().Lookup("exec-timeout"))
	_ = viper.BindPFlag("host_collect_interval", runCmd.Flags().Lookup("host-collect-interval"))
	_ = viper.BindPFlag("log_to_db", runCmd.Flags().Lookup("log-to-db"))
	_ = viper.BindPFlag("services_config", runCmd.Flags().Lookup("services-config"))

	viper.SetEnvPrefix("ADP")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))
	viper.AutomaticEnv()
	viper.SetConfigName("adp")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")
	viper.AddConfigPath("configs/worker/")
	viper.AddConfigPath("/etc/adp/")
	viper.AddConfigPath("/etc/adp/worker/")

	workerRootCmd.AddCommand(runCmd)
}

func Execute() {
	if err := workerRootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func runWorker(cmd *cobra.Command, _ []string) error {
	configPath, _ := cmd.Flags().GetString("config")
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

	cfg := config.WorkerConfig{
		ServerURL:           viper.GetString("server_url"),
		GRPCServerAddr:      viper.GetString("grpc_server_addr"),
		WorkerToken:         viper.GetString("worker_token"),
		Name:                viper.GetString("worker_name"),
		Type:                viper.GetString("worker_type"),
		PollInterval:        viper.GetDuration("poll_interval"),
		ExecTimeout:         viper.GetDuration("exec_timeout"),
		HostCollectInterval: viper.GetDuration("host_collect_interval"),
		LogToDB:             viper.GetBool("log_to_db"),
		ServicesConfigPath:  viper.GetString("services_config"),
	}
	if tokenFile := strings.TrimSpace(viper.GetString("worker_token_file")); tokenFile != "" {
		contents, err := os.ReadFile(tokenFile)
		if err != nil {
			return fmt.Errorf("read worker token file: %w", err)
		}
		cfg.WorkerToken = strings.TrimSpace(string(contents))
	}
	if unsafeWorkerToken(cfg.WorkerToken) {
		return errors.New("ADP_WORKER_TOKEN is required; supply it through a protected runtime secret")
	}

	client := worker.NewAgent(cfg.ServerURL, cfg.WorkerToken, cfg.Name, cfg.Type, cfg.PollInterval)
	client.SetGRPCServerAddr(cfg.GRPCServerAddr)
	client.SetExecTimeout(cfg.ExecTimeout)
	client.SetHostCollectInterval(cfg.HostCollectInterval)
	client.SetLogToDB(cfg.LogToDB)
	client.SetServicesConfigPath(cfg.ServicesConfigPath)

	if err := client.Run(); err != nil {
		log.Fatalf("worker failed: %v", err)
	}
	return nil
}

func unsafeWorkerToken(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return value == "" || strings.Contains(value, "<set-in-secret-manager>") || strings.Contains(value, "change-me")
}
