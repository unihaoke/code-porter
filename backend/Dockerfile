# syntax=docker/dockerfile:1

# ---- 1. 构建网关二进制 ----
FROM golang:1.23-alpine AS gateway-builder

WORKDIR /src
COPY backend/go.mod backend/go.sum ./
ENV GOPROXY=https://goproxy.cn,direct
RUN go mod download

COPY backend/cmd ./cmd
COPY backend/internal ./internal
COPY backend/pkg ./pkg

# 静态链接：alpine 运行阶段没有 libc，必须 CGO_ENABLED=0
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags "-s -w" \
    -o /out/codeporter-gateway ./cmd/gateway

# ---- 2. 构建前端（一并打进镜像，单容器也能访问控制台）----
# 必须用 glibc 基础镜像（node:22-slim），不能用 node:22-alpine：
# alpine 的 musl libc 与 vite/esbuild 的 glibc 预编译二进制不兼容，会导致 build 阶段 127。
FROM node:22-slim AS web-builder

WORKDIR /web
COPY frontend/package.json frontend/package-lock.json ./
# 用 npm install 而非 npm ci：对 lock 与 package.json 的微小漂移更宽容，
# 能从 package.json 稳定拉到 vite；registry 显式指向 npmmirror，与 lock 中
# resolved 域名一致，避免构建环境网络差异导致依赖装不全。
# 装完立即校验 vite 二进制是否存在，缺失则直接失败（而不是静默漏装、
# 等到 npm run build 才报 127 让人摸不着头脑）。
RUN npm install --include=dev --no-audit --no-fund --registry=https://registry.npmmirror.com \
 && test -x node_modules/.bin/vite || (echo "ERROR: vite binary missing after npm install"; exit 1)

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
