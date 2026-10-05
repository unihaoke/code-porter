# IM 机器人接入指南

| 渠道 | 接入方式 | 运行位置 | 是否需要公网域名 | 流式回复 |
|---|---|---|---|---|
| 飞书 | **客户端长连接**（官方 SDK WebSocket） | 本地客户端（与代理独立、与其他渠道独立） | 否 | 单张流式卡片（卡片 2.0 打字机） |
| 企业微信 | **智能机器人 API 长连接**（openws WebSocket） | 本地客户端（与代理独立、与其他渠道独立） | 否 | 流式消息（Markdown 增量更新） |

两个渠道都采用「客户端主动建立出站 WebSocket」的模式：消息直接在本机调用
AI 工具处理、再通过平台 OpenAPI 回复，**不经过网关队列，也不需要公网域名、回调验签与
Encrypt Key**。**各渠道互相独立，可同时启用、分别启停**。

> 服务端网关侧的 Webhook 机器人（飞书自定义机器人回调、企业微信应用回调）已整体下线，
> 控制台不再提供「机器人」菜单；IM 机器人统一由本地客户端承载。

> **机器人服务与代理是两个独立服务，机器人各渠道之间也彼此独立**：可以只启动机器人
> （IM 消息本地闭环）而不启动网关代理，也可以飞书、企业微信只开其一或同时开启。
> Windows 客户端在「概览」页提供「代理服务」与「IM 机器人」两张控制卡，机器人卡内
> 每个渠道一行、各有独立的启动/停止按钮；改完某个渠道的配置保存后，只需单独重启该渠道，
> 不用动代理，也不影响另一个渠道。

---

## 一、飞书：客户端长连接（推荐）

### 1. 飞书开放平台配置

