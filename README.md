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
  (Bearer 秘钥) ────────▶│              CodePorter Gateway           │
  飞书 / 企业微信 ───────▶│  /v1/chat/completions  /webhook/{ch}/{id} │
  网页控制台(账号密码) ───▶│  /api/auth /api/keys /api/chat /api/bots │
                        │      MySQL：用户/秘钥/会话/实例/机器人       │
                        └───────┬──────────────────────┬───────────┘
                                │ Pull 队列            │ WebSocket 直连
                                │ (Agent 持秘钥主动连)  │ (X-Agent-Token + 实例 ID)
                        ┌───────▼──────────────────────▼───────────┐
                        │            你的开发机 · LocalAgent         │
                        │  只出站不监听 → 协程池限流 → 本机 MCP 工具  │
                        │  Trae / Claude Code / CodeBuddy / Codex    │
                        └───────────────────────────────────────────┘
```

**多租户模型**：一个网关可服务多个开发者。账号密码登录网页控制台，在「秘钥」页
生成**连接秘钥**（可勾选 `agent` 客户端接入权限 / `api` OpenAI 调用权限，支持有效期）。
秘钥证明归属，本地客户端首次启动自动生成实例 ID（一台机器一个 ID）；
任务、机器人、任务历史都按用户隔离，普通用户只能访问自己名下的资源。

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
├── frontend/             Vue 3 + Vite 网页控制台（服务端）
├── client/               Electron 本地客户端（界面 + Go 核心 IPC 桥）
│   ├── src/main/             主进程：窗口、核心进程管理、IPC、原生对话框
│   ├── src/preload/          contextBridge 类型化 API
│   ├── src/renderer/         界面：概览 / 配置 / 日志
│   └── src/shared/           三方共用类型
├── docs/                 文档
├── docker-compose.yml    公网 VPS 一键部署
├── AGENTS.md             AI 编码助手协作指南
└── Makefile              顶层统一入口
```

依赖方向严格单向：`infrastructure → application → domain`。
客户端的界面层不参与这套依赖——它只通过 JSON 行协议与 Go 核心通信，核心逻辑对界面完全无感。

---

## 快速开始（公网 VPS，Docker Compose 部署）

```bash
# 1. 准备配置：改 .env（推荐）或直接改 yaml
#    优先级：进程环境变量 > .env 文件 > backend/configs/*.yaml
cp .env.example .env
vim .env            # 至少改 MYSQL_PASSWORD / MYSQL_ROOT_PASSWORD / GATEWAY_PUBLIC_ADDR

# 2. 一键启动（MySQL 8 + 网关 + 网页控制台；首次启动自动建表并种子化管理员）
docker compose up -d --build

# 3. 打开控制台
#    http://<你的服务器IP>   默认账号 admin / admin123（首次登录后请立即改密）
docker compose logs -f gateway
```

首次登录后：

1. 右上角用户菜单 → **修改密码**（启动日志在仍是默认密码时会持续告警）；
2. 「**秘钥**」页 → 创建秘钥（本机客户端勾选 `agent`，程序化调用勾选 `api`），
   明文秘钥**只显示一次**，复制保存；
3. 把秘钥填到本地客户端（见下节），或在 OpenAI 客户端里用
   `Authorization: Bearer cp_...` 调用 `/v1/chat/completions`；
4. 需要给同事开账号：admin 在「用户管理」页创建用户即可，各自资源互不可见。

> 账号体系数据（用户/秘钥/会话/实例/机器人）持久化在 MySQL 的 `mysql-data` 卷；
> 任务队列与在线状态仍在网关内存（重启可接受的运行时态）。
> DSN 由 `MYSQL_DSN` 注入，密码含特殊字符时需 URL 编码，详见 `.env.example`。

详细的 HTTPS、更新、备份与故障排查见 [docs/deployment.md](docs/deployment.md)。

---

## 在开发机上启动 LocalAgent

**推荐：Electron 图形客户端**（概览 / 配置 / 日志三视图，界面可随时换而不动核心）：

**一键打包**：产物都在 `client/release/`：

| 系统 | 命令 | 产物 |
| --- | --- | --- |
| Windows | 双击 `build-client.bat`（或 `make client-dist`） | 免安装单文件 `CodePorter-<版本>-portable.exe` |
| macOS | `bash build-client.sh`（或 `make client-dist-host`） | `CodePorter-<版本>-mac-<arch>.dmg` + `.zip` |
| Linux | `bash build-client.sh` | `CodePorter-<版本>-linux-<arch>.AppImage` |

dmg 依赖 macOS 的 `hdiutil` 与签名工具链，**只能在 macOS 上产出**；
三平台一起发版用 GitHub Actions（打 tag `v*` 即触发
`.github/workflows/release-client.yml`，自动产出 4 个包并建 Release）。
未签名的 dmg 首次打开会被 Gatekeeper 拦，右键「打开」或 `xattr -cr /Applications/CodePorter.app`。

