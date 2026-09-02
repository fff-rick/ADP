# ADP Agent 上下文管理升级方案

日期：2026-09-01  
状态：建议实施（分阶段）

## 1. 结论

**有条件推荐**借鉴 `baby-agent/ch05/context` 的 `Engine + Policy` 模式，但不
直接移植其实现。

ADP 是可恢复、可审批、可审计的运维 Agent；其上下文必须同时满足：

1. 每一次模型调用有明确的 token 预算，绝不依赖供应商静默截断；
2. 原始 Run Transcript 与给模型的上下文投影分离，压缩不丢审计证据；
3. 工具调用及其结果的协议配对不可被截断；
4. 审批、重启恢复和 SSE 重放能复现当时模型实际看到的内容；
5. 不能把敏感输入、任意历史记录或未审核知识变成模型可访问的“记忆”。

首个版本不引入通用向量记忆库、Redis 或新的 Agent。现有 PostgreSQL 的
`agent_runs`、`agent_events`、`agent_tool_calls` 以及审核型 Incident RAG 已足够。

## 2. 现状与问题

当前调用上下文为：

```text
SystemPrompt + 最近 10 条 ConversationMessage 中的 user/assistant 文本 + 当前用户输入
```

Run 内每轮把 assistant/tool 消息持续追加到 `agent_runs.transcript` 并完整发送给模型。
Transcript 可在审批和服务重启后恢复，但没有模型窗口预算、摘要、压缩策略或上下文
快照。跨 Run 又会丢弃所有工具证据，只保留最终回答。

这带来以下风险：

| 风险 | 后果 |
| --- | --- |
| 用“10 条消息”代替 token 预算 | 单条 Worker 输出、历史案例或中文长输入即可超窗；不同模型的失败行为不一致。 |
| Run 内 Transcript 单向增长 | 多工具诊断成本和超窗概率随步骤线性增加。 |
| 工具消息不进入下一轮会话 | 追问时模型只看见结论，不能区分事实、证据和推断。 |
| 无模型输入快照 | 发生争议或恢复后，无法证明某一步具体给模型看了什么。 |
| 用户原始输入进入 Conversation/Transcript | 现有 `AgentRun.Input` 虽脱敏，但建 Transcript 和 ConversationMessage 的路径仍使用原始 `input`，可能把凭据送给模型或持久化。 |

供应商可在超出窗口时从会话开头丢弃内容，或直接拒绝请求；这不是 ADP 可接受的
证据处理策略。因此由服务端先行编译上下文，并在预算不足时显式降级。

## 3. 为什么不直接照搬 baby-agent/ch05

`baby-agent/ch05/context` 的优点是：显式 token 计数、`StartTurn/CommitTurn` 事务边界、
以及可组合的 summary/offload/truncate 策略。ADP 应采用这些设计原则。

但以下实现不能直接使用：

| 示例做法 | 不适合 ADP 的原因 | ADP 的替代方案 |
| --- | --- | --- |
| 进程内 `Engine.messages` | 重启、审批等待和多实例下状态丢失。 | PostgreSQL 保存规范 Transcript、摘要状态和每步 ContextSnapshot。 |
| 把摘要构造成 `user` 消息 | 混淆用户原话和系统生成内容，削弱审计与提示注入边界。 | 使用显式 `system` 消息，标记 `ADP_CONTEXT_SUMMARY`、来源范围及不确定性。 |
| `load_storage(key)` 读取任意卸载内容 | 模型可扩大访问面，也无法限制到本 Run 的审计证据。 | 仅保留同 Run、已授权 `AgentToolCall` 的 `evidence_ref`；如确有必要，新增受控 `get_run_evidence`，只按已存在的 call ID 返回脱敏、限长片段。 |
| 通用长文本 Preview | 预览可能隐藏失败、审批或目标范围等关键信息。 | 以工具类型定义结构化证据摘要，保留状态、目标、时间、错误码和完整结果哈希。 |
| 仅按消息 token 统计 | 未计算 system prompt、工具 Schema 和预留输出。 | 统一计算固定提示、工具定义、消息与最大输出预留。 |

## 4. 目标架构

```text
canonical Transcript / ToolCall 审计记录（不可变、脱敏）
                          |
                          v
                 Context Manager
          预算 -> 选择 -> 压缩 -> 摘要 -> 截断
                          |
                          +--> ContextSnapshot（给模型的实际投影）
                          |
                          v
                 LLM Completion Request
                          |
                          v
       Assistant / ToolResult 持久化后 CommitTurn
```

`Context Manager` 只生成发送给模型的临时投影，**绝不修改或覆盖**
`agent_runs.transcript`、`agent_events`、`agent_tool_calls`。后者仍是审计和恢复的事实源。

建议新增 application 层：

```go
type ContextManager interface {
    Build(ctx context.Context, in ContextInput) (CompiledContext, error)
    CommitTurn(ctx context.Context, in CommitInput) error
}

type ContextPolicy interface {
    Name() string
    Apply(ctx context.Context, state ContextState) (ContextState, PolicyDecision, error)
}
```

