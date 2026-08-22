# ADP

ADP（AI-assisted Dispatch Platform）是一个面向智能运维场景的受控任务调度原型。它将用户请求、策略校验与审批放在 Server 控制面，将实际执行限制在目标主机的 Worker 数据面；LLM 只能选择已注册的运维模块和参数，不能直接生成或执行任意 Shell 命令。

首版覆盖 MySQL 定时备份、Nginx 可用性诊断和 Redis 性能诊断三个典型场景。

## 能力概览

- 通过 HTTP API 与内置 Web 控制台管理用户、Worker、任务和执行结果。
- 使用 Protobuf + gRPC 双向流完成 Worker 注册、心跳、任务下发与结果回传。
- 提供受控 Tool-Calling Agent：查询 Worker、读取主机事实、检索历史案例并创建已授权模块操作。
- 基于模块白名单、风险等级和人工审批约束变更性操作；任务、审批与审计信息持久化到 PostgreSQL。
- 保存 Agent Run、工具调用和事件，支持运行状态查询及 SSE 历史事件回放。
- 提供案例检索与 Prometheus 文本格式 `/metrics` 指标。

## 架构与安全边界

```text
用户 / Web UI / HTTP API
          |
          v
Server：鉴权、Agent、策略、审批、审计、调度、PostgreSQL
          |
          | gRPC 双向流
          v
Worker：本地 Service Profile 校验 → 受管模块执行 → 回传结果
```

LLM 是不可信的决策组件：它只能调用服务端注册工具，不能访问 Bash、SQL、YAML 或主机凭据。Server 校验模块、参数、目标 Worker、风险和审批状态；Worker 在执行前再次校验类型和本地服务 Profile。

MySQL、Redis、Nginx 是 ADP 管理的目标系统，不是 ADP 自身的基础设施依赖。

> 当前 Docker Compose 为本地演示环境：Worker 使用共享 Token，gRPC 未启用 TLS。生产部署应使用受保护的 Secret、mTLS、最小权限的 Worker 账户，并移除所有自由命令兼容路径。

## 技术栈

- Go 1.25、标准库 `net/http`
- gRPC 1.82、Protocol Buffers
- PostgreSQL 16、JSONB
- Cobra + Viper：CLI 与配置管理
- Docker Compose、Kubernetes 清单、GitHub Actions
- Prometheus 文本格式指标

## 快速开始：Docker Compose

### 前置条件

- Docker Engine 与 Docker Compose v2
- `make`

### 启动

```bash
git clone <repository-url>
cd ADP-fff-rick

# 从示例创建被 Git 忽略的本地环境文件；请替换其中的开发密钥。
make init
make up
```

启动完成后：

- Web UI / HTTP API：<http://127.0.0.1:18080>
- Worker gRPC：`127.0.0.1:19090`
- 健康检查：<http://127.0.0.1:18080/healthz>
- 指标：<http://127.0.0.1:18080/metrics>

常用命令：

```bash
make ps                 # 查看服务状态
make logs               # 跟踪全部日志
make logs SERVICE=server
make down               # 停止服务，保留 PostgreSQL 数据卷
make clean              # 停止服务并删除本地 PostgreSQL 数据卷
```

演示 Worker 只读挂载 `configs/worker/services.cnf.example`。生产环境必须改为挂载权限受限的真实服务目录，且不得将凭据提交到仓库。

## 本地开发运行

### 1. 配置 Server

```bash
cp configs/server/adp.yaml.example configs/server/adp.yaml
```

在 `configs/server/adp.yaml` 或受保护的环境变量中设置以下值：

- `db.dsn`：PostgreSQL 连接串；留空时使用内存仓储，仅适用于开发。
- `auth.admin_password`、`auth.secret`、`auth.worker_token`：必须替换为运行时 Secret。
- `agent.base_url`、`agent.api_key`、`agent.model`：启用 Agent 所需的 OpenAI-compatible 模型配置；留空时任务与 Worker API 可用，但 Agent 不可用。

启动 Server：

```bash
go run ./cmd/server serve --config configs/server/adp.yaml
```

默认监听 HTTP `127.0.0.1:8080` 和 Worker gRPC `127.0.0.1:9090`。

### 2. 配置并启动 Worker

```bash
cp configs/worker/adp.yaml.example configs/worker/adp.yaml
sudo install -d -m 0750 /etc/adp
sudo install -m 0640 configs/worker/services.cnf.example /etc/adp/services.cnf

go run ./cmd/worker run --config configs/worker/adp.yaml
```

将 Worker 配置中的 `grpc_server_addr`、`worker_token`、名称和类型改为目标环境对应的值。`services.cnf` 保存 Worker 本地服务 Profile，任务只能引用 Profile 名；不要将密码或连接串放入任务参数。

配置字段详见 [configs/README.md](./configs/README.md)。

## 使用指南

### 登录并获取 Token

```bash
curl -sS -X POST http://127.0.0.1:8080/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"<admin-password>"}'
```

响应中的 `token` 用于后续 API：

```bash
export TOKEN='<login-response-token>'
```

### 发起受控 Agent 诊断

```bash
curl -sS -X POST http://127.0.0.1:8080/api/v1/agent/runs \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"input":"检查 worker-1 上的 Nginx 是否正常"}'
```

查询 Run 与历史事件：

```bash
curl -sS -H "Authorization: Bearer $TOKEN" \
  http://127.0.0.1:8080/api/v1/agent/runs/<run-id>

curl -N -H "Authorization: Bearer $TOKEN" \
  'http://127.0.0.1:8080/api/v1/agent/runs/<run-id>/events?after=0'
```

如果操作进入 `waiting_approval`，管理员可查询并审批：

```bash
curl -sS -H "Authorization: Bearer $TOKEN" \
  http://127.0.0.1:8080/api/v1/approvals/jobs

curl -sS -X POST http://127.0.0.1:8080/api/v1/approvals/jobs/<job-id> \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"approved":true,"comment":"已确认执行窗口"}'
```

### 常用 API

| 功能 | 方法与路径 |
| --- | --- |
| 登录 | `POST /api/v1/auth/login` |
| Worker 管理 | `GET/POST /api/v1/workers` |
| 任务管理 | `GET/POST /api/v1/jobs` |
| Agent Run | `POST /api/v1/agent/runs`、`GET /api/v1/agent/runs/{id}` |
| Agent 事件回放 | `GET /api/v1/agent/runs/{id}/events` |
| 审批 | `GET /api/v1/approvals/jobs`、`POST /api/v1/approvals/jobs/{id}` |
| 审计 | `GET /api/v1/audit/logs` |
| 历史案例 | `GET /api/v1/cases`、`GET /api/v1/cases/suggestions` |
| 指标 | `GET /metrics` |

## 测试与开发检查

```bash
go test ./...
go vet ./...
golangci-lint run --config=.golangci.yml
```

修改 `api/proto/adp/v1/worker.proto` 后，重新生成 Go 代码：

```bash
protoc --go_out=. --go-grpc_out=. \
  --go_opt=paths=source_relative \
  --go-grpc_opt=paths=source_relative \
  api/proto/adp/v1/worker.proto
```

## 当前边界与后续方向

- 当前案例检索为结构化过滤与关键词匹配，不是向量 RAG。
- Agent 指标部分在进程内聚合，重启后会重置；任务和 Run 记录才是持久化事实来源。
- 当前不存在可写入文档的性能压测数据；正式上线前应完成端到端验收、断线恢复与并发压测。
- 生产化优先补齐 mTLS、按用户/目标范围的 RBAC、Transactional Outbox、Worker ACK 与执行去重，再评估消息队列或向量检索。
