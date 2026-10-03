# 接口文档

所有请求/响应均为 JSON（`Content-Type: application/json`），时间字段为 Unix 秒。

## 鉴权

| 接口族 | 鉴权方式 |
|---|---|
| `/v1/*`、`/admin/*` | `Authorization: Bearer <api_key>` |
| `/agent/*` | `X-Agent-Token: <agent_token>` |
| `/api/*` | `X-Admin-Token: <admin_token>`（也接受 `Authorization: Bearer` 或 `?token=`） |
| `/webhook/*` | 由渠道自己的签名 / Token 校验，不使用网关鉴权 |

`admin_token` 未配置时回落为第一个 `api_key`。

错误响应统一为 OpenAI 风格：

```json
{ "error": { "message": "invalid admin token", "type": "unauthorized", "code": "unauthorized" } }
```

错误码与 HTTP 状态码映射：`invalid_param`→400、`unauthorized`→401、`not_found`→404、
`queue_full`/`rate_limited`→429、`timeout`→504、`not_connected`/`unavailable`→503、其余→500。

---

## OpenAI 兼容接口

### `POST /v1/chat/completions`

请求体（标准 OpenAI 字段）：

```json
{
  "model": "claude-code",
  "messages": [{ "role": "user", "content": "帮我找出这段代码的内存泄漏" }],
  "stream": true
}
```

请求头：`x-codeporter-mode: pull | direct`（默认 `pull`）。

`stream=false` 返回完整的 `chat.completion`；`stream=true` 返回 SSE（`data: {...}` + `data: [DONE]`）。

---

## 网页控制台接口

### `POST /api/auth/login`

校验管理端令牌，前端据此决定是否保存。

```json
{ "ok": true, "auth_required": true }
```

### `POST /api/chat`

```json
{
  "messages": [{ "role": "user", "content": "重构这个函数" }],
  "model": "claude-code",
  "mode": "pull",
  "stream": true
}
```

流式响应为 SSE 事件序列：

```
event: meta
data: {"task_id":"task_x","model":"claude-code","mode":"pull","agent":"local-pc"}

event: chunk
data: {"content":"建议提取函数"}

event: done
data: {"content":"建议提取函数并补充单元测试"}
```

`stream=false` 时返回：

```json
{ "task_id": "task_x", "model": "claude-code", "mode": "pull", "content": "……" }
```

### `GET /api/overview`

```json
{
  "agents": 1,
  "agents_online": 1,
  "ws_conns": 1,
  "queues": { "local-pc": 0 },
  "tasks": { "pending": 0, "running": 1, "success": 3 },
  "bots": 2,
  "bots_enabled": 1,
  "models": [{ "model": "claude-code", "available": true, "agents": [] }]
}
```

### `GET /api/agents`

返回节点列表：状态、最近心跳、队列长度、CPU/内存、已接入的 AI 工具。

### `GET /api/tasks?limit=50&agent_id=local-pc`

返回最近任务：ID、来源、模型、通路、状态、重试次数、时间、请求预览。

### `GET /api/models`

返回全部受支持的本地 AI 工具及其在各节点上的可用状态。

### `GET /api/bots`

```json
{
  "bots": [{
    "id": "bot_xxx",
    "name": "研发群助手",
    "channel": "feishu",
    "channel_name": "飞书",
    "enabled": true,
    "model": "claude-code",
    "mode": "pull",
    "webhook_url": "https://open.feishu.cn/open-apis/bot/v2/hook/demo****ey",
    "has_secret": true,
    "has_token": false,
    "has_aes_key": false,
    "callback_url": "https://cp.example.com/webhook/feishu/bot_xxx",
    "can_receive": true,
    "can_reply": true
  }]
}
```

> 密钥类字段不会回传明文，只返回 `has_*` 标志；Webhook 中的 key 已做掩码。

### `POST /api/bots`

```json
{
  "name": "研发群助手",
  "channel": "feishu",
  "model": "claude-code",
  "mode": "pull",
  "webhook_url": "https://open.feishu.cn/open-apis/bot/v2/hook/xxx",
  "secret": "签名密钥（飞书）",
  "token": "Verification Token（飞书）/ 回调 Token（企微）",
  "aes_key": "43 位 Base64 加密密钥",
  "system_prompt": "回答请简洁",
  "mention_only": true,
  "enabled": true
}
```

创建成功返回 `201`。`aes_key` 必须是 43 位 Base64，否则返回 `invalid_param`。

### `PUT /api/bots/{id}`

局部更新：留空的字段保持不变；`aes_key` 传占位值表示不修改（前端用 `__unchanged__`）。

### `POST /api/bots/{id}/toggle`

```json
{ "enabled": false }
```

### `DELETE /api/bots/{id}`

删除后对应回调地址立即失效。

---

## IM 回调接口

### `POST /webhook/feishu/{bot_id}`

- 配置回调地址时飞书会发 `url_verification`，网关回显 `{"challenge":"..."}`；
- 消息事件（`im.message.receive_v1`）中 `message_type=text` 的消息会被受理；
- 开启 Encrypt Key 时请求体为 `{"encrypt":"..."}`，网关用 `SHA256(AESKey)` 作密钥解密。

响应：

```json
{ "ok": true, "accepted": true, "ignored": false, "task_id": "task_xxx", "reason": "" }
```

### `GET|POST /webhook/wecom/{bot_id}`

- `GET`：URL 验证，校验签名后解密 `echostr` 并**原样返回明文**（不包 JSON）；
- `POST`：接收加密消息 XML，解密后取 `MsgType=text` 的 `Content`。

查询串需带 `msg_signature`、`timestamp`、`nonce`。
企业微信的回调强制加密，机器人必须配置 `token` 与 `aes_key`，否则 `can_receive=false`。

---

## LocalAgent 内部接口

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/agent/pull?agentId=&maxBatch=` | 拉取任务（同时作为心跳） |
| POST | `/agent/ack` | 上报片段 / 成功 / 失败 / 释放 |
| POST | `/agent/health` | 上报本机健康与 MCP 可用性 |
| GET | `/agent/ws` | WebSocket 直连（直连模式） |

ACK 请求体：

```json
{
  "task_id": "task_xxx",
  "agent_id": "local-pc",
  "lock_token": "lock_xxx",
  "status": "progress|success|failed|released",
  "chunks": [{ "seq": 1, "content": "第 3 行 conn 未关闭" }],
  "result": "最终结果（status=success 时使用）",
  "error": "失败原因"
}
```

## 运维接口

- `GET /healthz` —— `{"ok":true,"service":"codeporter-gateway","ws_conns":1}`
- `GET /admin/agents`、`GET /admin/queues`（需 API Key）
