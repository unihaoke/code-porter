# syntax=docker/dockerfile:1

# ---- 1. 构建前端产物 ----
FROM node:22-alpine AS builder

ENV NODE_ENV=production

WORKDIR /web
# 强制按 Linux/x64 解析平台相关可选依赖（如 esbuild 预编译二进制），
# 防止 lock 在 Windows/macOS 上生成时把 win32/darwin 二进制当硬依赖 -> EBADPLATFORM。
ENV npm_config_os=linux
ENV npm_config_cpu=x64
# 依赖锁定文件必须随仓库提交；npm ci 从 lock 精确重建，lock 缺失或与
# package.json 不一致会直接报错，避免静默装出坏 node_modules 导致后续 127。
COPY package.json package-lock.json ./
RUN npm ci --no-audit --no-fund

COPY . .
RUN npm run build

# ---- 2. Nginx 托管 + 反向代理 ----
FROM nginx:1.27-alpine

COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=builder /web/dist /usr/share/nginx/html

RUN chmod -R a+rX /usr/share/nginx/html

EXPOSE 80

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget -q -O- http://127.0.0.1/healthz || exit 1

CMD ["nginx", "-g", "daemon off;"]
