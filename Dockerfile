# syntax=docker/dockerfile:1
# ============================================================
# 构建阶段
# ============================================================
# base 镜像源由 --build-arg 传入，ARG 里的默认值只是兜底
ARG GOLANG_IMAGE=docker.io/library/golang:1.25-alpine
ARG ALPINE_IMAGE=docker.io/library/alpine:3.21

FROM ${GOLANG_IMAGE} AS builder
# 镜像下载 + sum 校验走 goproxy.cn
ENV GOPROXY=https://goproxy.cn,direct
ENV GOSUMDB=off
ENV GO111MODULE=on
# 固定使用镜像里的 go 版本，不自动下载 toolchain
ENV GOTOOLCHAIN=local
ENV GOPATH=/go
ENV PATH=$PATH:/go/bin

WORKDIR /src

# 先只拷贝依赖描述，让 go mod download 这一层可被缓存复用
COPY go.mod go.sum ./
RUN go mod download && go mod verify

COPY . .

# 预热 std lib cache：触发标准库拉取，避免 build 时网络阻塞
RUN go list std 2>&1 | tail -2

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" -trimpath -o /out/server ./cmd/server && \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" -trimpath -o /out/sync ./cmd/sync

# ============================================================
# 运行阶段
# ============================================================
FROM ${ALPINE_IMAGE}

ENV TZ=Asia/Shanghai

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /out/server /out/sync /app/

RUN addgroup -g 1000 app \
    && adduser -u 1000 -G app -D app \
    && mkdir -p /app/config /app/logs \
    && chown -R app:app /app

WORKDIR /app
COPY --chown=app:app config/ /app/config/

USER app

EXPOSE 8080
STOPSIGNAL SIGTERM

CMD ["/app/server", "-config", "config/config.yaml"]
