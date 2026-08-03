# ADP LLM 到受控运维 Agent 的升级方案

日期：2026-08-03  
状态：建议实施（设计稿）

## 1. 结论

将当前的“LLM 生成文本，服务端解析 JSON/YAML，再由 Worker 执行”的链路升级为**受控 Tool-Calling Agent**。LLM 不再生成可执行 YAML、命令或可信的风险结论；它只在一个有最大步数、超时、审计和审批拦截的循环中，选择并调用 ADP 暴露的强类型运维工具。

这不是把 `baby-agent/ch02` 的 `bash` 工具直接搬到 ADP。ADP 应借鉴其 `LLM -> tool_calls -> tool result -> LLM` 循环，但工具边界必须是 ADP 的模块、策略和调度能力：模型始终拿不到 shell，也不能绕开审批、Worker 本地服务配置和模块白名单。

推荐的首个可交付版本是“**诊断 Agent（只读）**”：可自主获取能力目录、创建只读诊断步骤、等待/读取执行结果、查询历史案例并输出报告。修复类动作在第二阶段再开放，并且只能通过显式 proposal + policy + 人工审批执行。

## 2. 当前问题和可复用资产

当前 `parser`、`planner`、`analyzer` 分别调用 `llm.Client.Chat`，随后从普通文本中截取并反序列化 JSON；YAML 接口也要求模型生成 YAML。这个模式有四个问题：

- 输出协议靠 prompt 约束，格式漂移会变成解析失败；
- 规划、执行、结果观察分离，LLM 看不到真实工具结果后再决策；
- YAML 是额外的、不安全的中间 DSL，模型输出与最终执行语义可能不一致；
- 每段 LLM 调用的上下文、重试、审计和安全控制不统一。

不应推倒重来。以下资产应直接作为 Agent 的可信执行层：

- `internal/module.Registry` 和内置 Module：能力目录与 Worker 侧受控执行单元；
- `internal/domain/policy.Engine`：模板白名单、风险合并和审批门禁；
- `internal/infrastructure/worker.Runner`：Worker 本地模块执行、服务 Profile 和本地授权；
- Scheduler、Job、Approval、AuditLog、IncidentCase：异步执行、审批、追溯和经验闭环；
- 现有 managed templates / policies：作为工具目录和策略配置的来源。

## 3. 目标架构

```text
用户 / API
  |  POST /api/v1/agent/runs
  v
Agent Runtime --------------> Run/Event Store ----> SSE / 查询 API
  |  (消息、步数、取消、超时)          |
  |                                     +--> Audit Log / Metrics / Trace
  v
LLM Gateway <--- tool definitions --- Tool Registry
  |  tool_calls                            |
  v                                       v
Tool Executor --> Policy Guard --> Scheduler/Approval --> Worker
     ^                                               |
     +-------------- structured ToolResult <---------+

Worker: ModuleInvocation -> 本地授权 -> Module -> Result
```

信任边界：LLM 是不可信的决策组件；Agent Runtime 和 Tool Executor 是服务端可信控制面；Worker 是受限执行面。LLM 的任何 tool call 都必须在执行前由服务端重新做 schema、能力、参数、目标、风险和授权校验。

### 3.1 一个 Agent Run 的状态机

```text
queued -> running -> waiting_approval -> running -> completed
                    |                    |
                    +-> rejected         +-> failed | cancelled | timed_out
```

`waiting_approval` 不是 Agent 进程阻塞等待：Runtime 持久化暂停点和待恢复上下文；审批完成后投递 resume 事件，使用原 run 的会话上下文继续循环。这样重启服务、长时间人工审批和流式 UI 都可恢复。

### 3.2 典型诊断循环

1. Runtime 为请求建立 `AgentRun`，注入用户身份、目标范围、可用工具摘要和系统约束。
2. LLM 选择 `list_capabilities`、`get_worker_facts` 或 `search_incident_cases`；这些工具只返回脱敏且限长的数据。
3. LLM 调用 `propose_module_invocation`，参数只含 `module_code`、`worker_selector`、`parameters`、`reason`，不含命令。
4. Tool Executor 校验模块存在、参数 schema、服务 Profile、Worker 类型和 RBAC；Policy Guard 重新计算风险。
5. 低风险只读操作创建 Job 并调度；高风险操作创建 `ActionProposal`，转入审批，不派发 Job。
6. LLM 用 `get_operation_status` / `get_job_result` 观察真实结果，决定下一步或结束。
7. LLM 只能通过 `finalize_report` 提交有 schema 的报告；Runtime 将其与工具证据关联后持久化。

