package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"adp/internal/application/agent"
	"adp/internal/domain/model"
	"adp/internal/infrastructure/llm"
	"adp/internal/module"
)

type agentRunRequest struct {
	Input          string `json:"input"`
	ConversationID string `json:"conversation_id,omitempty"`
	Stream         bool   `json:"stream,omitempty"`
}

func (s *Server) handleAgentRun(w http.ResponseWriter, r *http.Request) {
	var req agentRunRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.Stream {
		s.handleAgentRunStream(w, r, req)
		return
	}

	// Load or create conversation.
	var convID string
	var history []llm.Message
	if req.ConversationID != "" {
		convID = req.ConversationID
		msgs, err := s.repo.ListConversationMessages(convID)
		if err == nil {
			history = conversationMessagesToLLM(msgs)
		}
	} else {
		title := req.Input
		if len(title) > 50 {
			title = title[:50]
		}
		conv, err := s.repo.CreateConversation(title)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		convID = conv.ID
	}

	// Persist user message.
	_ = s.repo.AddConversationMessage(model.ConversationMessage{
		ConversationID: convID, Role: "user", Content: req.Input,
	})

	// Run Agent with history.
	result, err := s.agentRuntime.Run(r.Context(), req.Input, history)
	if err != nil {
		// Still persist the error as an assistant message.
		_ = s.repo.AddConversationMessage(model.ConversationMessage{
			ConversationID: convID, Role: "assistant", Content: err.Error(),
		})
		writeError(w, http.StatusUnprocessableEntity, err)
		return
	}

	// Persist Agent events as conversation messages.
	for _, ev := range result.Events {
		// Only persist tool calls; skip intermediate assistant thinking.
		if ev.Type != "tool" {
			continue
		}
		msg := model.ConversationMessage{
			ConversationID: convID,
			Role:           ev.Type,
			Step:           ev.Step,
			ToolName:       ev.Name,
		}
		msg.ToolData, _ = ev.Data.(map[string]any)
		_ = s.repo.AddConversationMessage(msg)
	}
	// Persist final answer. (Events loop above skips assistant, so always store it here.)
	if result.Answer != "" {
		_ = s.repo.AddConversationMessage(model.ConversationMessage{
			ConversationID: convID, Role: "assistant", Content: result.Answer, Step: result.Steps + 1,
		})
	}

	user := currentUser(r)
	s.recordAudit("user", user.Username, "agent.run.completed", "agent_run", "", map[string]any{"steps": result.Steps, "conversation_id": convID})

	// Return pending jobs explicitly so both JSON and SSE clients can render the
	// approval controls immediately, without having to infer them from tool logs.
	pendingApprovals := pendingApprovalsFromEvents(result.Events)

	resp := map[string]any{
		"answer":          result.Answer,
		"events":          result.Events,
		"steps":           result.Steps,
		"conversation_id": convID,
	}
	if len(pendingApprovals) > 0 {
		resp["pending_approvals"] = pendingApprovals
	}
	writeJSON(w, http.StatusOK, resp)
}

func pendingApprovalsFromEvents(events []agent.Event) []map[string]any {
	seen := make(map[string]struct{})
	pending := make([]map[string]any, 0)
	for _, event := range events {
		if event.Type != "tool" {
			continue
		}
		payload, ok := event.Data.(map[string]any)
		if !ok {
			continue
		}
		result, ok := payload["result"].(map[string]any)
		if !ok {
			continue
		}
		jobs, ok := result["jobs"].([]map[string]any)
		if !ok {
			continue
		}
		for _, job := range jobs {
			jobID, _ := job["job_id"].(string)
			approvalRequired, _ := job["approval_required"].(bool)
			status, _ := job["status"].(model.JobStatus)
			if status == "" {
				statusString, _ := job["status"].(string)
				status = model.JobStatus(statusString)
			}
			if jobID == "" || !approvalRequired || status != model.JobStatusWaitingApproval {
				continue
			}
			if _, exists := seen[jobID]; exists {
				continue
			}
			seen[jobID] = struct{}{}
			pending = append(pending, job)
		}
	}
	return pending
}

