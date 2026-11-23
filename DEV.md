# 本地开发指南

两种启动方式：Docker Compose（推荐）或直接 `go run`。

---

## 方式一：Docker Compose

一条命令启动 server + mongo + 统计同步进程。

### 1. 准备

```bash
cp .env.example .env
```

Redis 是外部署的实例，本 compose 不负责启动它。容器之间通过 `data-net` 网络用容器名
`redis` 解析，所以需要：

```bash
docker network create data-net
```

如果还没有可用的 Redis 实例，用 `standalone` profile 起一个专用的：

```bash
docker compose --profile standalone up -d
```

它启动的容器名是 `short-url-redis`（不是 `redis`），宿主端口 3737。此时 `.env` 要改成：

```
REDIS_ADDR=short-url-redis:6379
REDIS_DB=0
```

> DB 号隔离只在**单机**模式下有效。Redis 若改成集群模式，所有 DB 会合并成 DB 0，隔离失效。

### 2. 启动

```bash
docker compose up -d --build
```

### 3. 查看日志

```bash
docker compose logs -f short-url
```

### 4. 手动跑一次统计同步

`sync-cron` 服务默认每 60 秒同步一轮。想立刻手动跑一次：

```bash
docker compose run --rm --no-deps sync-cron /app/sync -config config/config.yaml -batch 100
```

或者看它跑了什么：

```bash
docker compose logs -f sync-cron
```

### 5. 停止

```bash
docker compose down
```

---

## 方式二：直接 go run

适合改动代码后快速调试，需要本机安装 Go、MongoDB、Redis。

### 1. 准备 MongoDB

```bash
docker run -d --name mongo -p 27017:27017 mongo:7.0
```

### 2. 准备 Redis

本机进程不能用容器名连接，要用宿主地址：

```bash
export REDIS_ADDR=127.0.0.1:6379
export REDIS_PASSWORD=your-password
export REDIS_DB=3
```

Redis 在另一台机器上时，用 SSH 端口转发：

```bash
ssh -L 6379:127.0.0.1:6379 <服务器>
```

DB 号要与其他应用错开，本项目默认用 **3**。

### 3. 下载依赖

```bash
go mod download
```

### 4. 启动 server

```bash
go run ./cmd/server -config config/config.yaml
```

### 5. 另开终端，手动执行同步

```bash
go run ./cmd/sync -config config/config.yaml -batch 100
```

### 6. 停止

```
Ctrl + C 停止 server
docker stop mongo && docker rm mongo
```

---

## 测试接口

```bash
# 健康检查
curl http://localhost:8080/health

# 创建短链
curl -X POST http://localhost:8080/api/shorten \
  -H "Content-Type: application/json" \
  -d '{"long_url":"https://github.com"}'

# 访问短链（把 xxxxx 换成返回的 code）
curl -v http://localhost:8080/xxxxx

# 查看统计
curl http://localhost:8080/api/stat/xxxxx

# 查看 ID 号段用量
curl http://localhost:8080/api/id-segment
```

正常返回示例：

创建短链：

```json
{
  "code": 0,
  "msg": "成功",
  "data": {
    "short_url": "http://0.0.0.0:8080/TSIVC",
    "code": "TSIVC"
  }
}
```

查看统计：

```json
{
  "code": 0,
  "msg": "成功",
  "data": {
    "short_code": "TSIVC",
    "long_url": "https://github.com",
    "click_count": 5,
    "created_at": "2026-05-26 16:00:00",
    "last_visited": "2026-05-26 22:30:00"
  }
}
```

---

## 常见问题

### 容器起不来 / 反复重启

应用日志写在容器内的 `/app/logs/app.log`，**不在 stdout**，`docker logs` 往往是空的：

```bash
docker exec short-url cat /app/logs/app.log
```

### 连不上 Redis

容器之间用容器名解析 Redis，容器名只在**同一张网络**里有效。检查两边是否都挂了
`data-net`：

```bash
docker inspect redis --format '{{range $k,$v := .NetworkSettings.Networks}}{{$k}} {{end}}'
```

连不上时的典型报错是 `lookup redis on 127.0.0.11:53: no such host`。

### MongoDB 健康检查一直不过

首次启动需要创建 root 用户，比普通启动慢，`start_period` 已经放宽到 40 秒。如果改了
账号密码但仍然连不上，注意 `MONGO_INITDB_ROOT_*` 只在数据目录为空时生效 —— 卷初始化
过一次之后，改这两个变量不会改密码。

```bash
docker compose down
docker volume rm short-url_mongo-data
docker compose up -d
```

### 短链重复创建

同一个长链接会返回同一个短码，这是去重逻辑（`long_url` 唯一索引）生效的结果。
