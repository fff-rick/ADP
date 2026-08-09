package workerstream

import (
	"log"
	"sync"

	adpv1 "adp/api/proto/adp/v1"
	"adp/internal/domain/model"
)

// Hub tracks active gRPC streams from workers.
type Hub struct {
	mu      sync.RWMutex
	workers map[string]chan *adpv1.ServerEnvelope
}

// NewHub creates a worker stream hub.
func NewHub() *Hub {
	return &Hub{workers: make(map[string]chan *adpv1.ServerEnvelope)}
}

func (h *Hub) register(workerID string) chan *adpv1.ServerEnvelope {
	ch := make(chan *adpv1.ServerEnvelope, 32)
	h.mu.Lock()
	if old, ok := h.workers[workerID]; ok {
		close(old)
	}
	h.workers[workerID] = ch
	h.mu.Unlock()
	log.Printf("grpc worker stream: worker %s connected", workerID)
	return ch
}

func (h *Hub) unregister(workerID string, ch chan *adpv1.ServerEnvelope) {
	h.mu.Lock()
	if current, ok := h.workers[workerID]; ok && current == ch {
		delete(h.workers, workerID)
		close(ch)
	}
	h.mu.Unlock()
	log.Printf("grpc worker stream: worker %s disconnected", workerID)
}

// PushJob sends a job to a connected worker.
func (h *Hub) PushJob(workerID string, job model.Job) bool {
	return h.push(workerID, &adpv1.ServerEnvelope{
		Payload: &adpv1.ServerEnvelope_Job{
			Job: jobToProto(job),
		},
	})
}

// PushCommand sends a control command to a connected worker.
func (h *Hub) PushCommand(workerID, command string) bool {
	return h.push(workerID, &adpv1.ServerEnvelope{
		Payload: &adpv1.ServerEnvelope_Command{
			Command: command,
		},
	})
}

// IsConnected returns whether a worker has an active stream.
func (h *Hub) IsConnected(workerID string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	_, ok := h.workers[workerID]
	return ok
}

func (h *Hub) push(workerID string, msg *adpv1.ServerEnvelope) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	ch, ok := h.workers[workerID]
	if !ok {
		return false
	}
	select {
	case ch <- msg:
		return true
	default:
		log.Printf("grpc worker stream: worker %s send buffer full", workerID)
		return false
	}
}

func jobToProto(job model.Job) *adpv1.Job {
	return &adpv1.Job{
		Id:           job.ID,
		Name:         job.Name,
		WorkerType:   job.WorkerType,
		Command:      job.Command,
		TemplateCode: job.TemplateCode,
		Parameters:   cloneStringMap(job.Parameters),
		SourceType:   job.SourceType,
	}
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
