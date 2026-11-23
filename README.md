# 短链服务 (Short URL Service)

基于 Go + Gin + MongoDB + Redis 的短链接跳转服务。

## 核心特性

| 特性 | 说明 |
|------|------|
| 62 进制编码 | 5 位短链码，ID 空间上限 9.16 亿（62⁵−1） |
| 号段预取 | 每批从 MongoDB 领取 30 个 ID，生成短链时零 IO |
| 异或混淆 | 编码前对 ID 做掩码异或，短码不可直接反推自增 ID |
| 三层缓存 | 空值缓存防穿透、随机过期防雪崩、分布式锁防击穿 |
| 统计异步落库 | Redis 原子计数 + 独立进程批量回写 MongoDB |
| 请求链路追踪 | Sonyflake 生成请求 ID，贯穿访问日志 |

## 技术架构

```
客户端 → Gin → Redis（缓存 / 原子计数） → MongoDB（持久化 / 号段）
```

- **Gin**：HTTP 框架，负责路由与中间件
- **Redis**：热点缓存、点击计数、分布式锁
- **MongoDB**：短链持久化存储、自增 ID 号段

短链的读写路径：

- **创建**：查缓存 → 查库 → 取分布式锁 → 再查库 → 分配 ID → 编码 → 落库 → 写缓存
- **跳转**：查缓存 → 未命中回源 MongoDB → 原子计数 → 302
- **统计**：读缓存与 MongoDB 计数之和；Redis 里的增量由 `cmd/sync` 定期回写 MongoDB

## 项目结构

```
short-url/
├── cmd/
│   ├── server/             # HTTP 服务入口
│   └── sync/                # 点击统计同步入口
├── internal/
│   ├── codec/              # 62 进制编解码
│   ├── config/             # 配置加载（文件 + 环境变量）
│   ├── handler/            # HTTP handlers
│   ├── idgen/              # ID 号段生成器
│   ├── model/              # 数据模型
│   ├── repository/         # 数据访问层（MongoDB / Redis）
│   └── service/            # 业务逻辑
├── pkg/
│   ├── logger/             # 日志组件（zap + lumberjack）
│   ├── middleware/         # Gin 中间件（请求日志 / 请求 ID / 指标）
│   └── response/           # 统一响应格式
├── config/                 # 配置文件
├── k8s/                    # K8s 部署清单
├── .github/workflows/      # CI 流水线
├── Dockerfile
├── docker-compose.yml
└── go.mod
```

## API 接口

所有接口统一响应格式：

```json
{
  "code": 0,
  "msg": "成功",
  "data": {}
}
```

`code` 为 0 表示成功，非 0 为业务错误码。

| 方法 | 路径 | 说明 |
|------|------|------|
| `POST` | `/api/shorten` | 创建短链 |
| `GET` | `/:code` | 跳转到原始地址（302） |
| `GET` | `/api/stat/:code` | 查询短链统计 |
| `GET` | `/api/id-segment` | 查询 ID 号段用量 |
| `GET` | `/health` | 健康检查（探测 MongoDB、Redis） |
| `GET` | `/ready` | 就绪检查（K8s readiness probe） |
| `GET` | `/metrics` | 请求计数指标 |

### 创建短链

```bash
POST /api/shorten
Content-Type: application/json

{"long_url": "https://www.example.com/very/long/path"}
```

**响应**：

```json
{
  "code": 0,
  "msg": "成功",
  "data": {
    "short_url": "http://localhost:8080/AbC12",
    "code": "AbC12"
  }
}
```

同一个长链接重复调用返回同一个短码。

### 短链跳转

```bash
GET /AbC12
# → 302 Redirect to original URL
```

### 查询统计

```bash
GET /api/stat/AbC12
```

**响应**：

```json
{
  "code": 0,
  "msg": "成功",
  "data": {
    "short_code": "AbC12",
    "long_url": "https://www.example.com/very/long/path",
    "click_count": 128,
    "created_at": "2026-05-22 10:00:00",
    "last_visited": "2026-05-22 15:30:00"
  }
}
```

### 错误码

| code | 含义 |
|------|------|
| 1001 | URL 格式无效 |
| 1002 | 短链不存在 |
| 1003 | 服务器内部错误 |
| 1004 | 短链已被禁用 |

## 快速启动

### Docker Compose

```bash
cp .env.example .env    # 按需修改
docker compose up -d --build
```

服务地址：<http://localhost:8080>

Redis 默认连接外部署的实例 —— 容器之间通过 `data-net` 网络用容器名 `redis` 解析，
DB 号（默认 3）用于与同实例上的其他应用隔离。`data-net` 需要预先存在：

```bash
docker network create data-net
```

如果需要一个完全独立的 Redis，用 `standalone` profile：

```bash
docker compose --profile standalone up -d
```

它会额外启动容器 `short-url-redis`（宿主端口 3737），此时需要把 `REDIS_ADDR`
改成 `short-url-redis:6379`、`REDIS_DB` 改成 `0`。

> DB 号隔离只在**单机**模式下有效 —— 集群模式不支持多 DB，所有 key 都会落进 DB 0。

### 本地开发

需要本机安装 Go、MongoDB、Redis：

```bash
go mod download

# MongoDB 用 Docker 起
docker run -d --name mongo -p 27017:27017 mongo:7.0

# Redis：本机进程要用宿主地址连，容器名只在 Docker 网络里可解析
export REDIS_ADDR=127.0.0.1:6379
export REDIS_PASSWORD=your-password
export REDIS_DB=3

go run ./cmd/server -config config/config.yaml
```

点击统计的落库由独立进程负责：

```bash
go run ./cmd/sync -config config/config.yaml -batch 100
```

详见 [DEV.md](DEV.md)。

## 配置说明

配置来自 `config/config.yaml`，环境变量优先级更高。

| 配置节 | 说明 |
|--------|------|
| `app` | 监听地址、运行模式、对外短链域名 |
| `log` | 日志级别、文件滚动策略 |
| `mongodb` | 连接地址、库名、集合名、超时 |
| `redis` | 连接地址、DB 号、密码、连接池 |
| `idgen` | 号段大小、掩码、预取阈值 |
| `cache` | 短链缓存 TTL、空值缓存 TTL、号段缓存 TTL |

可用的环境变量覆盖项：

| 环境变量 | 对应配置 |
|----------|----------|
| `MONGODB_URI` | `mongodb.uri` |
| `MONGODB_DATABASE` | `mongodb.database` |
| `MONGODB_COLLECTION` | `mongodb.collection` |
| `REDIS_ADDR` | `redis.addr` |
| `REDIS_PASSWORD` | `redis.password` |
| `REDIS_DB` | `redis.db` |
| `APP_BASE_URL` | `app.base_url` |

`app.base_url` 为空时，短链地址按 `http://<host>:<port>` 自动拼接。

## MongoDB 索引

服务启动时自动创建：

```javascript
db.short_urls.createIndex({short_code: 1}, {unique: true})   // 短链唯一
db.short_urls.createIndex({long_url: 1}, {unique: true})     // 长链去重
db.short_urls.createIndex({status: 1, expire_at: 1})        // 状态 + 过期
```

号段集合 `url_sequence`：

```javascript
{_id: "global_seq", max: 0}
```

## 部署

- **Docker + GitHub Actions**：见 [.github/workflows/README.md](.github/workflows/README.md)
- **Kubernetes**：见 [K8S.md](K8S.md)

## 依赖

- Go 1.25+
- MongoDB 7.0+
- Redis 7.0+
