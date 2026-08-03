package builtin

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"adp/internal/config"
	"adp/internal/domain/model"
	"adp/internal/module"
)

type MySQLBackup struct{}

func (m *MySQLBackup) Code() string               { return "mysql_backup" }
func (m *MySQLBackup) Name() string               { return "MySQL 备份" }
func (m *MySQLBackup) Description() string        { return "使用 mysqldump 备份 MySQL 数据库" }
func (m *MySQLBackup) ToolType() string           { return "mysql" }
func (m *MySQLBackup) RiskLevel() model.RiskLevel { return model.RiskLevelMedium }
func (m *MySQLBackup) RiskProfile() module.RiskProfile {
	return module.RiskProfile{Level: model.RiskLevelMedium, Reversible: true, ImpactScope: "single_host"}
}
func (m *MySQLBackup) Parameters() []module.ParamDef {
	return []module.ParamDef{
		{Name: "Database", Description: "数据库名", Required: true},
		{Name: "ServiceProfile", Description: "Worker services.cnf 中的 MySQL Profile", Required: true},
	}
}
func (m *MySQLBackup) Check(ctx module.ExecContext) (module.CheckResult, error) {
	// Backup is always needed.
	return module.CheckResult{NeedsChange: true, CurrentState: "no recent backup found"}, nil
}
func (m *MySQLBackup) Execute(ctx module.ExecContext) (module.Result, error) {
	db := ctx.Params["Database"]
	if ctx.Service == nil || ctx.Service.Type != "mysql" {
		return module.Result{Success: false}, fmt.Errorf("mysql_backup requires a Worker-local mysql ServiceProfile")
	}
	service := ctx.Service
	credentialsFile, err := writeMySQLDefaults(*service)
	if err != nil {
		return module.Result{Success: false}, err
	}
	defer os.Remove(credentialsFile) //nolint:errcheck // best-effort removal of temporary credentials
	backupFile, err := os.CreateTemp("", "adp-mysql-backup-*.sql")
	if err != nil {
		return module.Result{Success: false}, fmt.Errorf("create backup file: %w", err)
	}
	filename := backupFile.Name()
	if err := backupFile.Close(); err != nil {
		return module.Result{Success: false}, fmt.Errorf("close backup file: %w", err)
	}
	out, err := exec.Command("mysqldump", "--defaults-extra-file="+credentialsFile, "--result-file="+filename, db).CombinedOutput()
	if err != nil {
		return module.Result{Success: false, Output: string(out), Facts: map[string]string{"backup_file": filename}}, nil
	}
	return module.Result{Success: true, Output: string(out), Changed: true, Facts: map[string]string{"backup_file": filename}}, nil
}

func writeMySQLDefaults(service config.RuntimeServiceProfile) (string, error) {
	if service.User == "" || service.Password == "" {
		return "", fmt.Errorf("mysql ServiceProfile %q requires user and password", service.Name)
	}
	file, err := os.CreateTemp("", "adp-mysql-*.cnf")
	if err != nil {
		return "", err
	}
	path := file.Name()
	defer file.Close() //nolint:errcheck // temporary file cleanup
	if err := file.Chmod(0o600); err != nil {
		return "", err
	}
	content := fmt.Sprintf("[client]\nhost=%s\nport=%s\nuser=%s\npassword=%s\n", service.Host, service.Port, service.User, service.Password)
	if _, err := file.WriteString(content); err != nil {
		return "", err
	}
	return filepath.Clean(path), nil
}
func (m *MySQLBackup) DryRun(ctx module.ExecContext) (module.Result, error) {
	db := paramDefault(ctx.Params, "Database", "???")
	return module.Result{Success: true, Output: fmt.Sprintf("would backup database %s with mysqldump", db), Changed: true}, nil
}
