# 架构设计

## 分层（DDD + Go 标准布局）

```
infrastructure  ──▶  application  ──▶  domain
（HTTP/WS/持久化/MCP/IM）   （用例 + 端口）      （聚合与领域服务）
```

`domain` 不依赖任何外部库，也不反向依赖上层。用例只依赖 `application/port` 中定义的端口接口，
具体由 `infrastructure` 实现并在 `cmd/*` 里装配。

| 层 | 目录 | 关键内容 |
|---|---|---|
| 领域 | `internal/domain/user` | 用户聚合（用户名规范化、bcrypt 密码哈希校验、角色/状态）、会话实体（可过期、哈希令牌） |
| 领域 | `internal/domain/apikey` | 秘钥聚合（cp_ 明文生成、SHA-256 存储、agent/api scope、有效期） |
| 领域 | `internal/domain/task` | `Task` 聚合（状态机 / 任务锁 / 重试 / 死信 / TTL，带 owner）、`Queue` 实体、`Router` 领域服务 |
| 领域 | `internal/domain/agent` | `Agent` 聚合（实例身份 = owner + 实例 ID、在线状态、健康快照、容量） |
| 领域 | `internal/domain/model` | 本地 AI 工具标识（路由到 MCP 适配器） |
| 应用 | `internal/application/auth` | 登录/登出/会话校验、用户管理（admin）、秘钥 CRUD、改密踢会话 |
| 应用 | `internal/application/gateway` | 提交任务（按租户路由）、Pull、ACK、健康上报、生命周期守护、网页对话、实例自注册/多实例路由 |
| 应用 | `internal/application/agent` | 拉取消费者、任务执行器、直连消费者、健康上报、渠道无关的 IM 机器人服务（IMBotService，消息本机闭环） |
| 应用 | `internal/application/port` | 出站端口：MCPRunner、TaskEventBroker、DirectPusher、GatewayClient、IMBotRunner、密码哈希器/凭据生成器、Clock、Logger |
| 基础设施 | `internal/infrastructure/**` | HTTP + SSE、WebSocket Hub、MySQL 仓储（go-sql-driver，内嵌 SQL 迁移）、内存仓储（测试用）、bcrypt/SHA-256/crypto-rand 安全组件、令牌桶限流、MCP stdio 客户端与四适配器、IM 机器人渠道（imbot 工厂 + feishubot / wecombot 长连接）、pkg/lockfile 单实例锁、系统探测、配置加载 |

## 多租户与鉴权模型

```
用户 user (admin/member)
  ├─ 1:N 秘钥 api_key（scope ∈ {agent, api}，硬删除，存 sha256）
  ├─ 1:N 会话 session（登录签发，存 sha256，TTL 默认 168h）
  ├─ 1:N Agent 实例 agent（实例自注册：秘钥鉴权得到 owner + X-Agent-ID）
  └─ 1:N 任务 task（运行时态在内存，按 owner 过滤）
```

- **鉴权链（Agent 接入）**：`X-Agent-Token`（秘钥）→ scope 校验得到 owner →
  `X-Agent-ID` 实例归属注册/校验 → 注入任务链路。秘钥证明归属，实例 ID 区分机器；
  同一把秘钥可在多台机器上使用（每台首次启动生成独立 agt_ ID）。
- **多实例路由**：提交任务未指定实例时，名下 0 台→503、1 台→自动、多于 1 台→409 附清单；
  指定他人实例→404（不泄露存在性）。
- **存储边界**：用户/秘钥/会话/Agent 身份 → MySQL（外键 ON DELETE CASCADE）；
  任务队列、broker、在线状态 → 网关内存（高频运行时态，重启不影响账号体系）。

## 任务状态机

```
                    ┌────────────── retry ──────────────┐
                    ▼                                    │
 pending ──▶ running ──▶ success                        │
   │           │  └──▶ failed ──────────────────────────┘
   │           └──▶ timeout
   └──▶ deadletter（重试耗尽）
```

