# Short URL - K8s 部署指南

## 前置准备

确保已安装以下工具：

```bash
docker --version       # Docker
kubectl version --client  # K8s 命令行
```

需要一个可用的 K8s 集群，本地开发三选一：

| 工具 | 启动命令 |
|------|---------|
| Docker Desktop 自带 | 设置 → Kubernetes → 启用 |
| minikube | `minikube start` |
| kind | `kind create cluster` |

---

## 一、构建镜像

```bash
# 在项目根目录
docker build -t short-url:latest .
```

验证：

```bash
docker images short-url
```

---

## 二、把镜像交给 K8s

| 环境 | 操作 |
|------|------|
| Docker Desktop 自带 K8s | 无需额外操作 |
| kind | `kind load docker-image short-url:latest` |
| minikube | `minikube image load short-url:latest` |

**生产环境（推送到远程镜像仓库）：**

```bash
docker tag short-url:latest your-registry.com/short-url:latest
docker push your-registry.com/short-url:latest

# 然后修改 k8s/server.yaml 和 k8s/cronjob.yaml 中的 image 和 Ingress 域名
# short-url:latest → your-registry.com/short-url:latest
# short.yourdomain.com → 你的真实域名
```

---

## 三、部署 MongoDB 和 Redis

```bash
# MongoDB
kubectl create deployment mongo --image=mongo:7.0
kubectl set resources deployment mongo \
  --requests=cpu=100m,memory=256Mi \
  --limits=cpu=500m,memory=512Mi
kubectl expose deployment mongo --port=27017 --name=mongo-service

# Redis
kubectl create deployment redis --image=redis:7-alpine
kubectl set resources deployment redis \
  --requests=cpu=50m,memory=64Mi \
  --limits=cpu=200m,memory=128Mi
kubectl expose deployment redis --port=6379 --name=redis-service
```

验证：

```bash
kubectl get pods
kubectl get svc mongo-service redis-service
```

---

## 四、创建 Secret

Secret 不提交到 Git，用命令创建：

```bash
kubectl -n short-url create secret generic short-url-secret \
  --from-literal=MONGODB_URI='mongodb://root:123456@mongo-service:27017' \
  --from-literal=REDIS_ADDR='redis-service:6379' \
  --from-literal=REDIS_PASSWORD='123456'
```

需要的 key：

| Key | 示例值 |
| --- | --- |
| `MONGODB_URI` | `mongodb://user:pass@mongo-service:27017` |
| `REDIS_ADDR` | `redis-service:6379` |
| `REDIS_PASSWORD` | `yourpassword` |

生产环境用同一套 `kubectl create secret` 命令，凭证从密码管理器或 CI 变量里读取即可
（`--dry-run=client -o yaml | kubectl apply -f -` 可以做成幂等的）。

---

## 五、部署应用

```bash
# 先部署主服务（Namespace + ConfigMap + Secret + Deployment + Service）
kubectl apply -f k8s/server.yaml

# 再部署统计同步任务
kubectl apply -f k8s/cronjob.yaml
```

### 5.1 配置 TLS（生产必需）

```bash
# 创建 TLS Secret（使用已有证书）
kubectl -n short-url create secret tls short-url-tls \
  --cert=/path/to/tls.crt \
  --key=/path/to/tls.key

# 或使用 cert-manager 自动管理（推荐）
# kubectl apply -f k8s/certificate.yaml
```

---

## 六、验证

```bash
# 查看 Pod 状态
kubectl -n short-url get pods

# 查看 server 日志
kubectl -n short-url logs -l component=server

# 端口转发到本地
kubectl -n short-url port-forward svc/short-url-server 8080:8080
```

另开终端测试：

```bash
# 健康检查
curl http://localhost:8080/health
# → {"status":"ok","time":"..."}

# 创建短链
curl -X POST http://localhost:8080/api/shorten \
  -H "Content-Type: application/json" \
  -d '{"long_url":"https://github.com"}'
# → {"code":0,"data":{"short_url":"http://0.0.0.0:8080/xxxxx","code":"xxxxx"}}

# 访问短链
curl -v http://localhost:8080/xxxxx
# → 302 重定向到 https://github.com

# 查看统计
curl http://localhost:8080/api/stat/xxxxx
# → {"code":0,"data":{"short_code":"xxxxx","click_count":1,...}}
```

```bash
# 查看 CronJob 状态
kubectl -n short-url get cronjob
kubectl -n short-url get jobs

# 查看 sync 执行日志
kubectl -n short-url logs -l component=sync --tail=20
```

---

## 修改代码后更新

```bash
# 1. 重新构建镜像
docker build -t short-url:latest .

# 2. 如果是 minikube / kind，重新加载
minikube image load short-url:latest
# 或
kind load docker-image short-url:latest

# 3. 滚动重启（不停机）
kubectl -n short-url rollout restart deployment/short-url-server
```

---

## 清理

```bash
kubectl delete -f k8s/cronjob.yaml
kubectl delete -f k8s/server.yaml
kubectl delete deployment mongo redis
kubectl delete svc mongo-service redis-service
```