`Build` 为纯编排：读已持久化事实、应用策略、写入快照后才允许 LLM 调用。
`CommitTurn` 只在 assistant 消息及 tool result 均已成功持久化后运行。这样失败、取消或
服务崩溃不会留下“摘要覆盖了原历史但模型响应不存在”的状态。

## 5. 上下文组成与预算

### 5.1 Token 预算

每个模型配置必须提供以下字段；启动时拒绝无效配置：

```yaml
agent:
  context:
    model_context_window_tokens: 65536
    reserved_output_tokens: 4096
    soft_usage_ratio: 0.65
    hard_usage_ratio: 0.80
    keep_recent_turns: 4
    summary_max_tokens: 1200
    tool_evidence_max_tokens: 600
    summarization_timeout: 15s
```

可用输入预算为：

```text
floor((model_context_window_tokens - reserved_output_tokens) * hard_usage_ratio)
  - tokens(system prompt + tool definitions)
```

计算器须覆盖 system/user/assistant/tool 内容、tool call 参数及工具 Schema。由于 ADP
通过 OpenAI-compatible 接口接入不同模型，第一版采用保守 tokenizer 估算器，并用提供方
返回的 `prompt_tokens` 校准偏差；高于硬预算不得调用模型。不得把供应商的自动 truncation
当作控制策略。

### 5.2 固定优先级

`Build` 按以下顺序组装；前面的项目永远不可被后续策略删除：

1. 固定 System Prompt、当前 actor/授权范围、策略和 Prompt 版本；
2. 当前用户输入（脱敏、上限校验后的版本）；
3. 未结束的 assistant tool call 与对应 tool result 成对保留；
4. `waiting_approval` 的 Job/Approval 状态、目标、风险和策略版本；
5. 当前 Run 的关键证据卡片与最近完整 turn；
6. 会话滚动摘要；
7. 最近 4 个已完成的 user/assistant turn；
8. 可丢弃的旧叙述性消息。

若第 1--4 项已超过硬预算，Run 以可解释的 `context_budget_exhausted` 失败；不能删除
系统约束、审批状态或打断 tool-call/tool-result 配对来“凑进窗口”。

## 6. 策略管线

策略与 baby-agent 示例同样按顺序组合，但都是对投影操作，不改 canonical Transcript。

### P0：IngressSanitize（强制）

在写 `ConversationMessage`、`AgentRun.Transcript` 前，统一执行 `SanitizeText`，再应用
请求长度上限。将当前 `createPersistentRun` 中未脱敏的 raw `input` 修正为安全副本；拒绝
含无法安全表达的二进制或超大输入。用户原文不应因“方便恢复”而绕过这一边界。

### P1：ProtocolPin（强制）

把同一 tool call 的 assistant 消息、tool result、call ID 视为原子组。未完成组、审批状态、
已发现 Worker 集合和本 Run 的目标范围为 pinned context。该策略同时防止恢复后模型遗忘
已调用过的工具或更换目标。

### P2：EvidenceOffload（软阈值 65%）

对已完成且不在 pinned 范围内的长工具结果，模型投影替换为：

```json
{
  "evidence_ref": "run_id:tool_call_id",
  "tool": "get_job_result",
  "target": "worker-01",
  "status": "failed",
  "summary": "exit_code=13; permission denied",
  "result_sha256": "..."
}
```

完整的、已脱敏结果继续只保存在 `agent_tool_calls.result`。摘要必须由服务端的工具适配器生成，
而不是由模型自由改写。第一期不开放取回全文的工具；只有真实案例证明不足时，才增加
`get_run_evidence(call_id, fields)`，并限制为当前 Run、白名单字段、600 tokens 与审计记录。

### P3：RollingSummary（软阈值后仍超预算时）

把已完成且不含 active tool protocol 的旧 turn 做增量摘要。摘要 Schema 固定为：

```text
已确认事实：...
已执行动作及结果：...
待决事项/审批：...
约束（目标、权限、策略）：...
不确定或待验证项：...
证据引用：run_id:tool_call_id, ...
```

摘要使用独立、低温度、短 deadline 的 LLM 调用，输入和输出均脱敏、限长；持久化其模型、
版本、覆盖的 message/event 范围和内容哈希。模型上下文中以 `system` 消息标明：摘要为
压缩索引，不是新的事实；需要时必须调用受控工具重新验证。摘要失败时保留原内容进入 P4，
绝不能以空摘要替换历史。

### P4：TurnTruncate（硬阈值 80%）

从最旧的**完整**已摘要 turn 开始移除投影，仅保留摘要、证据卡片、最近 turn 和 pinned
protocol。永不按“最后 N 条数据库行”截取，因为那会在 tool 记录穿插时损坏轮次边界。

## 7. 持久化模型

保留现有表，新增最小必要状态：

```text
conversation_context_states
  conversation_id PK, summary, covered_through_message_id,
  summary_model, summary_prompt_version, token_estimate, version, updated_at

agent_context_snapshots
  id, run_id, step, transcript_version, policy_version,
  model, token_estimate, budget_tokens, decisions JSONB,
  messages JSONB, content_sha256, created_at
```

