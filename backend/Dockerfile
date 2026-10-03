# syntax=docker/dockerfile:1

# ---- 1. 构建网关二进制 ----
FROM golang:1.23-alpine AS gateway-builder

WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download

COPY backend/cmd ./cmd
COPY backend/internal ./internal
COPY backend/pkg ./pkg

# 静态链接：alpine 运行阶段没有 libc，必须 CGO_ENABLED=0
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags "-s -w" \
    -o /out/codeporter-gateway ./cmd/gateway

# ---- 2. 构建前端（一并打进镜像，单容器也能访问控制台）----
FROM node:22-alpine AS web-builder

WORKDIR /web
# package*.json 同时匹配 package.json 与 package-lock.json；
# 用 glob 而非直接指定 lock，避免仓库没提交 lock 文件时 COPY 直接失败。
COPY frontend/package*.json ./
RUN npm install --no-audit --no-fund

COPY frontend/ .
RUN npm run build

# ---- 3. 运行阶段 ----
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata \
    && adduser -D -u 10001 codeporter

WORKDIR /app
COPY --from=gateway-builder /out/codeporter-gateway /app/codeporter-gateway
COPY --from=web-builder /web/dist /app/web/dist
COPY backend/configs/gateway.yaml /app/configs/gateway.yaml

# 机器人配置落盘目录（compose 中以命名卷挂载持久化）
RUN mkdir -p /app/data && chown -R codeporter:codeporter /app

USER codeporter
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8080/healthz || exit 1

ENTRYPOINT ["/app/codeporter-gateway"]
CMD ["-config", "/app/configs/gateway.yaml"]