func conversationMessagesToLLM(msgs []model.ConversationMessage) []llm.Message {
	// Only include user and assistant text messages; skip tool messages
	// because they require tool_call_id linkage that is not preserved.
	// Also limit history to prevent context overflow.
	const maxHistory = 10
	if len(msgs) > maxHistory {
		msgs = msgs[len(msgs)-maxHistory:]
	}
	var out []llm.Message
	for _, m := range msgs {
		switch m.Role {
		case "user":
			out = append(out, llm.Message{Role: "user", Content: m.Content})
		case "assistant":
			if m.Content != "" {
				out = append(out, llm.Message{Role: "assistant", Content: m.Content})
			}
		}
	}
	return out
}

type agentTool struct {
	definition llm.ToolDefinition
	execute    func(context.Context, json.RawMessage) (any, error)
}

func (t agentTool) Definition() llm.ToolDefinition { return t.definition }
func (t agentTool) Execute(ctx context.Context, args json.RawMessage) (any, error) {
	return t.execute(ctx, args)
}

func (s *Server) agentTools() []agent.Tool {
	tools := []agent.Tool{
		agentTool{
			definition: llm.ToolDefinition{
				Name:        "get_worker_facts",
				Description: "Read the latest hostname, IP, CPU and storage facts reported by one or more Worker hosts.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"worker_id":  map[string]any{"type": "string", "description": "Single worker ID"},
						"worker_ids": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Multiple worker IDs to query at once"},
					},
					"additionalProperties": false,
				},
			},
			execute: s.getWorkerFacts,
		},
		agentTool{
			definition: llm.ToolDefinition{
				Name:        "list_workers",
				Description: "List all registered Workers and their current status. Use this to discover what Workers are available before calling get_worker_facts or dispatching operations.",
				Parameters:  emptySchema(),
			},
			execute: s.listWorkers,
		},
		agentTool{definition: llm.ToolDefinition{Name: "list_capabilities", Description: "List registered and policy-authorized ADP modules.", Parameters: emptySchema()}, execute: func(context.Context, json.RawMessage) (any, error) {
			var capabilities []map[string]any
			for _, mod := range s.moduleReg.List() {
				if s.policyEng.ValidateTemplate(mod.Code()) == nil {
					capabilities = append(capabilities, map[string]any{"code": mod.Code(), "name": mod.Name(), "description": mod.Description(), "worker_type": mod.ToolType(), "risk_level": mod.RiskLevel(), "parameters": mod.Parameters()})
				}
			}
			return capabilities, nil
		}},
		agentTool{
			definition: llm.ToolDefinition{
				Name:        "create_module_operation",
				Description: "Create an operation using a registered module on one or more workers. Each worker gets its own job copy. Never use commands or YAML.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"module_code": map[string]any{"type": "string"},
						"worker_id":   map[string]any{"type": "string", "description": "Single worker ID (use worker_ids for multiple)"},
						"worker_ids":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Multiple worker IDs. Each gets its own job copy."},
						"parameters":  map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
						"reason":      map[string]any{"type": "string"},
					},
					"required":             []string{"module_code", "parameters", "reason"},
					"additionalProperties": false,
				},
			},
			execute: s.createModuleOperation,
		},
		agentTool{
			definition: llm.ToolDefinition{
				Name:        "get_job_result",
				Description: "Read the status and output of one or more operation jobs created by this system.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"job_id":  map[string]any{"type": "string", "description": "Single job ID"},
						"job_ids": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Multiple job IDs to query at once"},
					},
					"additionalProperties": false,
				},
			},
			execute: s.getJobResult,
		},
	}
	if s.config.AgentAllowShell {
		tools = append(tools, agentTool{
			definition: llm.ToolDefinition{
				Name:        "execute_shell_command",
				Description: "Execute a shell command directly on one or more workers. The command's first word must not be in the blocked tools list. Commands matching high-risk keywords (restart, kill, delete, etc.) require human approval. Prefer create_module_operation when a registered module exists for the task. Always provide a clear reason.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"command":    map[string]any{"type": "string", "description": "Shell command to execute"},
						"worker_id":  map[string]any{"type": "string", "description": "Single worker ID"},
						"worker_ids": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Multiple worker IDs"},
						"reason":     map[string]any{"type": "string", "description": "Why this command is needed"},
					},
					"required":             []string{"command", "reason"},
					"additionalProperties": false,
				},
			},
			execute: s.executeShellCommand,
		})
	}
	return tools
}

