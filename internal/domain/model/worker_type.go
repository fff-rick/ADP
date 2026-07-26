package model

import "strings"

// NormalizeWorkerType returns the canonical, case-insensitive worker type.
func NormalizeWorkerType(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

// WorkerCanRunType is the control-plane routing rule. A shell worker is the
// explicitly privileged general-purpose worker; every other worker can run
// only work declared for its own type.
func WorkerCanRunType(workerType, jobType string) bool {
	workerType = NormalizeWorkerType(workerType)
	jobType = NormalizeWorkerType(jobType)
	return workerType == "shell" || (workerType != "" && workerType == jobType)
}
