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
| 领域 | `internal/domain/bot` | `Bot` 聚合（租户归属、渠道、凭据、启停）、入站/出站消息值对象 |
| 领域 | `internal/domain/model` | 本地 AI 工具标识（路由到 MCP 适配器） |
| 应用 | `internal/application/auth` | 登录/登出/会话校验、用户管理（admin）、秘钥 CRUD、改密踢会话 |
| 应用 | `internal/application/gateway` | 提交任务（按租户路由）、Pull、ACK、健康上报、生命周期守护、机器人管理（租户化）、机器人入站、网页对话、实例自注册/多实例路由 |
| 应用 | `internal/application/agent` | 拉取消费者、任务执行器、直连消费者、健康上报 |
| 应用 | `internal/application/port` | 出站端口：MCPRunner、TaskEventBroker、DirectPusher、GatewayClient、BotSender、密码哈希器/凭据生成器、Clock、Logger |
| 基础设施 | `internal/infrastructure/**` | HTTP + SSE、WebSocket Hub、MySQL 仓储（go-sql-driver，内嵌 SQL 迁移）、内存仓储（测试用）、bcrypt/SHA-256/crypto-rand 安全组件、令牌桶限流、MCP stdio 客户端与四适配器、IM 渠道适配、系统探测、配置加载 |

## 多租户与鉴权模型

```
用户 user (admin/member)
  ├─ 1:N 秘钥 api_key（scope ∈ {agent, api}，硬删除，存 sha256）
  ├─ 1:N 会话 session（登录签发，存 sha256，TTL 默认 168h）
  ├─ 1:N Agent 实例 agent（实例自注册：秘钥鉴权得到 owner + X-Agent-ID）
  ├─ 1:N 机器人 bot（从旧 bots.json 一次性迁移，挂种子 admin）
  └─ 1:N 任务 task（运行时态在内存，按 owner 过滤）
```

- **鉴权链（Agent 接入）**：`X-Agent-Token`（秘钥）→ scope 校验得到 owner →
  `X-Agent-ID` 实例归属注册/校验 → 注入任务链路。秘钥证明归属，实例 ID 区分机器；
  同一把秘钥可在多台机器上使用（每台首次启动生成独立 agt_ ID）。
- **多实例路由**：提交任务未指定实例时，名下 0 台→503、1 台→自动、多于 1 台→409 附清单；
  指定他人实例→404（不泄露存在性）。
- **存储边界**：用户/秘钥/会话/Agent 身份/机器人 → MySQL（外键 ON DELETE CASCADE）；
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
| 适用 | 群机器人、长任务 | 网页对话、交互式 |

路由裁决在 `domain/task.Router`：direct 模式强依赖长连接，无连接直接返回 `not_connected`。

## 事件扇出

任务执行过程中的分片通过 `TaskEventBroker` 扇出给等待中的连接：

```
Agent ACK(progress) ──▶ AckTaskUseCase ──▶ broker.Publish(chunk)
                                              │
                     ┌────────────────────────┼────────────────────┐
                     ▼                        ▼                    ▼
              SSE（OpenAI 格式）         SSE（/api/chat）     机器人异步等待
```

关键约束：

1. **先订阅再入队**，否则极快完成的任务会丢事件；
2. 订阅写入是非阻塞的，慢消费者会丢弃最旧事件，保证总能读到最新进展与终止事件；
3. 终止事件（`done` / `error` / `rejected`）发布后订阅自动回收，调用方读完也要 `Close()`。

## IM 机器人链路

```
飞书/企微 ──POST──▶ /webhook/{channel}/{bot_id}
                        │
                        ├─ 解密（企微 AES-256-CBC；飞书可选 Encrypt Key）
                        ├─ 签名校验（企微 SHA1；飞书 Verification Token）
                        ├─ URL 验证（challenge / echostr）→ 立即回显
                        ▼
              BotInboundUseCase.Handle
                        │  立即返回（IM 回调有 3~5s 超时）
                        ├─ 提交任务（复用 SubmitTaskUseCase）
                        └─ goroutine 等待终止事件 ──▶ BotSender ──▶ 群 Webhook
```

- 机器人配置持久化在 `data/bots.json`（原子写：临时文件 + rename）；
- Webhook 未配置时只受理不回推，日志会告警；
- 群聊可设置「仅响应 @机器人」，飞书通过 `mentions` 判定，文本中的 `@_user_x` 占位符会被剥离。

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
| 机器人配置 | JSON 文件 | 数据库 |

仓储接口已在领域层定义，替换实现不影响上层。