func (s *Server) createModuleOperation(_ context.Context, raw json.RawMessage) (any, error) {
	var in struct {
		ModuleCode string            `json:"module_code"`
		WorkerID   string            `json:"worker_id"`
		WorkerIDs  []string          `json:"worker_ids"`
		Parameters map[string]string `json:"parameters"`
		Reason     string            `json:"reason"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	if in.ModuleCode == "" || in.Reason == "" {
		return nil, errors.New("module_code and reason are required")
	}
	workerIDs, err := collectIDs(in.WorkerID, in.WorkerIDs, "worker_id")
	if err != nil {
		return nil, err
	}
	if err := model.ValidateNoInlineSecrets(in.Parameters); err != nil {
		return nil, err
	}
	if err := model.ValidateServiceProfile(in.ModuleCode, in.Parameters); err != nil {
		return nil, err
	}
	// Validate module, policy, parameters once.
	mod, err := s.moduleReg.Get(in.ModuleCode)
	if err != nil {
		return nil, err
	}
	if err = s.policyEng.ValidateTemplate(in.ModuleCode); err != nil {
		return nil, err
	}
	for _, param := range mod.Parameters() {
		if in.Parameters[param.Name] == "" && param.Default != "" {
			in.Parameters[param.Name] = param.Default
		}
		if param.Required && in.Parameters[param.Name] == "" {
			return nil, fmt.Errorf("required module parameter missing: %s", param.Name)
		}
	}
	// Determine risk and approval once for the whole batch.
	// Render template command so the Worker gets a pre-built shell command.
	renderedCmd, cmdErr := mod.DryRun(module.ExecContext{Params: in.Parameters, Timeout: 0})
	if cmdErr != nil {
		return nil, fmt.Errorf("render template %s: %w", in.ModuleCode, cmdErr)
	}
	risk := s.policyEng.MergeRisk(mod.RiskLevel())
	approval := s.policyEng.RequiresManualApproval(risk)
	status := model.JobStatusPending
	approvalStatus := model.ApprovalStatusNotRequired
	if approval {
		status = model.JobStatusWaitingApproval
		approvalStatus = model.ApprovalStatusPending
	}
	// Validate worker types; collect compatible workers and skip incompatible ones.
	var compatible []string
	var validationErrors []map[string]any
	for _, wid := range workerIDs {
		worker, werr := s.repo.GetWorker(wid)
		if werr != nil {
			validationErrors = append(validationErrors, map[string]any{"worker_id": wid, "error": werr.Error()})
			continue
		}
		if !model.WorkerCanRunType(worker.WorkerType, mod.ToolType()) {
			validationErrors = append(validationErrors, map[string]any{
				"worker_id":     wid,
				"worker_type":   worker.WorkerType,
				"required_type": mod.ToolType(),
				"error":         fmt.Sprintf("worker %s type %s cannot run module %s (requires %s)", wid, worker.WorkerType, in.ModuleCode, mod.ToolType()),
			})
			continue
		}
		compatible = append(compatible, wid)
	}
	if len(compatible) == 0 {
		return nil, fmt.Errorf("no compatible workers for module %q (type %s): %d validation errors", in.ModuleCode, mod.ToolType(), len(validationErrors))
	}
	// Create and dispatch a job copy for each compatible worker.
	var jobs []map[string]any
	for _, wid := range compatible {
		jobName := fmt.Sprintf("[agent][worker:%s] %s", wid, in.Reason)
		job := model.Job{
			Name:             jobName,
			WorkerType:       mod.ToolType(),
			Command:          renderedCmd.Output,
			Status:           status,
			RiskLevel:        risk,
			ApprovalRequired: approval,
			ApprovalStatus:   approvalStatus,
			TemplateCode:     in.ModuleCode,
			Parameters:       cloneStringMap(in.Parameters),
			SourceType:       "agent",
			AssignedWorkerID: wid,
		}
		created, cerr := s.repo.CreateJob(job)
		if cerr != nil {
			jobs = append(jobs, map[string]any{"worker_id": wid, "error": cerr.Error()})
			continue
		}
		entry := map[string]any{
			"job_id":            created.ID,
			"worker_id":         wid,
			"status":            created.Status,
			"approval_required": approval,
			"module_code":       in.ModuleCode,
		}
		if !approval {
			dispatched, derr := s.dispatchJobToWorker(created.ID, wid)
			if derr != nil {
				jobs = append(jobs, map[string]any{"worker_id": wid, "error": derr.Error()})
				continue
			}
			s.workerHub.PushJob(wid, dispatched)
			entry["status"] = dispatched.Status
		}
		jobs = append(jobs, entry)
	}
	dispatchedCount := 0
	for _, j := range jobs {
		if j["error"] == nil && !approval {
			dispatchedCount++
		}
	}
	return map[string]any{
		"jobs":              jobs,
		"total":             len(jobs),
		"summary":           map[string]any{"dispatched": dispatchedCount, "failed_validation": len(validationErrors), "approval_required": approval},
		"validation_errors": validationErrors,
	}, nil
}

func (s *Server) listWorkers(_ context.Context, _ json.RawMessage) (any, error) {
	workers, err := s.repo.ListWorkers()
	if err != nil {
		return nil, err
	}
	results := make([]map[string]any, 0, len(workers))
	for _, w := range workers {
		results = append(results, map[string]any{
			"id": w.ID, "name": w.Name, "type": w.WorkerType,
			"status": w.Status, "last_heartbeat_at": w.LastHeartbeatAt,
		})
	}
	return map[string]any{"workers": results, "total": len(results)}, nil
}

func (s *Server) getWorkerFacts(_ context.Context, raw json.RawMessage) (any, error) {
	var in struct {
		WorkerID  string   `json:"worker_id"`
		WorkerIDs []string `json:"worker_ids"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	ids, err := collectIDs(in.WorkerID, in.WorkerIDs, "worker_id")
	if err != nil {
		return nil, err
	}
	results := make([]map[string]any, 0, len(ids))
	for _, wid := range ids {
		worker, werr := s.repo.GetWorker(wid)
		if werr != nil {
			results = append(results, map[string]any{"worker_id": wid, "error": werr.Error()})
			continue
		}
		results = append(results, map[string]any{
			"worker_id":         wid,
			"name":              worker.Name,
			"type":              worker.WorkerType,
			"status":            worker.Status,
			"last_heartbeat_at": worker.LastHeartbeatAt,
			"host_info":         worker.HostInfo,
		})
	}
	return map[string]any{"workers": results, "total": len(results)}, nil
}

func (s *Server) getJobResult(_ context.Context, raw json.RawMessage) (any, error) {
	var in struct {
		JobID  string   `json:"job_id"`
		JobIDs []string `json:"job_ids"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	ids, err := collectIDs(in.JobID, in.JobIDs, "job_id")
	if err != nil {
		return nil, err
	}
	var succeeded, failed, pending, running int
	results := make([]map[string]any, 0, len(ids))
	for _, jid := range ids {
		job, jerr := s.repo.GetJob(jid)
		if jerr != nil {
			results = append(results, map[string]any{"job_id": jid, "error": jerr.Error()})
			failed++
			continue
		}
		success := job.Status == model.JobStatusSuccess
		entry := map[string]any{
			"job_id":    job.ID,
			"worker_id": job.AssignedWorkerID,
			"status":    job.Status,
			"success":   success,
			"output":    truncateAgentOutput(job.Output),
		}
		switch job.Status {
		case model.JobStatusSuccess:
			succeeded++
		case model.JobStatusFailed, model.JobStatusCancelled:
			failed++
		case model.JobStatusPending, model.JobStatusQueued, model.JobStatusWaitingApproval:
			pending++
		case model.JobStatusRunning:
			running++
		}
		results = append(results, entry)
	}
	return map[string]any{
		"results": results,
		"total":   len(results),
		"summary": map[string]int{"succeeded": succeeded, "failed": failed, "pending": pending, "running": running},
	}, nil
}

func (s *Server) executeShellCommand(_ context.Context, raw json.RawMessage) (any, error) {
	var in struct {
		Command   string   `json:"command"`
		WorkerID  string   `json:"worker_id"`
		WorkerIDs []string `json:"worker_ids"`
		Reason    string   `json:"reason"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	if in.Command == "" || in.Reason == "" {
		return nil, errors.New("command and reason are required")
	}
	workerIDs, err := collectIDs(in.WorkerID, in.WorkerIDs, "worker_id")
	if err != nil {
		return nil, err
	}
	// Validate command against blacklist.
	if err := s.policyEng.ValidateCommand(in.Command); err != nil {
		return nil, err
	}
	// Assess risk from keywords.
	risk := s.policyEng.AssessCommandRisk(in.Command)
	approval := s.policyEng.RequiresManualApproval(risk)
	status := model.JobStatusPending
	approvalStatus := model.ApprovalStatusNotRequired
	if approval {
		status = model.JobStatusWaitingApproval
		approvalStatus = model.ApprovalStatusPending
	}
	// Validate workers.
	var compatible []string
	var validationErrors []map[string]any
	for _, wid := range workerIDs {
		worker, werr := s.repo.GetWorker(wid)
		if werr != nil {
			validationErrors = append(validationErrors, map[string]any{"worker_id": wid, "error": werr.Error()})
			continue
		}
		if worker.WorkerType != "shell" {
			validationErrors = append(validationErrors, map[string]any{
				"worker_id": wid, "worker_type": worker.WorkerType,
				"error": "execute_shell_command requires shell-type workers",
			})
			continue
		}
		compatible = append(compatible, wid)
	}
	if len(compatible) == 0 {
		return nil, fmt.Errorf("no compatible shell workers: %d validation errors", len(validationErrors))
	}
	// Create and dispatch jobs.
	var jobs []map[string]any
	for _, wid := range compatible {
		jobName := fmt.Sprintf("[agent-shell][worker:%s] %s", wid, in.Reason)
		job := model.Job{
			Name:             jobName,
			WorkerType:       "shell",
			Command:          in.Command,
			Status:           status,
			RiskLevel:        risk,
			ApprovalRequired: approval,
			ApprovalStatus:   approvalStatus,
			SourceType:       "agent_shell",
			AssignedWorkerID: wid,
		}
		created, cerr := s.repo.CreateJob(job)
		if cerr != nil {
			jobs = append(jobs, map[string]any{"worker_id": wid, "error": cerr.Error()})
			continue
		}
		entry := map[string]any{
			"job_id":            created.ID,
			"worker_id":         wid,
			"status":            created.Status,
			"command":           in.Command,
			"approval_required": approval,
		}
		if !approval {
			dispatched, derr := s.dispatchJobToWorker(created.ID, wid)
			if derr != nil {
				jobs = append(jobs, map[string]any{"worker_id": wid, "error": derr.Error()})
				continue
			}
			s.workerHub.PushJob(wid, dispatched)
			entry["status"] = dispatched.Status
		}
		jobs = append(jobs, entry)
	}
	s.recordAudit("system", "agent", "agent.shell_command", "job", "", map[string]any{
		"command":    in.Command,
		"reason":     in.Reason,
		"risk":       risk,
		"approval":   approval,
		"worker_ids": compatible,
	})
	dispatchedCount := 0
	for _, j := range jobs {
		if j["error"] == nil && !approval {
			dispatchedCount++
		}
	}
	return map[string]any{
		"jobs":              jobs,
		"total":             len(jobs),
		"summary":           map[string]any{"dispatched": dispatchedCount, "failed_validation": len(validationErrors), "approval_required": approval, "risk_level": risk},
		"validation_errors": validationErrors,
	}, nil
}

// collectIDs merges a single ID string and an array of IDs, deduplicating the result.
func collectIDs(single string, plural []string, fieldName string) ([]string, error) {
	ids := plural
	if single != "" {
		ids = append([]string{single}, ids...)
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("at least one of %s or %ss is required", fieldName, fieldName)
	}
	seen := map[string]bool{}
	unique := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		unique = append(unique, id)
	}
	if len(unique) == 0 {
		return nil, fmt.Errorf("at least one of %s or %ss is required", fieldName, fieldName)
	}
	return unique, nil
}

func emptySchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}
}
func truncateAgentOutput(value string) string {
	if len(value) > 4000 {
		return value[:4000] + "..."
	}
	return value
}