- **任务锁**：Agent 拉取任务时加锁（`lock_token` + `locked_until`），锁超时由生命周期守护回收并重投；
- **背压**：本地协程池满时 Agent 用 `released` 状态快速归还任务，不必等锁超时；
- **TTL**：任务整体存活时间，超时置 `timeout` 并通过事件通知等待中的调用方；
- **死信**：重试次数耗尽进入 `deadletter`，不再自动调度。

## 双通路

| | Pull 队列 | Direct 直连 |
|---|---|---|
| 传输 | Agent 主动 `GET /agent/pull` | 预建立 WebSocket，`/agent/ws` |
| 离线容忍 | 任务留在网关队列 | 直接失败（不静默降级，避免调用方误判） |
| 延迟 | 轮询间隔 0.5s ~ 3s 动态退避 | 毫秒级 |
| 适用 | 长任务、离线容忍 | 网页对话、交互式 |

路由裁决在 `domain/task.Router`：direct 模式强依赖长连接，无连接直接返回 `not_connected`。

## 事件扇出

任务执行过程中的分片通过 `TaskEventBroker` 扇出给等待中的连接：

```
Agent ACK(progress) ──▶ AckTaskUseCase ──▶ broker.Publish(chunk)
                                              │
                     ┌────────────────────────┼───────────────┐
                     ▼                        ▼               ▼
              SSE（OpenAI 格式）         SSE（/api/chat）   直连 WS 推送
```

关键约束：

1. **先订阅再入队**，否则极快完成的任务会丢事件；
2. 订阅写入是非阻塞的，慢消费者会丢弃最旧事件，保证总能读到最新进展与终止事件；
3. 终止事件（`done` / `error` / `rejected`）发布后订阅自动回收，调用方读完也要 `Close()`。

## IM 机器人链路（本地客户端长连接，多渠道）

机器人采用渠道无关的「简单工厂 + 策略」结构：`infrastructure/imbot` 按渠道名构造
runner（实现 `port.IMBotRunner`），消息编排统一在 `application/agent/IMBotService`。

```
IM 平台  ◀──出站 WebSocket 长连接──▶  本地 Agent 进程
  │                                     │
  ├─ 飞书开放平台（官方 SDK）            ├─ imbot 工厂 → 渠道 runner（feishubot / wecombot）
  └─ 企业微信 openws（裸 WebSocket）     ├─ IMBotService：@过滤 / 消息去重（立即 ACK，执行全异步）
                                        ├─ 本机 MCP/CLI 执行（每渠道独立协程池/注册表，与网关代理互不依赖）
                                        └─ 流式回复：
                                           ├─ 飞书：OpenAPI PATCH 单张流式卡片（JSON 2.0 打字机）
                                           └─ 企微：aibot_respond_msg 流式 Markdown（过程灰色引用区，正文后折叠）
```

- 机器人运行在 Agent 进程内，网关不参与消息收发，**无需公网域名与回调验签**；
- 渠道之间、机器人与网关代理之间均可独立启停（`bot.start/stop` 带 `channel` 参数，
  与 `agent.start/stop` 互不影响）；
- 每个渠道有独立的 MCP 注册表、协程池与 `pkg/lockfile` 心跳锁
  （`.imbot-<渠道>-<身份哈希>.lock`，保证同机同身份单实例）；
- 群聊可设置「仅响应 @机器人」，思考/工具过程只在流式回复的折叠区域展示，不进入任务结果 / SSE。

## 网页控制台

Vue 3 + Vite 单页应用，构建产物由网关托管（`web.static_dir`，SPA 回退到 `index.html`），
也可以由独立 Nginx 容器托管并反向代理 `/api`。

对话走 `POST /api/chat` + SSE（EventSource 不支持 POST，前端用 fetch 读流）。
Nginx 反代必须 `proxy_buffering off`，否则流式输出会被缓冲而「卡住不吐字」。

## 持久化现状与演进

| 数据 | 当前实现 | 演进方向 |
|---|---|---|
| 任务 / 队列 | 内存 | Redis（多副本网关） |
| Agent 注册与健康 | 内存 | Redis + 过期键 |

仓储接口已在领域层定义，替换实现不影响上层。
