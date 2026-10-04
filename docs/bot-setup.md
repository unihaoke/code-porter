# IM 机器人接入指南

| 渠道 | 推荐接入方式 | 运行位置 | 是否需要公网域名 |
|---|---|---|---|
| 飞书 | **客户端长连接**（官方 SDK WebSocket） | 本地 Agent 进程 | 否 |
| 飞书 | 自定义机器人 Webhook（旧方式，备选） | 网关 | 是 |
| 企业微信 | 群机器人 Webhook + 应用回调 | 网关 | 是 |

飞书支持「长连接接收事件」：Agent 主动向飞书建立出站 WebSocket，消息直接在本机调用
AI 工具处理、再通过 OpenAPI 回复，**不经过网关队列，也不需要公网域名、回调验签与
Encrypt Key**。企业微信没有长连接模式，仍需网关具备公网回调地址。

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

编辑客户端的 `configs/agent.yaml`（或 Windows GUI「飞书机器人」分组）：

```yaml
bots:
  feishu:
    enabled: true
    app_id: "cli_xxxxxxxxxxxxxxxx"
    app_secret: "xxxxxxxxxxxxxxxx"   # 建议改用环境变量 FEISHU_APP_SECRET
    model: ""                        # 空=claude-code，可选 trae/codebuddy/codex
    mention_only: true               # 群聊仅响应 @机器人（私聊始终响应）
    ack: true                        # 先回一条「处理中…」
    system_prompt: ""                # 可选，拼在每条消息前
```

支持的环境变量（优先级高于 yaml）：

| 变量 | 对应字段 |
|---|---|
| `FEISHU_APP_ID` | app_id |
| `FEISHU_APP_SECRET` | app_secret（推荐，避免密钥落盘） |
| `FEISHU_BOT_ENABLED` | enabled（`true`/`1`） |
| `FEISHU_BOT_MODEL` | model |
| `FEISHU_BOT_MENTION_ONLY` | mention_only |

### 3. 行为说明

- 消息到达后在**本机直接执行**，复用 Agent 的协程池与已配置的 MCP/CLI 工具，
  与网关任务共用并发上限（`worker_pool.max_concurrency`）；队列满时回复「队列已满，请稍后再发」；
- 默认先回一条「已收到，正在本地处理中…」，任务结束后推送卡片结果；
- 长连接由 SDK 维护心跳与自动重连，网关重启/不可达不影响飞书机器人；
- 同一应用最多 50 条长连接，多实例部署同一 App 时消息为集群投递（只有一个实例收到）；
- 事件处理必须在飞书 3 秒窗口内 ACK，客户端内置 message_id 去重，平台重推不会重复执行。

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
| Agent 日志无 `feishu bot enabled` | `bots.feishu.enabled` 是否为 true；App ID/Secret 是否齐全 |
| 日志反复 `feishu long connection exited, reconnecting` | App ID/Secret 是否正确；应用是否已发布；机器能否访问公网 |
| 连上了但消息无反应 | 是否订阅 `im.message.receive_v1`；群聊是否 @了机器人（mention_only） |
| 受理后无结果卡片 | 发消息权限是否开通（`im:message:send_as_bot`）；本机 AI CLI 是否已登录 |
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