```bash
cd client
npm install
npm run dev:core      # 编译 Go 核心到 backend/bin/
npm start             # 构建界面并启动
```

详见 [client/README.md](client/README.md)。架构是 **Electron 界面 + Go 核心**：
界面只负责展示与交互，调度 / 协议 / CLI 调用仍在 Go 核心里（`backend/`，1.2 万行、有完整测试），
两者通过 stdin/stdout 的 JSON 行协议通信。

<details>
<summary>其他方式（命令行 / 免安装单文件）</summary>

```bash
# 方式一：下载预编译单文件客户端（无需 Node）
make release                       # 生成 dist/，含 6 个平台客户端 + 下载页

# 方式二：源码运行（命令行无界面模式）
make run-agent

# 方式三：本地直接编译单文件 exe（只需 Go 工具链）
cd backend
go build -ldflags "-s -w -H windowsgui" -o codeporter-agent.exe ./cmd/agent
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags "-s -w -H windowsgui" -o codeporter-agent.exe ./cmd/agent
# 双击弹原生窗口；或无界面运行：
codeporter-agent.exe -console -config configs/agent.yaml
# 本地 AI 连通性自检
codeporter-agent.exe -test-cli -config configs/agent.yaml
```

> 单文件客户端是 walk 原生窗口版（功能完整但界面较简）；需要好看界面用上面的 Electron 版。
> AI 密钥通常**不需要**填写——`mode: cli` 时 Claude Code / Codex 走本机登录态（订阅）。

</details>

在网关控制台创建含 **agent 权限的秘钥**后，把它配置到客户端
（Electron 版在「配置」页填「连接秘钥」；命令行编辑 `backend/configs/agent.yaml` 的 `agent.key`
或设置环境变量 `AGENT_KEY`，网关地址填 `AGENT_GATEWAY_ADDR`）。
实例 ID 首次启动自动生成（`agt_...`）并写回配置，机器名默认取主机名，**多台机器各用各的秘钥或实例均可**。

> 旧版本的 `agent.token` / `AGENT_TOKEN` 已废弃：客户端检测到会直接报错并提示改用秘钥。

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
| 对话 | 和自己名下的本机 AI 对话，SSE 流式，多台机器时选择目标客户端 |
| 秘钥 | 创建/删除连接秘钥（agent/api 双 scope、有效期），明文仅创建时展示一次 |
| 用户管理（仅 admin） | 创建用户、角色分配、重置密码、删除（级联清理资源） |
| 机器人 | 自己名下的飞书 / 企业微信机器人增删改查、启停、回调地址一键复制 |
| 本地节点 | 自己名下客户端的在线状态、心跳、CPU/内存、队列与已接入工具（admin 可看全部） |
| 任务 | 自己最近任务的状态、来源、重试次数与错误（admin 可看全部） |
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

| 接口 | 鉴权 | 说明 |
|---|---|---|
| `POST /api/auth/login` `/logout` `/me` | 公开 / 会话 | 账号密码登录（默认 admin/admin123）、登出、当前用户 |
| `/api/keys` `/api/users/*` | 会话（users 仅 admin） | 秘钥自助管理、用户管理、修改密码 |
| `POST /v1/chat/completions` | 秘钥（api scope） | OpenAI 兼容入口（支持 `stream`，`x-codeporter-agent` 指定实例） |
| `POST /api/chat` | 会话 | 网页对话（POST + SSE） |
| `/api/bots` | 会话 | 机器人管理（CRUD、启停，按租户隔离） |
| `/api/overview` `/api/agents` `/api/tasks` `/api/models` | 会话 | 控制台数据（member 自己 / admin 全部） |
| `/webhook/feishu/{id}` `/webhook/wecom/{id}` | 平台回调 | IM 平台回调（按机器人归属路由） |
| `/agent/pull` `/agent/ack` `/agent/health` `/agent/ws` | 秘钥（agent scope）+ 实例 ID | LocalAgent 内部接口 |
| `GET /healthz` `/admin/agents` `/admin/queues` | 公开 / admin 会话 | 健康检查与运维 |

完整字段说明见 [docs/api.md](docs/api.md)。

---

## 安全说明

- **LocalAgent 只出站、不监听**，无需开放任何入站端口，也不用路由器端口映射；
- 网关必须置于 HTTPS 之后（生产环境不要直接暴露 HTTP）；
- 密码使用 bcrypt 存储，秘钥只保存 SHA-256 哈希；网页会话使用服务端可吊销令牌（默认 7 天有效）；
- 默认管理员 `admin/admin123` 来自数据库迁移种子，**首次登录后必须改密**（启动日志会持续告警）；
- 秘钥明文仅在创建时展示一次；可按 scope 授权、设置有效期、随时删除立即失效；
- 机器人密钥不会回传给前端，列表接口只返回 `has_secret` 之类的布尔标志；
- 账号/秘钥/会话/实例/机器人持久化在 MySQL；任务队列与在线状态为内存运行时态。

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
