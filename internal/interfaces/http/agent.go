package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"adp/internal/application/agent"
	"adp/internal/domain/model"
	"adp/internal/infrastructure/llm"
)

type agentRunRequest struct {
	Input string `json:"input"`
}

func (s *Server) handleAgentRun(w http.ResponseWriter, r *http.Request) {
	var req agentRunRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	result, err := s.agentRuntime.Run(r.Context(), req.Input)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err)
		return
	}
	user := currentUser(r)
	s.recordAudit("user", user.Username, "agent.run.completed", "agent_run", "", map[string]any{"steps": result.Steps})
	writeJSON(w, http.StatusOK, result)
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
	return []agent.Tool{
		agentTool{definition: llm.ToolDefinition{Name: "get_worker_facts", Description: "Read the latest hostname, IP, CPU and storage facts reported by one Worker host.", Parameters: map[string]any{"type": "object", "properties": map[string]any{"worker_id": map[string]any{"type": "string"}}, "required": []string{"worker_id"}, "additionalProperties": false}}, execute: func(_ context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				WorkerID string `json:"worker_id"`
			}
			if err := json.Unmarshal(raw, &in); err != nil {
				return nil, err
			}
			worker, err := s.repo.GetWorker(in.WorkerID)
			if err != nil {
				return nil, err
			}
			return map[string]any{"id": worker.ID, "name": worker.Name, "type": worker.WorkerType, "status": worker.Status, "last_heartbeat_at": worker.LastHeartbeatAt, "host_info": worker.HostInfo}, nil
		}},
		agentTool{definition: llm.ToolDefinition{Name: "list_capabilities", Description: "List registered and policy-authorized ADP modules.", Parameters: emptySchema()}, execute: func(context.Context, json.RawMessage) (any, error) {
			var capabilities []map[string]any
			for _, mod := range s.moduleReg.List() {
				if s.policyEng.ValidateTemplate(mod.Code()) == nil {
					capabilities = append(capabilities, map[string]any{"code": mod.Code(), "name": mod.Name(), "description": mod.Description(), "worker_type": mod.ToolType(), "risk_level": mod.RiskLevel(), "parameters": mod.Parameters()})
				}
			}
			return capabilities, nil
		}},
		agentTool{definition: llm.ToolDefinition{Name: "create_module_operation", Description: "Create one operation using a registered module. Never use commands or YAML.", Parameters: map[string]any{"type": "object", "properties": map[string]any{"module_code": map[string]any{"type": "string"}, "worker_id": map[string]any{"type": "string"}, "parameters": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}}, "reason": map[string]any{"type": "string"}}, "required": []string{"module_code", "worker_id", "parameters", "reason"}, "additionalProperties": false}}, execute: s.createModuleOperation},
		agentTool{definition: llm.ToolDefinition{Name: "get_job_result", Description: "Read the status and output of an operation job created by this system.", Parameters: map[string]any{"type": "object", "properties": map[string]any{"job_id": map[string]any{"type": "string"}}, "required": []string{"job_id"}, "additionalProperties": false}}, execute: func(_ context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				JobID string `json:"job_id"`
			}
			if err := json.Unmarshal(raw, &in); err != nil {
				return nil, err
			}
			job, err := s.repo.GetJob(in.JobID)
			if err != nil {
				return nil, err
			}
			return map[string]any{"id": job.ID, "status": job.Status, "success": job.Status == model.JobStatusSuccess, "output": truncateAgentOutput(job.Output)}, nil
		}},
	}
}

func (s *Server) createModuleOperation(_ context.Context, raw json.RawMessage) (any, error) {
	var in struct {
		ModuleCode string            `json:"module_code"`
		WorkerID   string            `json:"worker_id"`
		Parameters map[string]string `json:"parameters"`
		Reason     string            `json:"reason"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	if in.ModuleCode == "" || in.WorkerID == "" || in.Reason == "" {
		return nil, errors.New("module_code, worker_id and reason are required")
	}
	if err := model.ValidateNoInlineSecrets(in.Parameters); err != nil {
		return nil, err
	}
	if err := model.ValidateServiceProfile(in.ModuleCode, in.Parameters); err != nil {
		return nil, err
	}
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
	worker, err := s.repo.GetWorker(in.WorkerID)
	if err != nil {
		return nil, err
	}
	if !model.WorkerCanRunType(worker.WorkerType, mod.ToolType()) {
		return nil, fmt.Errorf("worker %s cannot run %s", in.WorkerID, in.ModuleCode)
	}
	risk := s.policyEng.MergeRisk(mod.RiskLevel())
	approval := s.policyEng.RequiresManualApproval(risk)
	status := model.JobStatusPending
	approvalStatus := model.ApprovalStatusNotRequired
	if approval {
		status = model.JobStatusWaitingApproval
		approvalStatus = model.ApprovalStatusPending
	}
	job, err := s.repo.CreateJob(model.Job{Name: "[agent] " + in.Reason, WorkerType: mod.ToolType(), Status: status, RiskLevel: risk, ApprovalRequired: approval, ApprovalStatus: approvalStatus, TemplateCode: in.ModuleCode, Parameters: in.Parameters, SourceType: "agent"})
	if err != nil {
		return nil, err
	}
	if !approval {
		job, err = s.dispatchJobToWorker(job.ID, in.WorkerID)
		if err != nil {
			return nil, err
		}
		s.workerHub.PushJob(in.WorkerID, job)
	}
	return map[string]any{"job_id": job.ID, "status": job.Status, "approval_required": approval, "module_code": in.ModuleCode}, nil
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
