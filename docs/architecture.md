# 架构说明

## V1 典型场景

第一版仅覆盖以下 3 个场景：

1. MySQL 定时备份
2. Nginx 可用性诊断
3. Redis 性能诊断

选择这 3 个场景的原因是它们分别覆盖了：

- 周期任务创建与执行
- 服务健康诊断流程
- 多步骤性能排查流程

## 非目标范围

- 任意任务的通用自治执行
- 直接执行 LLM 生成的原始命令
- 大规模集群编排治理
- 生产级租户隔离能力
- 高风险修复动作的自动执行

## 模块职责划分

### Web/API

- 登录与身份认证
- 任务提交
- 任务结果查询
- 中高风险操作的审批入口

### Control Plane

- 将用户输入解析成结构化请求
- 将请求映射为受控工具或模板
- 执行策略校验
- 生成执行计划
- 分析结果并输出诊断摘要

### Scheduler

- 任务入队
- 将任务分配给匹配的 Worker
- 管理重试与超时
- 跟踪任务状态流转

### Worker

- 执行已批准的模板或动作
- 上报状态和输出结果
- 不做本地自主决策

### LLM Gateway

- 提供统一的本地或远程模型接入接口
- 将 Prompt 与模型适配逻辑从核心调度逻辑中解耦

## 安全约束

- 不允许直接执行来自 LLM 原始输出的自由 Shell 命令
- 所有可执行能力必须经过工具白名单或命令模板白名单约束
- 中高风险动作必须进入审批流程
- 审计日志为必选项
- Worker 权限应遵循最小权限原则

## 当前技术栈

- Go `1.25.x`：后端服务开发语言
- Go 标准库 `net/http`：HTTP API 框架
- gRPC `1.82.x`：服务端与 Worker 通信
- PostgreSQL `16`：持久化元数据存储
- Docker Compose `v2`：本地部署编排
- Prometheus 文本格式指标：基础可观测性（`/metrics`）

MySQL 备份与 Redis 诊断是受管运维目标，而非 ADP 自身依赖；当前调度未引入 Redis 队列或缓存。

## 当前推荐源码结构

### `cmd/server`

控制面或 API 服务的启动入口。

### `cmd/worker`

Worker 进程的启动入口。

### `internal/interfaces/http`

接口层，负责 HTTP 路由、鉴权接入、请求响应转换和嵌入式控制台页面。

### `internal/application/*`

应用编排层，承接跨领域用例：

- `internal/application/parser`：自然语言任务解析
- `internal/application/planner`：诊断计划生成与计划存储
- `internal/application/analyzer`：执行结果分析与诊断报告生成

### `internal/domain/*`

领域层，沉淀稳定业务概念与规则：

- `internal/domain/model`：任务、诊断、审批、审计等核心模型
- `internal/domain/policy`：风险分级、白名单与审批规则
- `internal/domain/template`：命令模板注册、参数校验与渲染

### `internal/infrastructure/*`

基础设施层，封装技术细节与运行时能力：

- `internal/infrastructure/auth`：JWT 鉴权与权限校验
- `internal/infrastructure/llm`：统一 LLM 调用抽象
- `internal/infrastructure/scheduler`：当前内存版任务存储、状态流转与指标快照
- `internal/infrastructure/worker`：Worker 轮询、注册与命令执行客户端

### `api/proto`

用于 Worker 通信的 Protobuf 协议定义。

### `deploy/docker-compose`

本地部署所需的编排文件。

### `docs/project`

项目级资料归档目录，集中存放待办、开发日志和需求草稿，避免根目录堆积过程性文档。
