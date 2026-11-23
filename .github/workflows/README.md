# GitHub Actions 部署配置说明

本目录的 `deploy.yml` 是**通用模板**——通过 GitHub Secrets + Variables 注入环境差异，可复制到任何项目复用。

## 架构

两个 job 串联：

```
push main → build (GitHub-hosted runner)
              ├─ docker build + push (BuildKit + ACR 公网端点)
              └─ 输出 image_tag

              ↓ needs: build

            deploy (self-hosted runner on ECS)
              ├─ docker pull (公网端点 server-latest)
              ├─ docker compose up -d (--no-deps 不重启 mongo/redis)
              └─ health check
```

## 必需配置的 GitHub Vars（明文）

路径：`repo Settings → Secrets and variables → Actions → Variables → New variable`

| Var 名 | 含义 | 示例值 |
|--------|------|--------|
| `ACR_REGISTRY_HOST` | ACR 公网端点域名 | `<your-acr-region>.personal.cr.aliyuncs.com`(查 ACR 控制台实例详情) |
| `ACR_NAMESPACE` | ACR namespace/repo 路径 | `<your-namespace>/<your-repo>` |
| `IMAGE_NAME` | 镜像名（对应 compose 里 `image: ${IMAGE_REGISTRY}/${IMAGE_NAME}:server-latest`）| `short-url-server` |
| `APP_CONTAINER` | docker-compose 里 `container_name`，健康检查的目标容器 | `short-url` |
| `BASE_GOLANG_TAG` | builder 基础镜像 tag | `golang-1.25-alpine` |
| `BASE_ALPINE_TAG` | runtime 基础镜像 tag | `alpine-3.21` |
| `DEPLOY_DIR` | 服务器上部署目录 | `/data/short-url` |
| `RUNNER_LABELS` | self-hosted runner 标签（逗号分隔）| `self-hosted,docker,aliyun` |
| `CONCURRENCY_GROUP` | 部署并发组名（防止并发部署）| `deploy-short-url` |

## 必需配置的 GitHub Secrets（加密）

路径：`repo Settings → Secrets and variables → Actions → New repository secret`

| Secret 名 | 含义 |
|-----------|------|
| `ACR_USERNAME` | ACR RAM 用户名（用于 build job push） |
| `ACR_PASSWORD` | ACR RAM 密码（用于 build job push） |

## 服务器上必需文件

部署目录 `${DEPLOY_DIR}` 里：

| 文件 | 作用 |
|------|------|
| `.env` | 应用凭证 + 镜像仓库拉取凭证。**至少包含** `REGISTRY_USERNAME` / `REGISTRY_PASSWORD` / `ACR_REGISTRY_HOST` / `ACR_NAMESPACE` / `IMAGE_REGISTRY` / `IMAGE_NAME` |
| `docker-compose.yml` | 由 deploy job 自动 cp 最新版本（无需手动维护） |

首次部署会从仓库 `.env.example` 自动生成 `.env`，**手动填真实凭证后**才能正常 deploy。

> `.env` 只在文件不存在时生成，已存在则保持原样 —— 改 `.env.example` 不会自动同步到服务器，
> 需要手动更新服务器上的 `.env`。

## 复用步骤（迁移到其他项目）

1. **复制 deploy.yml** 到目标项目的 `.github/workflows/deploy.yml`
2. **在目标 repo 配置 8 个 Vars + 2 个 Secrets**
3. **runner 机器上**：
   - 装好 runner、注册、标签对齐 `RUNNER_LABELS` 的值
   - 准备好 base 镜像（golang / alpine / mongo / redis）已 sync 到 `${ACR_REGISTRY_HOST}/${ACR_NAMESPACE}`
4. **首次部署**：
   - 准备项目源码（Dockerfile + docker-compose.yml）
   - push 代码 → 自动触发 deploy job
   - 在 `${DEPLOY_DIR}/.env` 填入真实凭证
   - 再 push 一次（或手动触发 workflow）

## 镜像 tag 约定

镜像全名 = `${ACR_REGISTRY_HOST}/${ACR_NAMESPACE}/${IMAGE_NAME}:<tag>`。

| Tag | 类型 | 用途 |
|-----|------|------|
| `${IMAGE_NAME}-<7位 sha>` | immutable | 精确版本（由 build job 打） |
| `${IMAGE_NAME}:server-latest` | mutable pointer | deploy job 拉这个，compose 的 `image:` 也指向它 |

compose 的 `image:` 由 `${IMAGE_REGISTRY}/${IMAGE_NAME}:server-latest` 拼出（`IMAGE_REGISTRY`
为空时退化成裸名，本地构建用）。两个 tag 都在仓库里直接存在，服务器上不需要再 re-tag。

## ACR 端点说明

| 端点 | 用途 | deploy job 是否使用 |
|------|------|-------------------|
| 公网（`crpi-XXX.cn-...`） | build job push / deploy job pull | ✅ 用 |
| -vpc（`crpi-XXX-vpc.cn-...`）| 内网加速（GitHub-hosted runner 访问不到）| ❌ 不用 |

公网端点跨地域也能用（GitHub-hosted runner 在 AWS 公网访问），速度比 -vpc 略慢但**可移植**。

## 已知约束

1. **GitHub-hosted runner 访问不了 -vpc 端点**：必须用公网端点
2. **ACR 个人版 login 端点限制**：login 走公网稳定，-vpc login 在某些场景会 403
3. **self-hosted runner 必须打 `${RUNNER_LABELS}` 里的所有标签**：否则 deploy job 找不到 runner
4. **base 镜像必须已 sync 到 `${ACR_NAMESPACE}`**：包括 `golang:<tag>` / `alpine:<tag>` / `mongo:<tag>` / `redis:<tag>`，否则 build/deploy 会 fail

## build job 的构建约束

### 1. `--provenance=false --sbom=false`

BuildKit（v0.32+，即 `setup-buildx-action@v3` 装的默认版本）会在镜像 index 里附带一个
`application/vnd.oci.empty.v1+json` 的 attestation manifest。**阿里云 ACR 个人版不认这个
media type**，push 会被拒。关掉 provenance/sbom 后只推普通 image manifest，ACR 才能收。

### 2. base image 的 shell 兜底

`BASE_GOLANG_TAG` / `BASE_ALPINE_TAG` 是 GH Var，**可能没设**。没设时不能直接拼
`"${REGISTRY}:${BASE_GOLANG_TAG}"` —— 拼出来 tag 是空的，`FROM` 解析失败。所以要带 shell 默认值：

```bash
EFFECTIVE_GOLANG_TAG="${BASE_GOLANG_TAG:-golang-1.25-alpine}"
EFFECTIVE_ALPINE_TAG="${BASE_ALPINE_TAG:-alpine-3.21}"
```

> Dockerfile 里的 `ARG` 默认值兜不住这个：显式传 `--build-arg X=`（空值）会**覆盖**
> Dockerfile 的默认值，`FROM` 照样解析失败。真正管用的是 shell 层的 `:-` 兜底。
