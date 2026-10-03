# AGENTS.md

> 给 AI 编码助手（Claude Code / Codex / Trae / CodeBuddy）的项目协作指南。
> 改动代码前请先读完本文件，它描述了本仓库的结构约定与不可违反的边界。

## 项目是什么

CodePorter 是一个「本地 AI 编码工具的中继网关」：

- **网关（Gateway，Go）**部署在公网 VPS，对外提供 OpenAI 兼容接口，并把任务下发到开发机；
- **本地代理（LocalAgent，Go）**运行在开发者本机，**只出站不监听**，调用本机已安装并登录的
  Trae / Claude Code / CodeBuddy / Codex 执行任务，再把结果回传；
- **网页控制台（Vue 3）**提供对话、IM 机器人配置与运维监控；
- 飞书 / 企业微信群里的 @机器人 消息会被翻译成任务，结果自动回推到群里。

本项目**不包含任何 AI 模型**，只是中继层。

## 目录结构

```
code-porter/
├── backend/           Go 后端（module github.com/codeporter/code-porter）
│   ├── cmd/gateway/   网关入口
│   ├── cmd/agent/     本地客户端入口
│   ├── internal/domain/        领域层（零外部依赖）
│   ├── internal/application/   应用层（用例 + 端口）
│   ├── internal/infrastructure/ 基础设施层（HTTP/WS/持久化/MCP/IM 渠道）
│   ├── pkg/            可复用工具包（apperr、pool、backoff、openai、version）
│   ├── configs/        yaml 配置
│   └── scripts/release/ 发布产物工具
├── frontend/          Vue 3 + Vite 控制台
├── docs/              文档
├── docker-compose.yml  公网 VPS 部署
└── Makefile           顶层统一入口
```

## 不可违反的架构规则

1. **依赖方向严格单向**：`infrastructure → application → domain`。
   领域层（`internal/domain/**`）**不得 import** 任何第三方库，也不得 import `internal/application` 或 `internal/infrastructure`。
2. **领域对象字段私有**，通过 `NewXxx(Spec)` 构造、通过访问器读取、通过 `Clone()` 复制。
   仓储一律采用「取副本 → 修改 → 回写」，避免聚合状态被并发写坏。
3. **队列仓储接口是原子操作**（Enqueue/Dequeue/Requeue），不要在应用层做 read-modify-write。
4. **出站端口定义在 `internal/application/port`**，实现放在 `internal/infrastructure`。
   用例只依赖端口接口，不依赖具体实现。
5. **HTTP 层（`internal/infrastructure/transport/http`）只做协议转换**，业务逻辑不许写进 handler。
6. **前端不直连 Agent**，只与网关 `/api/*` 通信。

## 命令速查

```bash
make build            # 构建后端 + 前端，产物落到 backend/web/dist
make test             # 后端单测与集成测试
make vet fmt          # 静态检查与格式化
make run-gateway      # 启动网关（:8080，同时托管控制台）
make dev-web          # 前端开发服务器（:5173，代理 /api 到网关）
make release          # 交叉编译客户端 → dist/（含下载页）
make docker-up        # docker compose 一键部署
```

## 改动时的注意事项

- **新增 IM 渠道**：在 `internal/domain/bot/channel.go` 加渠道常量 →
  在 `internal/infrastructure/bot/` 加解析与发送实现 → 在 `Sender.Supports` 注册。
- **新增对外接口**：先写用例（application），再写 handler（infrastructure/transport/http），
  最后在 `server.go` 注册路由并套上对应鉴权中间件（API Key / Agent Token / Admin Token）。
- **任务事件流**：提交任务前必须先订阅 broker，否则可能丢事件；终态事件要发布，
  调用方读完终止事件后要 `Close()` 订阅。
- **WebSocket 长连接**：必须用与 HTTP 请求无关的根上下文，`r.Context()` 在 handler 返回后会被取消。
- **IM 回调有超时**（飞书 3s、企微 5s）：回调 handler 必须立即返回，任务异步执行后通过 Webhook 回推。
- **前端 SSE**：`/api/chat` 是 POST + SSE（EventSource 不支持 POST，用 fetch 读流）。
  Nginx 反代必须 `proxy_buffering off`，否则流式输出会被攒住。
- **密钥不要写进**：`bot.Secret/Token/AESKey` 不回传给前端，只回传 `has_*` 布尔标志。

## 测试要求

- 后端改动后必须通过：`go build ./... && go vet ./... && go test ./...`。
- 领域层状态机、队列、协程池、退避都有单测，改动后请保持覆盖。
- 网关端到端链路在 `backend/test/gateway_flow_test.go`。
- 新增 HTTP 接口建议补一个端到端用例，或至少用 `curl --noproxy '*'` 手工验证
  （本机回环访问要绕过沙箱 HTTP 代理）。

## 常见坑

- 本机 curl 访问 `127.0.0.1` 可能被沙箱代理拦截返回 502，用 `curl --noproxy '*'` 或 Python
  `urllib.request.build_opener(urllib.request.ProxyHandler({}))`。
- Git Bash 的 `/tmp` 与 Windows Python 看到的 `/tmp` 不是同一个目录，跨工具传文件用工作区内相对路径。
- 前端 `npm install` 后若报 `@esbuild/win32-x64 could not be found`，
  执行 `npm install --include=optional @esbuild/win32-x64@<esbuild 版本>`。
- 构建容器镜像必须 `CGO_ENABLED=0`（alpine 运行阶段没有 libc）。