## 4. 核心设计

### 4.1 LLM Gateway：从 Chat 到 Tool Calling

替换现有只返回字符串的接口，使用供应商无关的消息和工具调用模型。不要让 application 层依赖某一家 SDK。

```go
// internal/infrastructure/llm/client.go
type Client interface {
    Complete(ctx context.Context, req CompletionRequest) (Completion, error)
}

type CompletionRequest struct {
    Model       string
    Messages    []Message
    Tools       []ToolDefinition
    ToolChoice  ToolChoice // Auto / None
    Temperature *float64
}

type Completion struct {
    AssistantMessage Message
    ToolCalls        []ToolCall
    Usage            Usage
}

type ToolCall struct {
    ID        string
    Name      string
    Arguments json.RawMessage
}
```

实现 OpenAI-compatible adapter；对不支持原生 tool calling 的模型，单独提供受限的 JSON adapter，但不能回退到 YAML/正则截取。Adapter 必须对 tool name、arguments JSON 和 call ID 严格校验。

### 4.2 Agent Runtime：唯一的循环拥有者

新增 `internal/application/agent`，负责循环和会话，而不是由 HTTP handler 自己编排：

```go
type Runtime interface {
    Start(ctx context.Context, in StartInput) (model.AgentRun, error)
    Resume(ctx context.Context, runID string) (model.AgentRun, error)
    Cancel(ctx context.Context, runID, actor string) error
}

type Tool interface {
    Definition() llm.ToolDefinition
    Execute(ctx context.Context, call ToolCallContext, args json.RawMessage) (ToolResult, error)
}
```

循环规则：每次模型响应先持久化，再按顺序执行 tool call；每个 call 都写事件；工具结果回填为 `role=tool` 消息。默认 `max_steps=12`、总运行时限 10 分钟、每个 LLM 调用 30 秒、每个工具独立 deadline。达到上限时以 `limit_reached` 结束并返回已收集证据，不允许无限自我重试。

并行 tool call 第一阶段关闭，保证诊断步骤依赖顺序和审计顺序清晰；后续仅允许 Registry 标记为只读且无依赖的工具并行。

### 4.3 工具目录：面向能力，不面向命令

第一批工具如下。所有参数使用 JSON Schema，并在 Go 中二次反序列化为结构体验证。

| 工具 | 风险 | 作用 | 服务端强制条件 |
| --- | --- | --- | --- |
| `list_capabilities` | 低 | 返回可用 Module、参数和风险摘要 | 按用户/Worker 可见性过滤 |
| `get_worker_facts` | 低 | 读取已上报的主机信息 | 只能读授权 Worker |
| `search_incident_cases` | 低 | 查询脱敏历史案例 | 限制条数和返回长度 |
| `propose_module_invocation` | 按模块 | 创建受控运维动作 proposal | module + schema + policy + RBAC 校验 |
| `get_operation_status` | 低 | 读取 proposal / job / approval 状态 | 只能读本 run 关联资源 |
| `get_job_result` | 低 | 读取截断、脱敏后的结果 | 禁止回传服务密钥 |
| `finalize_report` | 低 | 提交结构化诊断报告 | 必须引用本 run 已发生的证据 |

第二阶段新增受控修复 Module（例如 `restart_service`），但不提供 `execute_shell`、`render_command`、`create_yaml`、`approve_action` 或任意文件读写工具。修复 Module 在目标 Worker 宿主机执行，服务单元名仅来自 Worker 本地 ServiceProfile；LLM 无法调用审批工具，审批只能来自有权限的人类 API 操作者。

`propose_module_invocation` 应替代 `TaskIntent.MatchedTemplate` 和 YAML job 中的 `task.template`。示例参数：

```json
{
  "module_code": "http_health_check",
  "worker_selector": {"worker_id": "worker-01"},
  "parameters": {"ServiceProfile": "edge-nginx"},
  "reason": "确认入口服务是否可访问"
}
```

