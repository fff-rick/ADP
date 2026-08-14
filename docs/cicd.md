# GitOps 部署体系

本仓库采用 GitOps 架构：CI 负责构建推送镜像到 GitHub Container Registry，ArgoCD 负责集群状态同步，SealedSecrets 管理敏感配置。

## 架构总览

```
开发者 push main
      │
      ▼
GitHub Actions (lint → test → build → push ghcr → 提交镜像 digest)
      │
      ├── ghcr.io/fff-rick/adp-server:<sha>
      │   sha256:<immutable-digest>
      │
      ▼
ArgoCD（部署在 kubeadm 集群，3分钟轮询）
      │
      └── deploy/k8s/overlays/prod/（Kustomize + SealedSecret）
            │
            ▼
         集群 Pod（imagePullPolicy: Always）
```

与旧版的区别：

| 维度 | 旧方案 | 新方案 |
|------|--------|--------|
| 镜像构建 | 远程主机 `docker build` | GitHub Actions `docker build & push` |
| 镜像存储 | 远程主机本地 | ghcr.io（GitHub Container Registry） |
| 镜像版本 | `release.env` 手工维护 | CI 提交不可变 image digest |
| 清单管理 | 裸 YAML（`manifests/`） | Kustomize（`base/` + `overlays/prod/`） |
| 敏感配置 | `kubectl create secret --from-env-file` | SealedSecrets 加密后提交 Git |
| 部署触发 | SSH 远程执行脚本 | Git 提交镜像 digest / Argo CD 自动同步 |
| 配置漂移修复 | 无 | ArgoCD selfHeal |

## CI 流水线

工作流文件：`.github/workflows/adp-ci.yml`

```
PR 触发：lint → test
main push：lint → test → build → push GHCR → 提交部署清单 → Argo CD 自动同步
```

### lint

`golangci-lint v2.11`，规则配置在 `.golangci.yml`：

- `errcheck`
- `gofmt`
- `govet`
- `ineffassign`
- `staticcheck`
- `unused`

### test

```bash
go test ./...
```

### build & push

```bash
# tokenpool 风格：先编译二进制，再复制到最简镜像
CGO_ENABLED=0 go build -trimpath -o output/adp-server ./cmd/server
CGO_ENABLED=0 go build -trimpath -o output/adp-worker ./cmd/worker

docker build -f Dockerfile.server -t ghcr.io/fff-rick/adp-server:latest .
docker build -f Dockerfile.worker -t ghcr.io/fff-rick/adp-worker:latest .

docker push ghcr.io/fff-rick/adp-server:latest
docker push ghcr.io/fff-rick/adp-worker:latest
```

### 触发部署

CI 从构建步骤取得 GHCR 的 `sha256` digest，更新
`deploy/k8s/overlays/prod/kustomization.yaml` 并提交回 `main`。Argo CD 发现
Git 提交后同步集群；CI 不连接集群，也不执行 `kubectl` 或 SSH。

## GitHub Secrets 清单

仓库 **Settings → Secrets and variables → Actions → Secrets**：

| Secret | 说明 |
|--------|------|
| 无 | 发布 CI 不需要主机、SSH 或 Kubernetes 凭据 |

同时确保 Settings → Actions → General → **Workflow permissions** 设为 **Read and write**（CI 需要 push 镜像到 ghcr.io）。

ghcr.io 镜像需设为 **Public**（Package Settings → Change visibility），否则集群拉取需要 pull secret。

## ghcr.io 镜像

CI 推送提交 SHA tag，并把其不可变 digest 写入 GitOps 清单：

- `ghcr.io/fff-rick/adp-server:<sha>` — 构建产物的可读索引
- `ghcr.io/fff-rick/adp-server@sha256:...` — 实际部署引用，内容不可变

同理 `adp-worker`。

## 本地开发构建

```bash
# 安装 task（https://taskfile.dev）
go install github.com/go-task/task/v3/cmd/task@latest

# 编译
task build

# 产物在 output/
ls output/
# adp-server  adp-worker
```

## Kustomize 清单结构

```
deploy/k8s/
  base/
    kustomization.yaml
    server-deployment.yaml
    server-service.yaml
    worker-deployment.yaml
  overlays/prod/
    kustomization.yaml           # images override → ghcr.io/fff-rick/*
    secrets/                     # SealedSecret 密文（镜像为 public 时可省略 GHCR 拉取密钥）
    adp-runtime.env              # 明文模板（不提交 Git）
    sealed-adp-runtime.yaml      # SealedSecret（加密后提交 Git）
  argocd/
    adp-app.yaml                 # ArgoCD Application
```

## SealedSecrets

### 原理

SealedSecrets 将 K8s Secret 加密为 `SealedSecret` 自定义资源，只有集群内的 controller 能解密。加密后的 YAML 可以安全提交 Git。

### 修改敏感配置