// handleAgentRunStream runs the Agent in streaming mode (SSE).
func (s *Server) handleAgentRunStream(w http.ResponseWriter, r *http.Request, req agentRunRequest) {
	// Setup conversation (same as handleAgentRun).
	var convID string
	var history []llm.Message
	if req.ConversationID != "" {
		convID = req.ConversationID
		msgs, _ := s.repo.ListConversationMessages(convID)
		history = conversationMessagesToLLM(msgs)
	} else {
		title := req.Input
		if len(title) > 50 {
			title = title[:50]
		}
		conv, err := s.repo.CreateConversation(title)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		convID = conv.ID
	}
	_ = s.repo.AddConversationMessage(model.ConversationMessage{
		ConversationID: convID, Role: "user", Content: req.Input,
	})

	// SSE headers.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("streaming not supported"))
		return
	}

	stream := make(chan agent.Event, 32)
	done := make(chan struct{})
	go func() {
		defer close(stream)
		defer close(done)
		result, err := s.agentRuntime.RunStreaming(r.Context(), req.Input, history, stream)
		// Send final result as a special event.
		finalData := map[string]any{"steps": result.Steps}
		if err != nil {
			finalData["error"] = err.Error()
		}
		if result.Answer != "" {
			finalData["answer"] = result.Answer
		}
		pendingApprovals := pendingApprovalsFromEvents(result.Events)
		if len(pendingApprovals) > 0 {
			finalData["pending_approvals"] = pendingApprovals
		}
		finalData["conversation_id"] = convID

		// Persist BEFORE sending done so the frontend sees them on refresh.
		for _, ev := range result.Events {
			if ev.Type != "tool" {
				continue
			}
			msg := model.ConversationMessage{
				ConversationID: convID, Role: ev.Type, Step: ev.Step, ToolName: ev.Name,
			}
			msg.ToolData, _ = ev.Data.(map[string]any)
			_ = s.repo.AddConversationMessage(msg)
		}
		if result.Answer != "" {
			_ = s.repo.AddConversationMessage(model.ConversationMessage{
				ConversationID: convID, Role: "assistant", Content: result.Answer, Step: result.Steps + 1,
			})
		}

		// Send final event.
		finalJSON, _ := json.Marshal(finalData)
		stream <- agent.Event{Step: -1, Type: "done", Data: string(finalJSON)}
		user := currentUser(r)
		s.recordAudit("user", user.Username, "agent.run.completed", "agent_run", "", map[string]any{"steps": result.Steps, "conversation_id": convID})
	}()

	for ev := range stream {
		data, _ := json.Marshal(ev)
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}
	<-done
}