服务端只保存和下发 `module_code + parameters + selector`。`Runner` 已有模块执行路径，应逐步移除 `Job.Command` 的 shell fallback；Worker 根据本地 `ServiceProfile` 解析密钥，绝不把密码、完整配置或原始环境变量送回模型。

### 4.4 模型与持久化

新增领域模型和表（PostgreSQL/MySQL 实现保持与现有 Repository 一致）：

- `AgentSession`：用户、目标范围、摘要、创建/更新时间；
- `AgentRun`：session、状态、模型、system prompt/version、预算、最终摘要、暂停原因；
- `AgentMessage`：run、seq、role、content、tool call 元数据；
- `AgentToolCall`：call ID、工具、脱敏请求/响应、状态、耗时、关联资源；
- `ActionProposal`：run、模块、目标、参数、风险快照、政策版本、审批/Job 关联；
- `AgentEvent`：适合 SSE 重放的不可变事件流。

所有记录需包含 `tenant/project`（即使当前单租户也预留）、`actor`、`trace_id`、`policy_version` 和 `prompt_version`。Tool 结果存储和上下文注入使用不同的脱敏视图：审计库保存受权限控制的原始结果，给 LLM 的仅是截断、secret-redacted 的 `ToolResult`。

### 4.5 安全不可变式

1. LLM 不能产生或执行 shell、SQL、YAML、cron 原文；只可选择已注册 Module。
2. 每次调用 `propose_module_invocation` 都重新从 Registry 获取参数定义，忽略模型声称的风险等级。
3. Policy 在入队前执行，Worker 仍在本地重复授权；两层都失败关闭（deny by default）。
4. 高风险、可变更、跨主机或不可逆 Module 必须进入 `waiting_approval`；审批后应重新做 policy 校验，防止配置已变。
5. Prompt、工具输入、工具输出和最终报告均进行敏感信息扫描/脱敏；禁止把 `services.cnf`、Token、密码注入 LLM context。
6. 目标选择器必须解析为有限、已授权的 Worker 集合；`all` 需明确 policy 许可和审批。
7. 工具不直接调用 HTTP handler，统一经过 application service，保证审计、事务和幂等键。

## 5. 分阶段迁移

### Phase A：基础能力（不改变现有业务）

1. 在 `llm` 增加 `Complete`、tool call 数据结构及 mock；保留 `Chat` adapter，避免一次性影响 parser/analyzer。
2. 新建 `internal/application/agent/{runtime,tool_registry,store,events}.go` 与 domain 模型、Repository migration。
3. 将 Module Registry 投影为 `list_capabilities`；实现只读工具和 Run/Event 审计。
4. 新增 `POST /api/v1/agent/runs`、`GET /api/v1/agent/runs/{id}`、`GET /api/v1/agent/runs/{id}/events`（SSE）与取消接口；仅用 feature flag `agent.enabled=false` 默认关闭。
5. 测试 Runtime 的最大步数、取消、无效 tool call、模型超时和事件重放。

验收：能针对“nginx 无法访问”调用只读工具、返回事件流和结构化报告；不创建任何 Job。

### Phase B：受控诊断执行（建议首先上线）

1. 实现 `propose_module_invocation` 和 `get_operation_status`；仅登记为 read-only 的 Module 自动调度。
2. 将新 Job 的 `Command` 置为兼容字段，执行以 `TemplateCode/Parameters` 为准；将 proto 演进为 `ModuleInvocation`，新老字段双读，先由 server 双写。
3. Agent 根据真实 Job Result 决策后续诊断步骤，最终调用 `finalize_report`。
4. 现有 `/diagnosis/plan` 保持不变，增加 `mode=agent` 或独立 `/agent/runs` 灰度入口。

验收：一次 Agent run 可以完成“查能力 -> 创建 Nginx/Redis 只读检查 -> 等待 Worker 结果 -> 分析报告”；任何未注册模块和任何 raw command 都被拒绝。

### Phase C：审批式变更和旧链路退役

