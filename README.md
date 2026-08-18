# ADP

ADP 是一个面向智能运维场景的原型项目，当前定义为“基于 AI 的辅助式任务调度平台”。

项目已完成最小调度闭环、风控审批、经验库与受控 Tool-Calling Agent 等核心能力；早期 Phase 0 的规划记录保留在 `docs/project/`，不作为当前实现的技术事实。

## Phase 0 范围

首版能力刻意收敛为以下 3 个典型场景：

1. MySQL 定时备份
2. Nginx 可用性诊断
3. Redis 性能诊断

首版不以支持以下能力为目标：

- 不受限制的自由命令执行
- 面向任意任务的通用自治运维
- 全量云原生集群治理
- 生产级多租户平台能力
- 高风险操作的全自动修复

## 模块边界

- `Web/API`：认证、请求入口、结果查询
- `Control Plane`：Agent Runtime、工具执行、策略校验、调度控制、结果分析
- `Scheduler`：任务入队、任务分发、失败重试、超时处理、执行追踪
- `Worker`：只负责受控执行，不做自主决策
- `LLM Gateway`：统一 Tool-Calling 模型调用抽象层
- `PostgreSQL`：保存元数据、任务定义、任务执行记录、审计日志、故障案例和 Agent 运行事件

## 当前技术栈

- Go `1.25.x`
- Go 标准库 `net/http`
- gRPC `1.82.x`
- PostgreSQL `16`
- Docker Compose `v2`
- Prometheus 文本格式指标（`/metrics`）

版本策略：

- 基础设施版本优先选择稳定、主流方案
- 第一阶段尽量减少依赖数量
- 向量数据库、ELK 等非核心组件后置

## 当前目录结构

```text
ADP/
  api/
    proto/
  bin/                         # 本地构建产物，默认不入库
  cmd/
    server/
    worker/
  configs/
    server/
    worker/
    ai/
    env/
    managed/
  deploy/
    docker-compose/
    k8s/
  docs/
    archive/
    project/
      dev-log.md
      requirements-draft.md
      todo.md
  internal/
    application/
      analyzer/
      parser/
      planner/
    domain/
      model/
      policy/
      template/
    infrastructure/
      auth/
      llm/
      scheduler/
      worker/
    interfaces/
      http/
  scripts/
  README.md
```

## Phase 0 交付物

- 明确 V1 业务范围
- 明确系统架构边界
- 固定初始技术栈与建议版本
- 初始化仓库目录结构
- 在 [docs/project/dev-log.md](./docs/project/dev-log.md) 中记录操作过程

说明：

- 仓库已经初始化为 `module adp`，后续如需与远端仓库路径对齐，可再统一调整模块名

## 下一步

完成 Phase 0 后，建议进入 Phase 1：

- 后端工程初始化
- 鉴权与基础数据模型实现
- Worker 注册与心跳能力实现
- 跑通不依赖 AI 的最小调度闭环

## 当前进度

目前已经完成 Phase 1、Phase 2、Phase 3、Phase 4 和 Phase 5：

- Phase 1：最小调度闭环（HTTP API、JWT 鉴权、Worker 注册/心跳、任务创建/分发/完成）
- Phase 2/3 的旧 LLM 解析、YAML 生成和静态诊断计划链路已移除，正在由 Tool-Calling Agent 重新实现。
- Phase 4：风控与人工确认（`waiting_approval`、人工审批接口、全链路审计日志）
- Phase 5：经验库与可观测性（故障案例入库、历史案例查询、相似建议、Prometheus 指标）

详细实现说明见 [docs/phase1.md](./docs/phase1.md) 和 [docs/project/dev-log.md](./docs/project/dev-log.md)。

LLM 从“生成 JSON/YAML”演进为受控 Tool-Calling 运维 Agent 的设计和迁移路线见 [docs/design/2026-08-03-agent-architecture-upgrade.md](./docs/design/2026-08-03-agent-architecture-upgrade.md)。该方案保持 Worker 受控执行和审批边界，不赋予模型任意命令执行能力。

## 本地运行

1. 启动服务端：

```bash
go run ./cmd/server serve
```

服务端默认会尝试读取本地的 `configs/server/adp.yaml`。仓库仅提供示例文件，首次本地运行可复制：

```bash
cp configs/server/adp.yaml.example configs/server/adp.yaml
go run ./cmd/server serve --config configs/server/adp.yaml
```

服务端会同时启动：

- HTTP API/UI：`127.0.0.1:8080`
- Worker gRPC 双向流：`127.0.0.1:9090`

2. 启动 Worker：

```bash
go run ./cmd/worker run
```

Worker 默认会尝试读取 `configs/worker/adp.yaml`。也可以显式指定：

```bash
go run ./cmd/worker run --config configs/worker/adp.yaml
```

Worker 会连接 `configs/worker/adp.yaml` 中的 `grpc_server_addr`，通过 gRPC 双向流完成注册、心跳、接收任务和回传执行结果。

