# GitHub Actions 部署

`deploy.yml` 在 push 到 `main` 时构建镜像并部署到自托管 runner。

```
push main → build (GitHub-hosted runner)
              ├─ docker build + push
              └─ 推送两个 tag

              ↓ needs: build

            deploy (self-hosted runner)
              ├─ docker pull
              ├─ docker compose up -d
              └─ 健康检查
```

## 配置项

在 `repo Settings → Secrets and variables → Actions` 里配置。

**Variables**（明文）：

| Var 名 | 含义 | 示例值 |
|--------|------|--------|
| `ACR_REGISTRY_HOST` | 镜像仓库公网端点域名 | `<your-acr-region>.personal.cr.aliyuncs.com` |
| `ACR_NAMESPACE` | 仓库 namespace | `<your-namespace>/<your-repo>` |
| `IMAGE_NAME` | 业务镜像名 | `short-url-server` |
| `APP_CONTAINER` | compose 里的 `container_name`，健康检查的目标容器 | `short-url` |
| `BASE_GOLANG_TAG` | builder 基础镜像 tag | `golang-1.25-alpine` |
| `BASE_ALPINE_TAG` | runtime 基础镜像 tag | `alpine-3.21` |
| `DEPLOY_DIR` | 服务器上的部署目录 | `/data/short-url` |
| `RUNNER_LABELS` | self-hosted runner 标签（逗号分隔） | `self-hosted,docker,aliyun` |
| `CONCURRENCY_GROUP` | 部署并发组名 | `deploy-short-url` |

**Secrets**（加密）：

| Secret 名 | 含义 |
|-----------|------|
| `ACR_USERNAME` | 镜像仓库 RAM 用户名（build job push 用） |
| `ACR_PASSWORD` | 镜像仓库 RAM 密码（build job push 用） |

## 服务器上的文件

部署目录 `${DEPLOY_DIR}` 里：

| 文件 | 作用 |
|------|------|
| `.env` | 应用凭证 + 拉取镜像的凭证。至少包含 `REGISTRY_USERNAME` / `REGISTRY_PASSWORD` / `ACR_REGISTRY_HOST` / `ACR_NAMESPACE` / `IMAGE_NAME` |
| `docker-compose.yml` | 由 deploy job 自动同步最新版本 |

首次部署会从仓库的 `.env.example` 生成 `.env`，**填入真实凭证后**才能正常部署。
`.env` 已存在时保持原样 —— 改模板不会自动同步到服务器。

## 镜像 tag

完整镜像名 = `${ACR_REGISTRY_HOST}/${ACR_NAMESPACE}:${IMAGE_NAME}-<version>`。

| Tag | 类型 | 用途 |
|-----|------|------|
| `${IMAGE_NAME}-<7位 sha>` | immutable | 精确版本 |
| `${IMAGE_NAME}-latest` | mutable | deploy job 拉这个，compose 的 `image:` 也指向它 |

compose 的 `image:` 由 `.env` 里的 `ACR_REGISTRY_HOST` / `ACR_NAMESPACE` / `IMAGE_NAME`
拼出（没配镜像仓库时退化成裸名，供本地构建用）。

## 部署约束

1. **runner 必须打全部 `RUNNER_LABELS` 标签**，否则找不到 runner
2. **base 镜像要先 sync 到仓库**：`golang:<tag>` / `alpine:<tag>` / `mongo:<tag>` / `redis:<tag>`
3. **GitHub-hosted runner 访问不了 `-vpc` 内网端点**，统一走公网端点
4. **`--provenance=false --sbom=false` 不能删**：BuildKit 默认会在镜像 index 里附带
   OCI attestation manifest，ACR 个人版不认这个 media type，push 会被拒
5. **base 镜像的 tag 用 shell 兜底**：`BASE_GOLANG_TAG` / `BASE_ALPINE_TAG` 是 Variables，
   可能没设。`"${REGISTRY}:${BASE_GOLANG_TAG}"` 在没设时拼出的 tag 是空的，`FROM` 解析失败。
   写法：`EFFECTIVE_GOLANG_TAG="${BASE_GOLANG_TAG:-golang-1.25-alpine}"`
