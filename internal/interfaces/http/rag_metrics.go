package api

import (
	"sync"
	"time"
)

// ragRuntimeMetrics covers generation work done by this server process. Queue
// state remains durable in PostgreSQL and is reported separately.
type ragRuntimeMetrics struct {
	mu       sync.RWMutex
	calls    int
	failures int
	latency  time.Duration
}

func (m *ragRuntimeMetrics) complete(latency time.Duration, failed bool) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	m.latency += latency
	if failed {
		m.failures++
	}
}

func (m *ragRuntimeMetrics) snapshot() (calls, failures int, avgLatencySeconds float64) {
	if m == nil {
		return 0, 0, 0
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.calls > 0 {
		avgLatencySeconds = m.latency.Seconds() / float64(m.calls)
	}
	return m.calls, m.failures, avgLatencySeconds
}

func (m *ragRuntimeMetrics) dashboard() map[string]float64 {
	calls, failures, avg := m.snapshot()
	return map[string]float64{"generation_calls": float64(calls), "generation_failures": float64(failures), "generation_latency_seconds": avg}
}
