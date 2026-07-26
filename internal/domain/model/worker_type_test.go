package model

import "testing"

func TestWorkerCanRunType(t *testing.T) {
	tests := []struct {
		workerType string
		jobType    string
		want       bool
	}{
		{"mysql", "mysql", true},
		{"mysql", "redis", false},
		{"redis", "mysql", false},
		{"shell", "mysql", true},
		{"shell", "redis", true},
		{"shell", "shell", true},
		{"", "mysql", false},
	}
	for _, tt := range tests {
		if got := WorkerCanRunType(tt.workerType, tt.jobType); got != tt.want {
			t.Errorf("WorkerCanRunType(%q, %q) = %t, want %t", tt.workerType, tt.jobType, got, tt.want)
		}
	}
}
