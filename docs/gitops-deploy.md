# ADP GitOps Deployment

目标服务器：`43.136.82.118`，运行 kubeadm Kubernetes 集群。

## 1. 推荐链路

```text
源码仓库 -> CI 测试和构建 Server 镜像 -> GitHub Packages/GHCR -> 更新当前仓库部署清单 -> Argo CD -> kubeadm 集群
```

生产环境不建议让 CI 直接 SSH 到服务器执行 `kubectl apply`。CI 只负责构建镜像并更新当前仓库中的部署清单；集群里的 Argo CD 从 Git 拉取期望状态并同步。

## 2. 准备 GitHub Packages 镜像仓库

GitHub Actions 会把 Server 镜像推送到 GitHub Container Registry：

```text
registry: ghcr.io
server image: ghcr.io/<github-owner>/adp-server@sha256:<image-digest>
```

GitHub Actions 使用仓库内置的 `GITHUB_TOKEN` 推送镜像，不需要额外配置镜像仓库用户名和密码。需要确认仓库的 Actions 权限允许写入 packages：

```text
Settings -> Actions -> General -> Workflow permissions -> Read and write permissions
```

## 3. 解决服务器访问 GitHub 受限

当前远程服务器已经给 `argocd-repo-server` 配置了宿主机代理，因此 Argo CD 可以直接拉当前 GitHub 仓库。当前流程：

```text
GitHub 源码仓库
  -> GitHub Actions 构建 Server 镜像
  -> 推送 Server 镜像到 ghcr.io
  -> GitHub Actions 更新 deploy/k8s/overlays/prod/kustomization.yaml
  -> Argo CD 从当前 GitHub 仓库拉取并同步
```

如果后续 GitHub 访问仍然不稳定，再考虑把 GitOps 仓库迁到 Gitee/CODING/自建 GitLab。

## 4. CI 权限

GitHub Actions 需要能推送 GHCR package，并能把新镜像 digest 提交回当前仓库：

```text
Settings -> Actions -> General -> Workflow permissions -> Read and write permissions
```

当前 workflow 不需要配置 `GITOPS_REPO_SSH` 或 `GITOPS_SSH_KEY`。

## 5. 在集群创建真实 Secret

> 真实凭据只能由受控 Secret 系统创建，不能写入 PR、Kustomize overlay 或任务 YAML。生产发布前应使用 SOPS/External Secrets 等受审计方案替代下方的一次性手工命令；手工创建后也不得将导出的 Secret 回传仓库。

不要把明文 Secret 提交到 Git。第一版可以先手工创建：

```bash
kubectl create namespace adp --dry-run=client -o yaml | kubectl apply -f -

kubectl -n adp create secret generic postgres-secret \
  --from-literal=POSTGRES_USER=postgres \
  --from-literal=POSTGRES_PASSWORD='<postgres-password>' \
  --from-literal=POSTGRES_DB=adp

kubectl -n adp create secret generic adp-server-secret \
  --from-literal=ADP_DB_DSN='postgres://postgres:<postgres-password>@postgres.adp.svc.cluster.local:5432/adp?sslmode=disable' \
  --from-literal=ADP_AUTH_ADMIN_USERNAME=admin \
  --from-literal=ADP_AUTH_ADMIN_PASSWORD='<admin-password>' \
  --from-literal=ADP_AUTH_SECRET='<long-random-jwt-secret>' \
  --from-literal=ADP_AUTH_WORKER_TOKEN='<long-random-worker-token>' \
  --from-literal=ADP_LLM_API_KEY='<deepseek-api-key>'
```

创建 GHCR 拉取密钥。`<github-pat>` 至少需要 `read:packages` 权限；如果镜像设为 Public，也可以去掉 Deployment 里的 `imagePullSecrets`。

```bash
kubectl -n adp create secret docker-registry ghcr-pull-secret \
  --docker-server=ghcr.io \
  --docker-username='<github-username>' \
  --docker-password='<github-pat>'
```

后续建议改成 Sealed Secrets 或 SOPS。

## 6. 安装 Argo CD

如果服务器访问 GitHub 受限，先在本地能访问 GitHub 的机器下载 Argo CD install manifest，再上传到服务器执行：

```bash
kubectl create namespace argocd --dry-run=client -o yaml | kubectl apply -f -
kubectl apply -n argocd -f install.yaml
```

然后应用：

```bash
kubectl apply -f deploy/argocd/project.yaml
kubectl apply -f deploy/argocd/application-adp-prod.yaml
```

确认：

```bash
kubectl -n argocd get pods
kubectl -n argocd get applications
kubectl -n adp get pods,svc
```

## 7. 通过 IP + 端口访问

当前清单不依赖域名和 Ingress，直接用 Kubernetes NodePort 暴露 ADP Server HTTP 入口：

```text
http://43.136.82.118:30081
```

对应配置在 `deploy/k8s/base/server-service.yaml`：

```yaml
type: NodePort
ports:
  - name: http
    port: 8080
    targetPort: http
    nodePort: 30081
```

腾讯云安全组建议：

```text
22    只允许你的固定 IP
30081 允许公网，或只允许你的固定 IP
6443  不向公网开放，或只允许你的固定 IP
9090  默认不向公网开放；只有外部 Worker 需要连入时才通过内网、VPN、专线或受控入口开放
```

验证入口：

```bash
curl http://43.136.82.118:30081/healthz
```

当前 GitOps 清单只部署 ADP Server 和 PostgreSQL，不部署 Worker。临时无域名访问使用 `http://43.136.82.118:30081`；生产长期入口应改为 TLS Ingress。Worker 可以后续在需要诊断的机器上独立运行，连接 Server 的 gRPC 地址，并使用由 `ADP_AUTH_WORKER_TOKEN` 注入的 token。不要把 token 放在启动命令行、任务 YAML 或日志中；应通过权限为 `0600` 的本地凭据文件或运行时 Secret 注入。

## 8. 本地验证清单

提交前执行：

```bash
go test ./...
go vet ./...
kubectl kustomize deploy/k8s/overlays/prod
```
