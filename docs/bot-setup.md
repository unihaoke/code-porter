# IM 机器人接入指南

| 渠道 | 推荐接入方式 | 运行位置 | 是否需要公网域名 |
|---|---|---|---|
| 飞书 | **客户端长连接**（官方 SDK WebSocket） | 本地客户端的机器人服务（与代理独立） | 否 |
| 飞书 | 自定义机器人 Webhook（旧方式，备选） | 网关 | 是 |
| 企业微信 | 群机器人 Webhook + 应用回调 | 网关 | 是 |

飞书支持「长连接接收事件」：客户端主动向飞书建立出站 WebSocket，消息直接在本机调用
AI 工具处理、再通过 OpenAPI 回复，**不经过网关队列，也不需要公网域名、回调验签与
Encrypt Key**。企业微信没有长连接模式，仍需网关具备公网回调地址。

> **机器人服务与代理是两个独立服务**：可以只启动机器人（飞书消息本地闭环）而不启动网关代理，
> 反之亦然。Windows 客户端在「概览」页分别提供「代理服务」和「机器人服务 · 飞书」两张控制卡，
> 改完飞书配置保存后，只需单独重启机器人服务，不用动代理。

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

- **Windows GUI**：到「概览」页在「机器人服务 · 飞书」卡上点「启动机器人」；
  「配置」页的「测试连接」按钮可只校验 App ID / Secret（换取 tenant_access_token），
  不建立长连接——注意它校验的是**已保存**的配置，改完先保存再测；
  改动本地 AI 设置（工作目录、CLI 命令、密钥、工具开关）后，点「本地 AI」卡片里的
  **「保存并重启本地 AI」**即可自动重启当前运行中的代理 / 机器人，无需逐个手动重启；
- **命令行 / IPC**：通过 `bot.start`、`bot.stop`、`bot.status`、`bot.test` 动作控制，
  与 `agent.start/stop` 互不影响。

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
  2. 配置目录下的 `.feishu-bot.lock` 心跳锁保证**同一台机器上同一 App ID 只有一个实例**，
     第二个实例启动会被拒绝并报告占用方 PID；进程崩溃后锁在约 20s 心跳超时后自动释放。

     注意文件锁只管同一台机器。**两台机器（或本机 GUI + 命令行）用同一个 App ID 同时上线**
     仍会各收到一部分消息、表现为「回复两次」——一个应用只在一处部署即可；
- 飞书要求事件处理在 3 秒窗口内 ACK：受理判断（@过滤、去重）同步完成，执行与回复全部异步。

---

## 二、飞书：网关 Webhook（旧方式，备选）

适用于继续使用「控制台 → 机器人」管理的存量部署，需要网关有公网 HTTPS 地址。

1. 控制台「机器人」→ 新建，渠道选飞书，保存后复制**回调地址**
   （形如 `https://cp.example.com/webhook/feishu/bot_xxx`）；
2. 出站结果用群「自定义机器人」Webhook（`/open-apis/bot/v2/hook/...`），
   开启签名校验时把签名密钥填入「签名 Secret」；
3. 开放平台事件订阅选「将事件发送至开发者服务器」并填入回调地址，
   订阅 `im.message.receive_v1`；Encrypt Key / Verification Token 按需填入控制台；
4. 网关收到 `url_verification` 会原样回显 `challenge`。

> 新部署优先使用第一节的客户端长连接；两种方式可共存于不同机器人配置，互不影响。

---

## 三、企业微信（网关 Webhook）

1. 群设置 → 群机器人 → 添加机器人，复制 Webhook
   （`https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=xxxx`）填入控制台；
2. 管理后台 → 自建应用 → 接收消息 → API 接收，URL 填控制台给出的回调地址
   （形如 `https://cp.example.com/webhook/wecom/bot_xxx`）；
3. 随机生成的 **Token** 与 **EncodingAESKey** 填入控制台；
   企微回调强制加密，二者缺一不可（缺则 `can_receive=false`，消息被忽略）。

---

## 四、验证与排查

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

**Webhook 模式：**

```bash
curl -X POST https://cp.example.com/webhook/feishu/bot_xxx \
  -H 'Content-Type: application/json' \
  -d '{"type":"url_verification","challenge":"ping","token":""}'
# 期望返回 {"challenge":"ping"}
```

网关侧日志关键字：`bot message accepted`、`bot replied`、`reply to im failed`。

---

## 五、安全建议

- 客户端模式的 App Secret 优先用环境变量注入；写入 yaml 时文件权限为 0600，勿提交到仓库；
- Webhook 模式必须使用 HTTPS，并填写 Token / Encrypt Key 防止伪造投递；
- 群机器人 Webhook 等同于「往群里发消息的钥匙」，泄露后请立即在 IM 平台重置。