1. 编辑 `deploy/k8s/overlays/prod/adp-runtime.env`（不提交）
2. 运行 `./scripts/seal-secret.sh`
3. 提交更新后的 `sealed-adp-runtime.yaml`

### 环境变量模板

```env
ADP_SERVER_ADDR=:8080
ADP_ADMIN_USERNAME=admin
ADP_ADMIN_PASSWORD=<your-password>
ADP_AUTH_SECRET=<random-string>
ADP_AUTH_WORKER_TOKEN=<random-string>
ADP_SERVER_URL=http://adp-server:8080
ADP_WORKER_NAME=worker-prod-1
ADP_WORKER_TYPE=shell
ADP_WORKER_POLL_INTERVAL=5s
ADP_LLM_BASE_URL=
ADP_LLM_API_KEY=
ADP_LLM_MODEL=gpt-4
```

## ArgoCD

ArgoCD 部署在集群内，每 3 分钟轮询追踪 `deploy/k8s/overlays/prod/`，自动同步变更并修复配置漂移。

### 访问 ArgoCD UI

```bash
# 获取密码
kubectl -n argocd get secret argocd-initial-admin-secret \
  -o jsonpath="{.data.password}" | base64 -d && echo

# 如果已设置 NodePort 30080
# 浏览器打开 https://<server-ip>:30080
# 登录：admin / 上一步获取的密码
```

### Application 清单

```yaml
# deploy/k8s/argocd/adp-app.yaml
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: adp
  namespace: argocd
spec:
  project: default
  source:
    repoURL: https://github.com/fff-rick/ADP-fff-rick.git
    targetRevision: main
    path: deploy/k8s/overlays/prod
  destination:
    namespace: adp
    server: https://kubernetes.default.svc
  syncPolicy:
    automated:
      prune: true
      selfHeal: true
```

## 部署用户 (adpdeploy)

### 创建 Linux 用户

```bash
sudo useradd -m -s /bin/bash adpdeploy
sudo passwd -d adpdeploy
```

### 创建 K8s ServiceAccount 并授权

```bash
kubectl create serviceaccount adpdeploy -n adp

kubectl apply -f - <<'EOF'
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: adpdeploy
  namespace: adp
rules:
  - apiGroups: ["apps"]
    resources: ["deployments"]
    verbs: ["get", "list", "watch", "update", "patch"]
  - apiGroups: [""]
    resources: ["pods", "services", "secrets"]
    verbs: ["get", "list", "watch"]
  - apiGroups: [""]
    resources: ["namespaces"]
    verbs: ["get", "list"]
EOF

kubectl create rolebinding adpdeploy -n adp \
  --role=adpdeploy --serviceaccount=adp:adpdeploy
```

### 生成长期 Token 和 kubeconfig

```bash
kubectl apply -f - <<'EOF'
apiVersion: v1
kind: Secret
metadata:
  name: adpdeploy-token
  namespace: adp
  annotations:
    kubernetes.io/service-account.name: adpdeploy
type: kubernetes.io/service-account-token
EOF

sleep 2

TOKEN=$(kubectl get secret adpdeploy-token -n adp -o jsonpath='{.data.token}' | base64 -d)
APISERVER=$(kubectl config view --minify -o jsonpath='{.clusters[0].cluster.server}')
CA_DATA=$(kubectl get secret adpdeploy-token -n adp -o jsonpath='{.data.ca\.crt}')

sudo mkdir -p /home/adpdeploy/.kube
sudo tee /home/adpdeploy/.kube/config << KUBECONFIG_EOF
apiVersion: v1
kind: Config
clusters:
  - cluster:
      certificate-authority-data: ${CA_DATA}
      server: ${APISERVER}
    name: default
contexts:
  - context:
      cluster: default
      namespace: adp
      user: adpdeploy
    name: adp
current-context: adp
users:
  - name: adpdeploy
    user:
      token: ${TOKEN}
KUBECONFIG_EOF

sudo chown -R adpdeploy:adpdeploy /home/adpdeploy/.kube
sudo chmod 600 /home/adpdeploy/.kube/config
```

### 配置 SSH 密钥

在**本地开发机**生成密钥：

```bash
ssh-keygen -t ed25519 -C "github-actions-adp" -f ~/.ssh/adp_github_actions
```

在**服务器**上添加公钥（替换为实际公钥内容）：

```bash
sudo mkdir -p /home/adpdeploy/.ssh
echo "ssh-ed25519 AAA... github-actions-adp" | sudo tee /home/adpdeploy/.ssh/authorized_keys
sudo chmod 700 /home/adpdeploy/.ssh
sudo chmod 600 /home/adpdeploy/.ssh/authorized_keys
sudo chown -R adpdeploy:adpdeploy /home/adpdeploy/.ssh
```

验证：`ssh -i ~/.ssh/adp_github_actions adpdeploy@<ip> kubectl get pods -n adp`

