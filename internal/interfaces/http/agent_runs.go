package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"adp/internal/application/agent"
	"adp/internal/domain/model"
	"adp/internal/infrastructure/llm"
)

const agentPromptVersion = "controlled-agent-v1"
const agentPolicyVersion = "default-v1"

func (s *Server) createPersistentRun(input, conversationID string, history []llm.Message) (model.AgentRun, error) {
	messages := []llm.Message{{Role: "system", Content: agent.SystemPrompt}}
	messages = append(messages, history...)
	messages = append(messages, llm.Message{Role: "user", Content: input})
	transcript, _ := json.Marshal(messages)
	return s.repo.CreateAgentRun(model.AgentRun{Input: model.SanitizeText(input), ConversationID: conversationID, Status: model.AgentRunStatusQueued, TraceID: fmt.Sprintf("trace-%d", time.Now().UnixNano()), PolicyVersion: agentPolicyVersion, PromptVersion: agentPromptVersion, Transcript: transcript, NextStep: 1})
}

func (s *Server) executePersistentRun(ctx context.Context, runID string) (model.AgentRun, []agent.Event, error) {
	run, err := s.repo.GetAgentRun(runID)
	if err != nil {
		return model.AgentRun{}, nil, err
	}
	if run.Status == model.AgentRunStatusCancelled || run.Status == model.AgentRunStatusCompleted {
		return run, nil, nil
	}
	var transcript []llm.Message
	if err := json.Unmarshal(run.Transcript, &transcript); err != nil {
		return run, nil, fmt.Errorf("decode run transcript: %w", err)
	}
	run.Status = model.AgentRunStatusRunning
	if err := s.repo.UpdateAgentRun(run); err != nil {
		return run, nil, err
	}
	ctx = agent.WithRunID(ctx, run.ID)
	persist := func() error {
		encoded, _ := json.Marshal(transcript)
		run.Transcript = encoded
		return s.repo.UpdateAgentRun(run)
	}
	observer := agent.Observer{
		OnModelComplete: func(_ int, latency time.Duration, usage llm.Usage) error {
			s.agentMetrics.model(latency, usage)
			return nil
		},
		OnToolComplete: func(_ int, _ llm.ToolCall, latency time.Duration, err error) error {
			s.agentMetrics.tool(latency, err)
			return nil
		},
		OnAssistant: func(step int, message llm.Message) error {
			message.Content = model.SanitizeText(message.Content)
			transcript = append(transcript, message)
			_, err := s.repo.AddAgentEvent(model.AgentEvent{RunID: run.ID, Step: step, Type: "assistant", Data: map[string]any{"content": message.Content}})
			if err != nil {
				return err
			}
			return persist()
		},
		OnToolCall: func(step int, call llm.ToolCall) error {
			var arguments map[string]any
			_ = json.Unmarshal(call.Arguments, &arguments)
			safeArguments, _ := json.Marshal(model.SanitizeMap(arguments))
			return s.repo.CreateAgentToolCall(model.AgentToolCall{ID: run.ID + ":" + call.ID, RunID: run.ID, Step: step, ToolName: call.Name, Arguments: safeArguments, Status: "running"})
		},
		OnToolResult: func(step int, call llm.ToolCall, payload map[string]any) error {
			encoded, _ := json.Marshal(payload)
			transcript = append(transcript, llm.Message{Role: "tool", ToolCallID: call.ID, Content: string(encoded)})
			callErr, _ := payload["error"].(string)
			if err := s.repo.CompleteAgentToolCall(model.AgentToolCall{ID: run.ID + ":" + call.ID, Result: encoded, Error: callErr, Status: "completed"}); err != nil {
				return err
			}
			_, err := s.repo.AddAgentEvent(model.AgentEvent{RunID: run.ID, Step: step, Type: "tool", Name: call.Name, Data: payload})
			if err != nil {
				return err
			}
			return persist()
		},
		PauseAfterTool: func(_ llm.ToolCall, payload map[string]any) bool { return payloadNeedsApproval(payload) },
	}
	result, runErr := s.agentRuntime.RunMessages(ctx, transcript, run.NextStep, nil, observer)
	run.NextStep = result.Steps + 1
	if result.Paused {
		run.Status = model.AgentRunStatusWaitingApproval
	} else if runErr != nil {
		run.Error = runErr.Error()
		if errors.Is(runErr, context.DeadlineExceeded) {
			run.Status = model.AgentRunStatusTimedOut
		} else {
			run.Status = model.AgentRunStatusFailed
		}
	} else {
		run.Answer = result.Answer
		run.Status = model.AgentRunStatusCompleted
	}
	_ = persist()
	s.agentMetrics.complete(result.Steps, runErr == nil && !result.Paused)
	return run, result.Events, runErr
}

func payloadNeedsApproval(payload map[string]any) bool {
	result, _ := payload["result"].(map[string]any)
	jobs, _ := result["jobs"].([]map[string]any)
	for _, job := range jobs {
		status := fmt.Sprint(job["status"])
		if status == string(model.JobStatusWaitingApproval) {
			return true
		}
	}
	return false
}

func (s *Server) handleGetAgentRun(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/agent/runs/")
	if strings.HasSuffix(id, "/events") || strings.HasSuffix(id, "/cancel") {
		s.handleAgentRunActions(w, r)
		return
	}
	run, err := s.repo.GetAgentRun(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, run)
}
func (s *Server) handleAgentRunActions(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/agent/runs/")
	parts := strings.Split(path, "/")
	if len(parts) != 2 {
		writeError(w, http.StatusNotFound, fmt.Errorf("not found"))
		return
	}
	id := parts[0]
	if parts[1] == "cancel" && r.Method == http.MethodPost {
		run, err := s.repo.GetAgentRun(id)
		if err != nil {
			writeError(w, 404, err)
			return
		}
		if run.Status == model.AgentRunStatusRunning {
			writeError(w, 409, fmt.Errorf("run is active"))
			return
		}
		run.Status = model.AgentRunStatusCancelled
		_ = s.repo.UpdateAgentRun(run)
		_, _ = s.repo.AddAgentEvent(model.AgentEvent{RunID: id, Type: "cancelled", Data: map[string]any{}})
		writeJSON(w, 200, run)
		return
	}
	if parts[1] == "events" && r.Method == http.MethodGet {
		s.handleAgentEventsSSE(w, r, id)
		return
	}
	writeError(w, http.StatusNotFound, fmt.Errorf("not found"))
}
func (s *Server) handleAgentEventsSSE(w http.ResponseWriter, r *http.Request, id string) {
	if _, err := s.repo.GetAgentRun(id); err != nil {
		writeError(w, 404, err)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	after := int64(0)
	if raw := r.URL.Query().Get("after"); raw != "" {
		fmt.Sscan(raw, &after)
	}
	events, err := s.repo.ListAgentEvents(id, after)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	for _, event := range events {
		data, _ := json.Marshal(event)
		fmt.Fprintf(w, "id: %d\ndata: %s\n\n", event.ID, data)
	}
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}