3. 初始管理员账号：

- 用户名默认是 `admin`，可通过 `ADP_AUTH_ADMIN_USERNAME` 覆盖。
- 必须通过受保护的运行时 Secret 设置 `ADP_AUTH_ADMIN_PASSWORD`；项目不再提供可用的默认密码。

4. 配置参考：

- 配置目录说明见 [configs/README.md](./configs/README.md)
- 服务端本地配置示例见 [configs/server/adp.yaml.example](./configs/server/adp.yaml.example)
- Worker 配置见 [configs/worker/adp.yaml](./configs/worker/adp.yaml)
- Agent 配置通过 `agent.base_url`、`agent.api_key`、`agent.model` 和 `agent.max_steps` 提供。
- 环境变量示例见 [configs/env/app.env.example](./configs/env/app.env.example)

## Protobuf 生成

Worker 和 Server 之间使用标准 protobuf + gRPC 双向流，协议文件在 `api/proto/adp/v1/worker.proto`。

修改 proto 后重新生成 Go 代码：

```bash
protoc --go_out=. --go-grpc_out=. \
  --go_opt=paths=source_relative \
  --go-grpc_opt=paths=source_relative \
  api/proto/adp/v1/worker.proto
```

## API 总览

### Agent API

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/api/v1/agent/runs` | 运行受控 Tool-Calling Agent；输入为 `{"input":"..."}` |
| GET | `/api/v1/agent/runs/{id}` | 查询可恢复 Agent run 的状态与元数据 |
| GET | `/api/v1/agent/runs/{id}/events` | 以 SSE 重放该 run 的持久化事件（可选 `?after=<event_id>`） |
| POST | `/api/v1/agent/runs/{id}/cancel` | 取消 queued/waiting_approval 状态的 run |

Agent 通过目标 Worker 在其宿主机执行已注册 Module：可读取 Worker 上报的宿主机事实、执行 `host_diagnostics` 等只读诊断，并可创建 `restart_service` 修复操作。修复操作必须使用 Worker 本地 `services.cnf` 中固定的 `unit`，且默认策略要求人工审批；Agent 不具备任意 bash 权限。

Agent run 会持久化模型消息、工具调用和工具结果，并记录 `trace_id`、策略版本和提示词版本。审批批准后，服务端从已保存的工具结果继续同一 run；创建的任务使用 run、tool-call、worker 三元组幂等键，因此审批请求或服务重启不会重复创建任务。

### Phase 4 审批与审计 API

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/v1/approvals/jobs` | 查询待审批任务 |
| POST | `/api/v1/approvals/jobs/{id}` | 人工批准或驳回任务 |
| GET | `/api/v1/audit/logs` | 查询审计日志 |

### Phase 5 案例与指标 API

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/v1/cases` | 查询历史故障案例 |
| GET | `/api/v1/cases/suggestions` | 获取相似案例与历史建议 |
| GET | `/metrics` | 导出 Prometheus 文本指标 |

示例：

```bash
curl -X POST http://127.0.0.1:8080/api/v1/agent/runs \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"input":"检查 worker-1 上的 Nginx 是否正常"}'
```

## 下一步

当前已进入交付完善阶段，建议优先补齐：

- Docker Compose 演示环境联调与验收
- 3 个典型场景的完整功能验收与压测
- 独立端到端测试套件与交付材料整理

## 测试

运行全部测试与静态检查：

```bash
go test ./...
go vet ./...
```

说明：

- 当前仓库尚未包含独立的 `tests/integration` 测试包；不要执行 `go test ./tests/integration/...`。

## Docker Compose 演示

当前仓库已提供最小演示栈：

- `server`
- `worker`
- `postgres`

启动方式：

```bash
make init
make up
```

启动后可访问：

- ADP Server: `http://127.0.0.1:18080`
- Worker gRPC: `127.0.0.1:19090`

常用命令：`make logs`、`make ps`、`make down`；`make clean` 会额外删除本地 PostgreSQL 数据卷。若端口冲突，可在 `deploy/docker-compose/.env` 设置 `ADP_HTTP_PORT`、`ADP_GRPC_PORT`。

该 Compose 栈将 `configs/worker/services.cnf.example` 只读挂载给演示 Worker；生产环境必须替换为受保护的实际 `services.cnf`。

## GitHub PR CI/CD

仓库已经补充了面向 PR 的 GitHub Actions 流水线：

- PR 打开、重新打开、追加提交时自动触发
- 先执行 `golangci-lint`
- 再执行 `go test ./...`
- 再通过 SSH 登录远程主机 `43.136.82.118`
- 在远程主机同步最新 PR 代码、按 `deploy/k8s/release.env` 的版本构建镜像
- 最后通过 Kubernetes Deployment 执行滚动发布

详细配置说明见 [docs/cicd.md](./docs/cicd.md)。