## 服务器安装清单

kubeadm 集群需手动安装（一次性）：

```bash
# 1. SealedSecrets
kubectl apply -f https://github.com/bitnami-labs/sealed-secrets/releases/download/v0.27.3/controller.yaml
kubeseal --fetch-cert > sealed-secrets-cert.pem

# 2. ArgoCD
kubectl create namespace argocd
kubectl apply -n argocd -f https://raw.githubusercontent.com/argoproj/argo-cd/stable/manifests/install.yaml

# 3. 创建命名空间
kubectl create namespace adp

# 4. 应用 ArgoCD Application
kubectl apply -f deploy/k8s/argocd/adp-app.yaml
```

## 国内服务器网络问题

如果服务器在国内，HTTPS 访问 GitHub 和 ghcr.io 会被墙，表现为：

- `curl -m 5 https://github.com` 超时/无响应
- `kubectl describe pod` 显示 `Pulling image "ghcr.io/..."` 长时间卡住
- ArgoCD 报 `ComparisonError: context deadline exceeded` 拉不到 Git 仓库

### 解决方案：Clash 代理

前提：服务器已有 Clash 代理运行，假设 HTTP 端口为 `7890`。

验证代理可用：

```bash
curl -m 5 --proxy http://127.0.0.1:7890 https://github.com > /dev/null 2>&1 && echo "OK" || echo "FAIL"
```

#### 1. containerd 走代理

containerd 负责拉取 ghcr.io 镜像，需要配置 systemd 环境变量：

```bash
sudo mkdir -p /etc/systemd/system/containerd.service.d

sudo tee /etc/systemd/system/containerd.service.d/http-proxy.conf <<'EOF'
[Service]
Environment="HTTP_PROXY=http://127.0.0.1:7890"
Environment="HTTPS_PROXY=http://127.0.0.1:7890"
Environment="NO_PROXY=localhost,127.0.0.1,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,.svc,.cluster.local"
EOF

sudo systemctl daemon-reload
sudo systemctl restart containerd
```

配置后验证：

```bash
sudo ctr image pull ghcr.io/fff-rick/adp-server:latest
# 应该在几秒内完成
```

#### 2. ArgoCD repo-server 走代理

ArgoCD 的 repo-server 负责从 GitHub 拉取仓库代码和清单：

```bash
kubectl set env deployment/argocd-repo-server -n argocd \
  HTTP_PROXY=http://127.0.0.1:7890 \
  HTTPS_PROXY=http://127.0.0.1:7890 \
  NO_PROXY=localhost,127.0.0.1,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,.svc,.cluster.local

kubectl rollout status deployment/argocd-repo-server -n argocd --timeout=60s
```

配置后验证 ArgoCD 能否连接 GitHub：

```bash
kubectl get app -n argocd adp -o yaml | grep -A15 "status:"
# 不应再出现 ComparisonError
```

#### 3. kubelet 走代理（可选，拉 ghcr 镜像使用）

如果 containerd 配置后镜像拉取仍然缓慢，可能是 kubelet 层面也需要代理：

```bash
sudo mkdir -p /etc/systemd/system/kubelet.service.d

sudo tee /etc/systemd/system/kubelet.service.d/http-proxy.conf <<'EOF'
[Service]
Environment="HTTP_PROXY=http://127.0.0.1:7890"
Environment="HTTPS_PROXY=http://127.0.0.1:7890"
Environment="NO_PROXY=localhost,127.0.0.1,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,.svc,.cluster.local"
EOF

sudo systemctl daemon-reload
sudo systemctl restart kubelet
```

### 注意事项

- Clash 的 HTTP 代理端口根据实际配置调整（常见：`7890`、`1080`、`1087`）
- `NO_PROXY` 确保 K8s 内部流量（Service CIDR、Pod CIDR、`.svc`、`.cluster.local`）不经过代理
- 如果 Clash 进程挂了，containerd 和 ArgoCD 都会回退到直连（即拉镜像/拉代码超时）

## 完整部署步骤（新环境从零开始）

1. **服务器**：安装 SealedSecrets + ArgoCD + 创建命名空间
2. **服务器**：创建 `adpdeploy` 用户、ServiceAccount、SSH 授权
3. **本地**：配置 GitHub Secrets（`DEPLOY_HOST`, `ADP_DEPLOY_USER`, `ADP_DEPLOY_SSH_KEY`, `ADP_DEPLOY_PORT`）
4. **GitHub**：Settings → Actions → Read and write permissions
5. **GitHub**：ghcr.io Package Settings → Public
6. **提交**：将代码推送到 main 分支
7. **观察**：GitHub Actions 自动构建部署，ArgoCD 自动同步

## 触发边界

- **PR**：只执行 `lint` 和 `test`（Draft PR 跳过）
- **main push**：lint → test → build → push ghcr → SSH 触发滚动更新
