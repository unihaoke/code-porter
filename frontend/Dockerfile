# syntax=docker/dockerfile:1

# ---- 1. 构建前端产物 ----
FROM node:22-alpine AS builder

ENV NODE_ENV=production

WORKDIR /web
# 用 package*.json glob：仓库没提交 lock 时也不会 COPY 失败
COPY package*.json ./
RUN npm install --no-audit --no-fund

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
