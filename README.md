# CodePorter

把**你本机已安装并登录的 AI 编码工具**（Trae / Claude Code / CodeBuddy / Codex）
变成一条可被远程调用的 API —— 通过公网网关中继，接入 **OpenAI 兼容客户端**、
**飞书 / 企业微信群机器人**和**网页对话控制台**。

> CodePorter **不包含任何 AI 模型**，也不上传你的代码。它只是中继层：
> 请求下发到你自己的机器，AI 在你本机执行，结果原路返回。

---

## 架构

```
                        ┌──────────────── 公网 VPS ─────────────────┐
  OpenAI 客户端          │                                          │
  (Cursor/Cline等) ─────▶│              CodePorter Gateway           │
  飞书 / 企业微信 ───────▶│  /v1/chat/completions  /webhook/{ch}/{id} │
  网页控制台(浏览器) ─────▶│  /api/chat  /api/bots  /api/tasks        │
                        └───────┬──────────────────────┬───────────┘
                                │ Pull 队列            │ WebSocket 直连
                                │ (Agent 主动拉)        │ (低延迟推送)
                        ┌───────▼──────────────────────▼───────────┐
                        │            你的开发机 · LocalAgent         │
                        │  只出站不监听 → 协程池限流 → 本机 MCP 工具  │
                        │  Trae / Claude Code / CodeBuddy / Codex    │
                        └───────────────────────────────────────────┘
```

**双通路**

| 通路 | 触发方式 | 特点 |
|---|---|---|
| **Pull 队列**（默认） | Agent 主动轮询拉取 | 断连不丢任务，离线也能排队，适合长任务与群机器人 |
| **Direct 直连** | WebSocket 长连接推送 | 毫秒级交互，适合网页对话；Agent 不在线时直接失败 |

---

## 目录结构

```
code-porter/
├── backend/              Go 后端（DDD 分层 + Go 标准布局）
│   ├── cmd/gateway/      网关入口
│   ├── cmd/agent/        本地客户端入口
│   ├── internal/domain/        领域层（task / agent / bot / model，零外部依赖）
│   ├── internal/application/   应用层（用例 + 出站端口）
│   ├── internal/infrastructure/ 基础设施（HTTP/SSE/WS、内存与文件仓储、MCP、IM 渠道）
│   ├── pkg/               工具包（apperr / pool / backoff / openai / version）
│   ├── configs/           gateway.yaml、agent.yaml
│   └── scripts/release/   发布产物工具
├── frontend/             Vue 3 + Vite 网页控制台
├── docs/                 文档
├── docker-compose.yml    公网 VPS 一键部署
├── AGENTS.md             AI 编码助手协作指南
└── Makefile              顶层统一入口
```

依赖方向严格单向：`infrastructure → application → domain`。

---

## 快速开始（公网 VPS，Docker Compose 部署）

```bash
# 1. 准备配置：至少改这三处
#    server.public_addr     网关对外 https 地址（机器人回调 URL 由它拼出）
#    security.admin_token   网页控制台登录令牌
#    agent.token            本地 Agent 鉴权令牌
vim backend/configs/gateway.yaml

# 2. 一键启动（网关 + 网页控制台）
cp .env.example .env
docker compose up -d --build

# 3. 打开控制台
#    http://<你的服务器IP>   用 admin_token 登录
docker compose logs -f gateway
```

浏览器打开 `http://<服务器IP>` 即可进入控制台：**对话 / 机器人 / 本地节点 / 任务**。

详细的 HTTPS、更新、备份与故障排查见 [docs/deployment.md](docs/deployment.md)。

---

## 在开发机上启动 LocalAgent

```bash
# 方式一：下载预编译客户端（推荐）
make release                       # 生成 dist/，含 6 个平台客户端 + 下载页
#   打开 dist/index.html 选自己系统的包，解开即用

# 方式二：源码运行
make run-agent
```

编辑 `backend/configs/agent.yaml`，填入与网关一致的 `agent.id` / `agent.token`，
以及网关地址 `gateway.addr`（生产环境用 `https://`）。

> 前置条件：本机已安装并登录至少一个受支持的 AI 编码工具，CodePorter 只是中继层。

---

## 本地开发

```bash
make init           # 整理 Go 依赖 + 安装前端依赖
make run-gateway    # 网关 :9022（同时托管控制台，需先执行 make web）
make dev-web        # 前端热更新 :5173，/api 代理到网关
make test           # 后端测试
```

前端产物由网关托管（`web.static_dir` 默认 `web/dist`），
改完前端执行 `make build-frontend && make web` 再访问 `:9022`。

---

## 网页控制台功能

| 页面 | 能力 |
|---|---|
| 对话 | 直接和本机 AI 对话，SSE 流式输出，可切换模型与通路 |
| 机器人 | 飞书 / 企业微信机器人增删改查、启停、回调地址一键复制 |
| 本地节点 | Agent 在线状态、心跳、CPU/内存、队列长度、已接入的 AI 工具 |
| 任务 | 最近任务的状态、来源、重试次数与错误 |
| 概览 | 节点 / 连接 / 机器人 / 任务状态总览 |

---

## IM 机器人接入

1. 控制台「机器人」→ 新建，选择渠道，填入群机器人 Webhook（结果回推用）；
2. 保存后复制生成的**回调地址**；
3. 到飞书开放平台 / 企业微信后台把回调地址配上（Token、EncodingAESKey 按需填写）；
4. 在群里 @机器人 提问，结果几分钟内回推到群里。

分步截图级指引见 [docs/bot-setup.md](docs/bot-setup.md)。

---

## 接口一览

| 接口 | 说明 |
|---|---|
| `POST /v1/chat/completions` | OpenAI 兼容入口（支持 `stream`，`x-codeporter-mode: direct\|pull`） |
| `POST /api/chat` | 网页对话（POST + SSE） |
| `/api/bots` | 机器人管理（CRUD、启停） |
| `/api/overview` `/api/agents` `/api/tasks` `/api/models` | 控制台数据 |
| `/webhook/feishu/{id}` `/webhook/wecom/{id}` | IM 平台回调 |
| `/agent/pull` `/agent/ack` `/agent/health` `/agent/ws` | LocalAgent 内部接口 |
| `GET /healthz` | 健康检查 |

完整字段说明见 [docs/api.md](docs/api.md)。

---

## 安全说明

- **LocalAgent 只出站、不监听**，无需开放任何入站端口，也不用路由器端口映射；
- 网关必须置于 HTTPS 之后（生产环境不要直接暴露 HTTP）；
- `admin_token`、`api_keys`、`agent_tokens`、`机器人密钥` 都是占位值，**部署前必须替换**；
- 机器人密钥不会回传给前端，列表接口只返回 `has_secret` 之类的布尔标志；
- 任务与队列状态保存在内存、机器人配置保存在 JSON 文件，重启网关任务记录会清空。

---

## 路线图（V0.3）

- [ ] 任务与会话持久化（SQLite / Redis），支持多副本网关
- [ ] 多租户与更细粒度的权限
- [ ] 机器人支持卡片按钮交互、多轮上下文
- [ ] 代码仓库上下文自动附加
- [ ] 更完善的指标与告警

---

## 文档

- [架构设计](docs/architecture.md)
- [接口文档](docs/api.md)
- [机器人接入指南](docs/bot-setup.md)
- [部署指南](docs/deployment.md)
- [开发指南](docs/development.md)
- [AI 协作指南](AGENTS.md)
