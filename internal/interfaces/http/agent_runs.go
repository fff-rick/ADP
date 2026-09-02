package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"adp/internal/application/agent"
	"adp/internal/domain/model"
	"adp/internal/infrastructure/llm"
)

const agentPromptVersion = "controlled-agent-v2"
const agentPolicyVersion = "default-v1"

func (s *Server) createPersistentRun(input, conversationID string, history []llm.Message) (model.AgentRun, error) {
	input = model.SanitizeText(input)
	messages := []llm.Message{{Role: "system", Content: agent.SystemPrompt}}
	messages = append(messages, history...)
	messages = append(messages, llm.Message{Role: "user", Content: input})
	transcript, _ := json.Marshal(messages)
	return s.repo.CreateAgentRun(model.AgentRun{Input: model.SanitizeText(input), ConversationID: conversationID, Status: model.AgentRunStatusQueued, TraceID: fmt.Sprintf("trace-%d", time.Now().UnixNano()), PolicyVersion: agentPolicyVersion, PromptVersion: agentPromptVersion, Transcript: transcript, NextStep: 1})
}

func (s *Server) executePersistentRun(ctx context.Context, runID string, stream chan<- agent.Event) (model.AgentRun, []agent.Event, error) {
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
	for i := range transcript {
		transcript[i].Content = model.SanitizeText(transcript[i].Content)
	}
	// Upgrade legacy runs in place before resuming them so a credential that was
	// stored by an earlier version cannot be sent to the provider again.
	run.Transcript, _ = json.Marshal(transcript)
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
		BeforeModel: func(step int, messages []llm.Message, definitions []llm.ToolDefinition, tokenEstimate, budgetTokens int) error {
			request := llm.CompletionRequest{Messages: messages, Tools: definitions}
			encoded, err := json.Marshal(request)
			if err != nil {
				return fmt.Errorf("encode agent context snapshot: %w", err)
			}
			digest := sha256.Sum256(encoded)
			_, err = s.repo.CreateAgentContextSnapshot(model.AgentContextSnapshot{
				RunID: run.ID, Step: step, TranscriptVersion: len(messages), TokenEstimate: tokenEstimate, BudgetTokens: budgetTokens,
				Decisions: map[string]any{"phase": "phase0", "compaction": "disabled", "sanitized": true, "over_budget": budgetTokens > 0 && tokenEstimate > budgetTokens},
				Messages:  encoded, ContentSHA256: fmt.Sprintf("%x", digest),
			})
			s.agentMetrics.context(tokenEstimate, budgetTokens, err)
			return err
		},
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
	result, runErr := s.agentRuntime.RunMessages(ctx, transcript, run.NextStep, stream, observer)
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
		caseEvents := result.Events
		if persistedEvents, err := s.repo.ListAgentEvents(run.ID, 0); err == nil {
			caseEvents = make([]agent.Event, 0, len(persistedEvents))
			for _, event := range persistedEvents {
				caseEvents = append(caseEvents, agent.Event{Step: event.Step, Type: event.Type, Name: event.Name, Data: event.Data})
			}
		}
		if incidentCase := incidentCaseCandidate(run, caseEvents); incidentCase != nil {
			if _, err := s.repo.UpsertIncidentCase(run.ID, *incidentCase); err != nil {
				return run, result.Events, fmt.Errorf("create incident case candidate: %w", err)
			}
		}
	}
	_ = persist()
	s.agentMetrics.complete(result.Steps, runErr == nil && !result.Paused)
	return run, result.Events, runErr
}

// executePersistentRunWithEvents keeps approval-resumed runs observable after
// the original POST stream has ended. It forwards token deltas to subscribers;
// durable assistant/tool events are still written by executePersistentRun.
func (s *Server) executePersistentRunWithEvents(ctx context.Context, runID string) (model.AgentRun, []agent.Event, error) {
	stream := make(chan agent.Event, 64)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for event := range stream {
			s.agentEvents.publish(runID, event)
		}
	}()
	run, events, err := s.executePersistentRun(ctx, runID, stream)
	close(stream)
	<-done
	return run, events, err
}

// incidentCaseCandidate creates a review-only record from a completed run.
// Its evidence is derived from bounded, redacted tool output; the agent's
// narrative remains untrusted until an administrator approves the case.
func incidentCaseCandidate(run model.AgentRun, events []agent.Event) *model.IncidentCase {
	var evidence []string
	for _, event := range events {
		if event.Type != "tool" || (event.Name != "get_worker_facts" && event.Name != "get_job_result") {
			continue
		}
		encoded, _ := json.Marshal(event.Data)
		text := model.SanitizeText(string(encoded))
		if len(text) > 800 {
			text = text[:800] + "…[truncated]"
		}
		evidence = append(evidence, text)
	}
	if len(evidence) == 0 {
		return nil
	}
	title := strings.TrimSpace(run.Input)
	if len(title) > 160 {
		title = title[:160] + "…"
	}
	return &model.IncidentCase{
		Title: title, Summary: model.SanitizeText(run.Answer), EvidenceSummary: strings.Join(evidence, "\n"),
		Status: model.IncidentCaseStatusPendingReview, SourceRunID: run.ID,
	}
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
	if strings.HasSuffix(id, "/events") || strings.HasSuffix(id, "/cancel") || strings.HasSuffix(id, "/context") {
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
	if parts[1] == "context" && r.Method == http.MethodGet {
		s.handleAgentContextSnapshots(w, r, id)
		return
	}
	writeError(w, http.StatusNotFound, fmt.Errorf("not found"))
}

// handleAgentContextSnapshots exposes only the sanitized prompt-projection
// metadata. The full snapshot messages remain an audit-store concern and are
// intentionally not returned by this general operator API.
func (s *Server) handleAgentContextSnapshots(w http.ResponseWriter, _ *http.Request, id string) {
	if _, err := s.repo.GetAgentRun(id); err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	snapshots, err := s.repo.ListAgentContextSnapshots(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"run_id": id, "snapshots": snapshots})
}
func (s *Server) handleAgentEventsSSE(w http.ResponseWriter, r *http.Request, id string) {
	if _, err := s.repo.GetAgentRun(id); err != nil {
		writeError(w, 404, err)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("streaming not supported"))
		return
	}
	after := int64(0)
	live := r.URL.Query().Get("live") == "1"
	if raw := r.URL.Query().Get("after"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed < 0 {
			writeError(w, http.StatusBadRequest, fmt.Errorf("after must be a non-negative event ID"))
			return
		}
		after = parsed
	}
	events, err := s.repo.ListAgentEvents(id, after)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	for _, event := range events {
		data, _ := json.Marshal(event)
		if _, err := fmt.Fprintf(w, "id: %d\ndata: %s\n\n", event.ID, data); err != nil {
			return
		}
	}
	flusher.Flush()
	if !live {
		return
	}

	liveEvents, unsubscribe := s.agentEvents.subscribe(id)
	defer unsubscribe()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case event := <-liveEvents:
			data, _ := json.Marshal(event)
			if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
				return
			}
			flusher.Flush()
		case <-ticker.C:
			run, runErr := s.repo.GetAgentRun(id)
			if runErr != nil {
				return
			}
			if run.Status == model.AgentRunStatusCompleted || run.Status == model.AgentRunStatusFailed || run.Status == model.AgentRunStatusCancelled || run.Status == model.AgentRunStatusTimedOut {
				data, _ := json.Marshal(map[string]any{"type": "done", "data": map[string]any{"run": run, "answer": run.Answer, "error": run.Error}})
				_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
				flusher.Flush()
				return
			}
		case <-r.Context().Done():
			return
		}
	}
}