在[飞书开放平台](https://open.feishu.cn/app)创建**企业自建应用**：

1. 开启「机器人」能力；
2. 「事件与回调」→ 事件订阅方式选择 **「使用长连接接收事件」**；
3. 添加事件 **`im.message.receive_v1`（接收消息）**；
4. 权限管理开通 `im:message`、`im:message:send_as_bot`（或「以应用身份发消息」）等权限；
5. 创建版本并发布；把机器人加进群（或直接私聊它）。

在「凭证与基础信息」页拿到 **App ID**（`cli_` 开头）与 **App Secret**。

### 2. 客户端配置

编辑客户端的 `configs/agent.yaml`（或 Windows GUI「配置」页的「飞书机器人」分组）：

```yaml
bots:
  feishu:
    enabled: true
    app_id: "cli_xxxxxxxxxxxxxxxx"
    app_secret: "xxxxxxxxxxxxxxxx"   # 建议改用环境变量 FEISHU_APP_SECRET
    model: ""                        # 空=claude-code，可选 trae/codebuddy/codex
    mention_only: true               # 群聊仅响应 @机器人（私聊始终响应）
    system_prompt: ""                # 可选，拼在每条消息前
```

保存后：

- **Windows GUI**：到「概览」页在「IM 机器人」卡的「飞书」一行点「启动」；
  「配置」页飞书卡片里的「测试连接」按钮可只校验 App ID / Secret（换取 tenant_access_token），
  不建立长连接——注意它校验的是**已保存**的配置，改完先保存再测；
  改动本地 AI 设置（工作目录、CLI 命令、密钥、工具开关）后，点「本地 AI」卡片里的
  **「保存并重启本地 AI」**即可自动重启当前运行中的代理 / 机器人渠道，无需逐个手动重启；
- **命令行 / IPC**：通过 `bot.start`、`bot.stop`、`bot.status`、`bot.test` 动作控制，
  参数可带 `{"channel":"feishu"}` 指定渠道（不带时操作全部已启用渠道），
  与 `agent.start/stop` 及 `wecom` 渠道互不影响。

支持的环境变量（优先级高于 yaml）：

| 变量 | 对应字段 |
|---|---|
| `FEISHU_APP_ID` | app_id |
| `FEISHU_APP_SECRET` | app_secret（推荐，避免密钥落盘） |
| `FEISHU_BOT_ENABLED` | enabled（`true`/`1`） |
| `FEISHU_BOT_MODEL` | model |
| `FEISHU_BOT_MENTION_ONLY` | mention_only |

### 3. 行为说明

- **独立运行时**：机器人有自己的 MCP 注册表与协程池，与网关代理互不依赖——网关不可达、
  代理未启动都不影响飞书收发；并发上限同样取 `worker_pool.max_concurrency`，
  队列满时直接在卡片上回复「本地任务队列已满，请稍后再发」；
- **单卡片实时流式回复（飞书卡片 2.0 流式更新模式）**：每条消息只产生一张卡片。
  处理中卡片开启 `streaming_mode`，平台对正文做**原生打字机效果**（增量逐字渲染，
  流式期间的全量更新不计常规频控），刷新节流约 0.7s 且所有更新严格串行；
  卡片分两个区域：
  - **思考与执行过程**（折叠面板）：Claude Code 以 `stream-json --verbose` 运行，
    reasoning 思考链与工具调用（🔧 调用 / ✅❌ 完成）实时进入该面板，进行中自动展开、
    正文开始后自动折叠，过程超过约 4000 字截头保尾；
  - **最终正文**：只承载答案文本（打字机增长，最多约 6000 字），
    头部颜色随阶段变化（处理中蓝 → 完成绿 → 失败红），底部状态行在
    「🧠 正在思考 / 🧰 正在调用工具 / ✍️ 正在输出」间切换，聊天栏预览同步显示摘要；

  思考与工具过程**只在本机卡片展示**，不会进入网关任务结果 / SSE / OpenAI 接口返回；
  卡片正文对裸邮箱做脱敏，避免飞书内容审计以 EMAIL_ADDRESS 拒绝更新；
  **不再发送任何独立的「已收到，处理中…」文本**——卡片建卡瞬间即回执（显示「思考中」），
  每条消息严格只有一张卡片；旧配置文件里残留的 `ack` 字段会被自动忽略，无需手动清理；
- 长连接由 SDK 维护心跳与自动重连（指数退避）；
- **防重复回复（双重保障）**：
  1. 事件回调内置 10 分钟窗口的 message_id 去重，平台超时重推 / 重连重投不会二次执行；
  2. 配置目录下的 `.imbot-feishu-<身份哈希>.lock` 心跳锁保证**同一台机器上同一 App ID 只有一个实例**，
     第二个实例启动会被拒绝并报告占用方 PID；进程崩溃后锁在约 20s 心跳超时后自动释放。
     锁文件按「渠道 + 身份」命名，飞书与企业微信、不同 App ID 之间互不阻塞。

     注意文件锁只管同一台机器。**两台机器（或本机 GUI + 命令行）用同一个 App ID 同时上线**
     仍会各收到一部分消息、表现为「回复两次」——一个应用只在一处部署即可；
- 飞书要求事件处理在 3 秒窗口内 ACK：受理判断（@过滤、去重）同步完成，执行与回复全部异步。

---

## 二、企业微信：智能机器人 API 长连接

企业微信「智能机器人」支持 **API 模式 · 长连接**接入：客户端向
`wss://openws.work.weixin.qq.com` 建立出站 WebSocket，订阅后实时接收消息回调，
并以**流式消息**（Markdown）增量更新回复。无需公网域名、无需配置回调 URL，
也不依赖企业微信官方 Go SDK（客户端直接实现 WebSocket 帧协议，仅用 gorilla/websocket）。

### 1. 企业微信管理后台配置

在[企业微信管理后台](https://work.weixin.qq.com/wework_admin/)：

1. 「安全与管理 → 管理工具 → **智能机器人**」中创建机器人；
2. 启用 **API 模式**，接入方式选择 **「长连接」**；
3. 复制机器人的 **Bot ID** 与 **Secret**；
4. 配置可见范围并发布，成员即可在单聊中找到机器人、或把它加进群并 @ 它。

### 2. 客户端配置

编辑 `configs/agent.yaml`（或 Windows GUI「配置」页的「企业微信机器人」分组）：

```yaml
bots:
  wecom:
    enabled: true
    bot_id: "bot_xxxxxxxxxxxxxxxx"
    secret: "xxxxxxxxxxxxxxxx"          # 建议改用环境变量 WECOM_BOT_SECRET
    model: ""                           # 空=claude-code，可选 trae/codebuddy/codex
    mention_only: true                  # 群聊仅响应 @机器人（单聊始终响应）
    system_prompt: ""                   # 可选，拼在每条消息前
```

保存后在「概览」页「IM 机器人」卡的「企业微信」一行点「启动」；配置页的「测试连接」
会完成一次「拨号 + 订阅握手」（约 12s 超时），校验 Bot ID / Secret 是否被平台接受，
**不会保持长连接**（校验的是已保存配置，改完先保存）。

支持的环境变量（优先级高于 yaml）：

| 变量 | 对应字段 |
|---|---|
| `WECOM_BOT_ID` | bot_id |
| `WECOM_BOT_SECRET` | secret（推荐，避免密钥落盘） |
| `WECOM_BOT_ENABLED` | enabled（`true`/`1`） |
| `WECOM_BOT_MODEL` | model |
| `WECOM_BOT_MENTION_ONLY` | mention_only |

### 3. 行为说明与平台限制

- **独立运行时**：与飞书渠道相同，企微机器人拥有独立的 MCP 注册表、协程池与单实例锁，
  代理未启动 / 网关不可达 / 飞书渠道未启用都不影响它；队列满时直接回复「本地任务队列已满，请稍后再发」；
- **流式回复（`aibot_respond_msg` + `msgtype: stream`）**：每条消息只有一条流式消息，
  帧头 `req_id` 必须透传回调帧的 `req_id`，因此**只有被动回复支持流式**；
  AI 处理过程中按约 **2s** 节流串行 PATCH（平台频控比飞书严），内容为 Markdown：
  - **思考与工具过程**渲染为灰色引用区（`<font color="comment">` 引用块），
    reasoning 与工具调用实时追加，超过约 1500 字截头保尾；
  - **正文开始生成后，过程区自动折叠**为最后几行的摘要置于正文之前；
  - 失败时摘要以警告色（`color="warning"`）显示；
  - 单帧内容上限 **20480 字节**（按 UTF-8 字节截头保尾，不会切断 emoji），
    整条流式消息必须在 **10 分钟**内 `finish`，限流约 30 条/分钟、1000 条/小时；
- **连接维护**：每 30s 发 `ping`，连续 2 次无 ACK 判定连接死亡并重连
  （指数退避 1s→30s）；收到平台 `disconnected_event`（同一 Bot ID 在别处登录被顶替）
  会立即重连——同一 Bot ID **全平台只允许一条长连接**；
- **主动推送**（如系统通知）走 `aibot_send_msg`，不带 `req_id`、**不支持 stream**，
  单聊 `chatid` 即用户 userid；
- **防重复回复**：消息按 `msgid` 去重；配置目录下的
  `.imbot-wecom-<身份哈希>.lock` 心跳锁保证同机同 Bot ID 单实例（约 20s 超时自动释放）。
  跨机器同 Bot ID 部署仍会互踢/重复，需在平台侧保证只部署一处。

---

## 三、验证与排查

**飞书长连接模式：**

| 现象 | 检查项 |
|---|---|
| 日志无 `feishu bot started` | 概览页机器人服务是否已启动；`bots.feishu.enabled` 是否为 true；App ID/Secret 是否齐全 |
| 启动即报锁占用（另一实例 PID） | 已有一个客户端/命令行实例在跑同一 App ID；停掉其一，或等约 20s 让崩溃实例的心跳锁过期 |
| **每条消息收到两条回复 / 执行两次** | 是否有两台机器或本机 GUI + 命令行用同一 App ID 同时在线（文件锁只管同机）；日志出现 `duplicated im event ignored` 则属平台重推被正常去重，无需处理 |
| 日志反复 `feishu bot exited` | App ID/Secret 是否正确；应用是否已发布；机器能否访问公网 |
| 「测试连接」失败 | 先用配置页「测试连接」校验凭证（错误会带飞书返回码，如 99991663=Secret 错误） |
| 连上了但消息无反应 | 是否订阅 `im.message.receive_v1`；群聊是否 @了机器人（mention_only）；事件订阅是否选了「长连接」 |
| 受理后无卡片 / 提示发卡片失败 | 发消息权限是否开通（`im:message:send_as_bot` 等）；本机 AI CLI 是否已登录 |
| 没有打字机效果/卡片整块刷新 | 飞书客户端需 7.20+（卡片 JSON 2.0）；旧版会显示升级提示。确认 claude 参数为 `--output-format stream-json --verbose`（旧 json 参数会在启动时自动迁移，自定义过需手动改） |
| 看不到思考过程/工具调用 | 仅 Claude Code 支持：必须 `stream-json --verbose`（默认参数已含）；其他 CLI 只显示正文 |
| 卡片一直「处理中…」不结束 | 查看本机 AI 执行日志；单任务上限约 15 分钟，超时卡片会置为「执行失败」 |
| 只收到「队列已满」 | 降低 `max_concurrency` 之外的任务负载，或调大 `worker_pool.queue_size` |

**企业微信长连接模式：**

| 现象 | 检查项 |
|---|---|
| 日志无 `im bot started`（channel=wecom） | 概览页「IM 机器人」卡企微行是否已启动；`bots.wecom.enabled` 是否为 true；Bot ID/Secret 是否齐全 |
| 「测试连接」提示企业微信拒绝凭证（errcode 60011 等） | Bot ID / Secret 是否复制正确；机器人是否已启用 API 模式并发布到可见范围 |
| 订阅超时（12s 内未收到回执） | 本机网络 / 出站代理能否访问 `openws.work.weixin.qq.com:443`；防火墙是否放行 WebSocket 升级 |
| 日志反复 `im bot exited`（channel=wecom） | 同一 Bot ID 是否在另一台机器 / 另一个客户端上连着（平台只允许一条连接，会推 `disconnected_event` 顶替） |
| 启动即报锁占用（另一实例 PID） | 同机已有一个实例在跑同一 Bot ID；停掉其一，或等约 20s 让崩溃实例的心跳锁过期 |
| 连上了但消息无反应 | 群聊是否 @了机器人（mention_only=true 时仅响应 @）；机器人是否在群内、可见范围是否包含发送者 |
| 流式消息中途停止更新 | 单条流必须在 10 分钟内 finish；单帧 content 不超过 20480 字节；检查是否触发 30 条/分钟频控，必要时调大客户端节流 |
| 主动通知发不出去 | 主动推送（`aibot_send_msg`）不支持流式且必须带有效 chatid（单聊为 userid）；流式回复只能用于被动消息回调 |

---

## 四、安全建议

- 客户端模式的渠道密钥（飞书 App Secret、企业微信 Secret）优先用环境变量
  （`FEISHU_APP_SECRET` / `WECOM_BOT_SECRET`）注入；写入 yaml 时文件权限为 0600，
  勿提交到仓库。GUI 与 IPC 状态只回报「已配置」布尔状态与脱敏的身份标识，绝不回传密钥。
