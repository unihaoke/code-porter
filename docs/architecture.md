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
| 领域 | `internal/domain/task` | `Task` 聚合（状态机 / 任务锁 / 重试 / 死信 / TTL）、`Queue` 实体、`Router` 领域服务 |
| 领域 | `internal/domain/agent` | `Agent` 聚合（在线状态、健康快照、容量） |
| 领域 | `internal/domain/bot` | `Bot` 聚合（渠道、凭据、启停）、入站/出站消息值对象 |
| 领域 | `internal/domain/model` | 本地 AI 工具标识（路由到 MCP 适配器） |
| 应用 | `internal/application/gateway` | 提交任务、Pull、ACK、健康上报、生命周期守护、机器人管理、机器人入站、网页对话 |
| 应用 | `internal/application/agent` | 拉取消费者、任务执行器、直连消费者、健康上报 |
| 应用 | `internal/application/port` | 出站端口：MCPRunner、TaskEventBroker、DirectPusher、GatewayClient、BotSender、Clock、Logger |
| 基础设施 | `internal/infrastructure/**` | HTTP + SSE、WebSocket Hub、内存/文件仓储、令牌桶限流、MCP stdio 客户端与四适配器、IM 渠道适配、系统探测、配置加载 |

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