1. 开放带 `RiskProfile.RequiresApproval` 的修复 Module，统一走 `ActionProposal` + 现有 Approval API。
2. 审批通过后 Runtime resume；拒绝、超时或策略变化都生成可解释结论且不得执行。
3. UI 用 Agent 时间线替换 YAML 编辑/生成主入口；旧 YAML API 标记 deprecated，仅保留人工导入的兼容用途。
4. 将 parser/planner/analyzer 迁为兼容 facade 或删除；删除 LLM 生成 YAML 的生产路径。
5. Worker 去除 raw shell fallback。该项必须在库存 Job 迁移完成并有回滚窗口后进行。

## 6. API、配置与兼容性

建议 API：

```text
POST   /api/v1/agent/runs                 # {input, scope, dry_run}
GET    /api/v1/agent/runs/{run_id}
GET    /api/v1/agent/runs/{run_id}/events # SSE，支持 Last-Event-ID
POST   /api/v1/agent/runs/{run_id}/cancel
POST   /api/v1/agent/runs/{run_id}/resume # 仅内部/审批回调使用
```

新增 `configs/ai/agent.yaml`，至少包含：`enabled`、`model`、`max_steps`、`run_timeout`、`llm_timeout`、`max_tool_result_bytes`、`allow_parallel_readonly_tools`、`allowed_tools`、`prompt_version`。策略配置继续是执行授权的唯一来源；`agent.yaml` 不能提升模块权限。

保留 `/tasks/parse`、`/tasks/run`、`/diagnosis/plan` 和 YAML API 一个发布周期。所有新入口由 feature flag 和用户/项目 allowlist 灰度，指标确认后才切流。回滚只需关闭 `agent.enabled`，不会影响传统任务/审批/Worker。

## 7. 测试、观测与发布门槛

单元测试覆盖 Tool schema、Policy Guard、参数脱敏、pause/resume、循环上限和幂等性；Runtime 使用 mock LLM，不依赖真实模型。集成测试至少覆盖：

- 模型试图调用未声明工具、伪造风险、传入 raw command；
- 高风险 proposal 必须等待审批，拒绝后不得创建 Job；
- 审批等待期间服务重启，resume 后不重复派发；
- Worker result 含疑似密钥时，LLM context 与 SSE 均被脱敏；
- 重复 tool call / 网络重试不重复创建 Job；
- feature flag 关闭时旧 API 行为完全不变。

新增指标：`agent_runs_total`、`agent_run_duration_seconds`、`agent_steps_total`、`agent_tool_calls_total`、`agent_tool_latency_seconds`、`agent_policy_denials_total`、`agent_waiting_approval_seconds`、token usage 和 tool error rate。一个 run 对应一条 trace，LLM 调用、tool call、审批等待和 Worker Job 使用同一 `trace_id`。

发布条件：诊断场景的关键测试全绿；所有 Agent 创建 Job 都有 audit；安全负测全部拒绝；灰度期的重复派发率为零；人工抽样确认报告中的每个关键结论都能链接到具体工具结果。

## 8. 目录落位建议

```text
internal/
  application/agent/        # runtime、loop、tool executor、session
  domain/model/agent.go     # AgentRun / Proposal / Event
  domain/agent/             # Tool、状态机、脱敏策略等纯领域规则
  infrastructure/llm/       # provider adapter、mock、tool-call protocol
  infrastructure/db/        # agent repository 与 migration
  interfaces/http/agent.go  # REST/SSE
configs/ai/agent.yaml
tests/integration/agent_run_test.go
```

## 9. 明确不做的事

- 不赋予模型 SSH、bash、文件系统、SQL 或 Kubernetes 任意执行权限；
- 不让模型自行批准、扩大目标范围、注册新 Module 或修改策略；
- 不把 Chain-of-Thought 存入审计或返回给用户；事件记录的是可观察的动作、工具输入/输出摘要和最终理由；
- 不在第一版引入多 Agent 自治协作。先把单 Agent 的安全、恢复、观测和评测做完整，再评估专用子 Agent。

## 10. 实施顺序建议

按 Phase A -> B -> C 交付，每个 Phase 独立可上线、可回滚。优先顺序是：先统一 tool-calling 协议与 Runtime，再接入只读诊断，最后才开放审批式修复。这样可以在不扩大执行面风险的前提下，验证 Agent 是否确实比当前静态诊断计划带来更好的诊断覆盖率和可解释性。
