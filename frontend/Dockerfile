# syntax=docker/dockerfile:1

# ---- 1. 构建前端产物 ----
# 注意：必须用 glibc 基础镜像（node:22-slim），不能用 node:22-alpine。
# node:22-alpine 基于 musl libc，而 vite/esbuild 的预编译二进制是 glibc 的，
# 在 alpine 上 esbuild 的工具链无法正常工作，会导致 npm ci 后 vite 不可用 / 缺失 -> 构建 127。
FROM node:22-slim AS builder

ENV NODE_ENV=production

WORKDIR /web
COPY package.json package-lock.json ./
# 用 npm install 而非 npm ci：对 lock 与 package.json 的微小漂移更宽容，
# 能从 package.json 稳定拉到 vite；registry 显式指向 npmmirror，与 lock 中
# resolved 域名一致，避免构建环境网络差异导致依赖装不全。
# 装完立即校验 vite 二进制是否存在，缺失则直接失败（而不是静默漏装、
# 等到 npm run build 才报 127 让人摸不着头脑）。
RUN npm install --no-audit --no-fund --registry=https://registry.npmmirror.com \
 && test -x node_modules/.bin/vite || (echo "ERROR: vite binary missing after npm install"; exit 1)

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
