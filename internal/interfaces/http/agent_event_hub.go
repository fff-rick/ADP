package api

import (
	"sync"

	"adp/internal/application/agent"
)

// agentEventHub fans out ephemeral token deltas for active runs. Durable
// events remain in the repository, so reconnects first replay persisted data.
type agentEventHub struct {
	mu   sync.Mutex
	subs map[string]map[chan agent.Event]struct{}
}

func newAgentEventHub() *agentEventHub {
	return &agentEventHub{subs: make(map[string]map[chan agent.Event]struct{})}
}

func (h *agentEventHub) subscribe(runID string) (<-chan agent.Event, func()) {
	ch := make(chan agent.Event, 64)
	h.mu.Lock()
	if h.subs[runID] == nil {
		h.subs[runID] = make(map[chan agent.Event]struct{})
	}
	h.subs[runID][ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.subs[runID], ch)
		if len(h.subs[runID]) == 0 {
			delete(h.subs, runID)
		}
		h.mu.Unlock()
	}
}

func (h *agentEventHub) publish(runID string, event agent.Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs[runID] {
		select {
		case ch <- event:
		default: // A slow browser must not delay the controlled operation.
		}
	}
}