`messages` 保存的必须是已脱敏的**实际 LLM 输入投影**，而非原始数据库记录；`decisions`
保存每条策略是否触发、移除了哪些范围、产生了哪些证据引用与失败回退原因。快照与 Run
使用事务写入；快照写入失败即不发起模型调用。

会话摘要只覆盖已完成 turn；Run 内的长期工具轨迹由 `agent_tool_calls` 和
`agent_context_snapshots` 负责，不写入会话摘要以避免把跨 Run 的运维证据混成用户记忆。

## 8. 与 RAG、审批和恢复的边界

- 已审核 Incident Case RAG 继续只通过 `search_incident_cases` 显式调用；不自动把向量检索
  结果塞入会话摘要，也不把未审核 Run 直接写进 RAG。
- 审批暂停前必须保存最后一个 ContextSnapshot。批准恢复时以该 Run 的 canonical Transcript
  重新 `Build`，并重新读取 Job/Policy 状态；不复用可能已过期的内存上下文。
- 被拒绝、取消或策略变更时，摘要只能记录状态，不能把历史 proposal 当作可继续执行的指令。
- Conversation、Run、证据和快照必须在接口层按 actor/tenant/project 过滤；在单租户部署中也
  预留字段，不能依赖 Conversation ID 的不可猜测性作为授权。

## 9. 落地顺序

### Phase 0：先修正安全与可观测性

1. 统一入站脱敏，修复 raw input 写入 Conversation/Transcript 的路径；
2. 新增配置与 token estimator，记录 prompt/system/tool/message 各自 token 估算；
3. 每次模型调用先写 `agent_context_snapshots`，不改变现有 10 条历史行为；
4. 指标：`agent_context_tokens_estimated`、`agent_context_budget_ratio`、
   `agent_context_snapshot_failures_total`、`agent_context_over_budget_total`。

### Phase 1：无摘要压缩

1. 以完整 turn 替换现有最后 10 条行截取；
2. 实现 P1、P2、P4 和恢复测试；
3. 默认 dry-run 记录“将压缩什么”，与现有请求的实际 token usage 比对一个发布周期；
4. 达标后对诊断 Agent 小流量启用 hard budget。

### Phase 2：滚动摘要

1. 实现 P3、`conversation_context_states` 与摘要版本化；
2. 用经审核的历史 Run 建立回归集，检查摘要后 Worker、错误码、审批和未决事项是否仍可追溯；
3. 摘要模型不可用时验证 P4 仍能安全运行；
4. 仅在摘要准确率、成本和 P95 延迟达标后放量。

## 10. 验收与测试

必须新增以下测试：

- 单条超长用户输入、超长工具输出、中文混合错误日志均不突破预算；
- tool call 与对应 tool result 在压缩、摘要、恢复后仍完整配对；
- 审批前后、服务重启后，重新构建的上下文保留目标、审批状态与策略版本；
- 检测到 password/token/DSN 的用户输入不会进入 Conversation、Transcript、Snapshot、SSE 或
  摘要模型；
- 摘要超时、存储失败、token 估算异常时，安全回退到完整 turn 截断或明确失败；
- ContextSnapshot 的 hash 与发送给 mock LLM 的消息逐字一致；
- 不同 Conversation/tenant 的 evidence_ref、摘要和快照不可互相读取；
- 历史案例仍只能来自 approved 状态，且不被误述为当前实时事实。

发布门槛：上下文超窗请求为零；快照覆盖率 100%；工具协议配对回归为零；敏感数据负测为零
泄漏；启用后的 P95 Agent 时延增量和单 Run token 成本在预设 SLO 内。

## 11. 取舍与最终建议

| 方案 | 复杂度 | 恢复/审计 | 成本控制 | 建议 |
| --- | --- | --- | --- | --- |
| 继续保留“最后 10 条” | 低 | 低 | 差 | 不推荐。 |
| 直接移植 baby-agent 内存 Engine | 中 | 低 | 中 | 不推荐。它不适配持久化审批和权限边界。 |
| 本方案：持久化事实 + 临时上下文投影 | 中 | 高 | 高 | 推荐。 |
| 新增通用向量记忆/多 Agent | 高 | 中 | 不确定 | 当前不做。没有明确检索失败数据前属于过度设计。 |

最终采用“**预算驱动的 Context Manager + 证据卡片 + 可重放快照 + 渐进摘要**”。它继承
baby-agent 策略化的优点，同时保留 ADP 的受控执行、审批和审计不变式。

## 12. 参考

- `baby-agent/ch05/context/engine.go`、`policy_summary.go`、`policy_offload.go`：策略接口、
  提交边界与摘要/卸载的参考实现。
- [OpenAI Responses API 的 truncation 与 token usage 说明](https://platform.openai.com/docs/api-reference/responses-streaming/response/refusal?lang=python)：供应商截断不能替代应用侧上下文预算控制。
