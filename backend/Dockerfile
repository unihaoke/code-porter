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
# 强制按 Linux/x64 解析平台相关可选依赖（如 esbuild 预编译二进制），
# 避免 lock 在 Windows/macOS 上生成时把 win32/darwin 二进制当硬依赖 -> EBADPLATFORM。
ENV npm_config_os=linux
ENV npm_config_cpu=x64
# 依赖锁定文件必须随仓库提交；npm ci 从 lock 精确重建，
# 若 package.json 与 lock 不一致会直接报错（而不是悄悄装出坏 node_modules）。
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci --no-audit --no-fund

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
