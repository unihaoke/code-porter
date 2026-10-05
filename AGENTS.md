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
make run-gateway      # 启动网关（:9022，同时托管控制台）
make dev-web          # 前端开发服务器（:5173，代理 /api 到网关）
make release          # 交叉编译客户端 → dist/（含下载页）
make docker-up        # docker compose 一键部署
```

## 改动时的注意事项

- **新增 IM 渠道（在本地客户端，不在网关）**：机器人架构是渠道无关的「简单工厂 + 策略」，
  按以下顺序接线即可，业务编排不要写进 runner：
  1. 新建 `internal/infrastructure/<channel>bot/` 包实现 `port/im_bot.go` 的 `IMBotRunner`
     （长连接 + 重连退避 + 心跳；可选实现 `IMBotCredentialTester`、`IMBotCardPacer`），
     参考 `infrastructure/feishubot`（飞书 SDK）与 `infrastructure/wecombot`（裸 WebSocket）；
  2. 在 `infrastructure/imbot/factory.go` 注册渠道常量、`Credentials` 字段与 `New(spec)` 分支；
  3. `infrastructure/config` 加 `bots.<channel>` 配置节（yaml tag）、环境变量与 `configs/agent.yaml` 模板；
  4. `cmd/agent/service.go` 的 `channelBotConfig` 加配置投影（凭证字段名渠道各异），
     运行时按渠道存于 `bots map[string]*botRuntime`（独立 pool/MCP registry/锁），
     StartBot/StopBot/TestBot 与 IPC `bot.*`（参数 `{"channel":...}`）无需改结构；
  5. 单实例锁一律用 `pkg/lockfile`（按「渠道+身份」命名），消息处理复用
     `application/agent/im_bot_service.go` 的 `IMBotService`，不要新写消息服务；
  6. 客户端五层（shared types → main → preload → store → Settings/Dashboard）跟着加渠道条目。
  网关侧没有也不应有机器人菜单与 webhook 接口。
- **新增对外接口**：先写用例（application），再写 handler（infrastructure/transport/http），
  最后在 `server.go` 注册路由并套上对应鉴权中间件（API Key / Agent Token / Admin Token）。
- **任务事件流**：提交任务前必须先订阅 broker，否则可能丢事件；终态事件要发布，
  调用方读完终止事件后要 `Close()` 订阅。
- **WebSocket 长连接**：必须用与 HTTP 请求无关的根上下文，`r.Context()` 在 handler 返回后会被取消。
- **IM 消息回调有超时**（飞书 3s）：客户端机器人 OnMessage 的受理判断（@过滤、去重）
  必须立即返回，执行与卡片更新全部异步进行（机器人跑在本地 Agent 进程，不经网关）。
- **前端 SSE**：`/api/chat` 是 POST + SSE（EventSource 不支持 POST，用 fetch 读流）。
  Nginx 反代必须 `proxy_buffering off`，否则流式输出会被攒住。
- **密钥不要外传**：客户端飞书机器人的 App Secret 只存在本机配置 / 环境变量中，
  绝不经网关或 IPC 明文回传（GUI 只展示「已配置」布尔状态）。

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
- 前端固定 `vite@^6.4.3`（修掉 CVE-2026-53632 / CVE-2026-53571 / CVE-2026-39365 等 dev server 漏洞），
  **不要降到 5.x**，且要求 Node 20.19+。
- 前端 `npm install` 后若报 `@esbuild/<platform> could not be found`，
  执行 `npm install --no-save --include=optional @esbuild/win32-x64@<esbuild 版本>`。
  **平台包绝不能写进 `package.json` 的 dependencies** —— 否则 Linux 构建会报 `EBADPLATFORM`。
- `npm audit` 必须用官方源：`npm audit --registry=https://registry.npmjs.org`（国内镜像源不支持该端点）。
- 构建容器镜像必须 `CGO_ENABLED=0`（alpine 运行阶段没有 libc）。
