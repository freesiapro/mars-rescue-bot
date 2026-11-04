# syntax=docker/dockerfile:1

# ---- 构建阶段 ----
FROM golang:1.26.1-alpine AS builder

WORKDIR /src

# 先复制依赖清单，利用层缓存避免每次改代码都重新下载模块。
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# 纯 Go 依赖（glebarez/sqlite、x/net/proxy），无需 CGO。
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/mars-rescue-bot .

# ---- 运行阶段 ----
FROM alpine:3.21

# ca-certificates：访问 Telegram API 的 TLS 校验。
# tzdata：STATS_TZ 可配置任意时区。
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S -g 10001 app \
    && adduser -S -D -H -u 10001 -G app app

COPY --from=builder /out/mars-rescue-bot /usr/local/bin/mars-rescue-bot

WORKDIR /app

# 数据目录归非 root 用户所有，SQLite 文件（含 -wal/-shm）写入此处。
RUN mkdir -p /data && chown -R app:app /data

# 仅包含非敏感默认值；BOT_TOKEN 等密钥必须由环境变量注入，镜像内不写入任何密钥。
ENV DB_PATH=/data/bot.db

USER app

VOLUME ["/data"]

ENTRYPOINT ["mars-rescue-bot"]
