# 接口文档

所有请求/响应均为 JSON（`Content-Type: application/json`），时间字段为 Unix 秒。

## 鉴权

| 接口族 | 鉴权方式 |
|---|---|
| `/v1/*` | `Authorization: Bearer <秘钥明文>`（秘钥需含 `api` scope），也接受 `X-API-Key` |
| `/agent/*` | `X-Agent-Token: <秘钥明文>`（秘钥需含 `agent` scope）+ `X-Agent-ID: <实例ID>` + 可选 `X-Agent-Name`；秘钥也可放 `Authorization: Bearer` |
| `/api/*`（除登录/webhook） | `Authorization: Bearer <会话令牌>`（登录后获得） |
| `/api/users/*`、`/admin/*` | 同上，且要求 **admin 角色** |
| `POST /api/auth/login`、`/webhook/*`、`/healthz` | 无需鉴权（webhook 由渠道签名校验） |

鉴权失败返回 401；已登录但权限不足（如 member 调 admin 接口、秘钥缺少所需 scope）返回 403。

错误响应统一为 OpenAI 风格：

```json
{ "error": { "message": "用户名或密码错误", "type": "unauthorized", "code": "unauthorized" } }
```

多实例未指定目标时返回 409，并附带可选实例清单：

```json
{
  "error": { "message": "multiple agents available, please specify target agent", "code": "conflict" },
  "agents": [{ "id": "agt_ab12", "name": "work-laptop", "status": "online" }]
}
```

错误码与 HTTP 状态码映射：`invalid_param`→400、`unauthorized`→401、`forbidden`→403、
`not_found`→404、`conflict`（含多实例）→409、`queue_full`/`rate_limited`→429、
`timeout`→504、`not_connected`/`unavailable`→503、其余→500。

---

## 账号与会话

### `POST /api/auth/login`

```json
{ "username": "admin", "password": "admin123" }
```

响应（会话令牌请只保存在浏览器本地，不要再展示给他人）：

```json
{ "token": "服务端签发的不透明会话令牌", "expires_at": "2026-10-11T12:00:00Z",
  "user": { "id": "usr_admin_seed", "username": "admin", "role": "admin", "status": "active" } }
```

### `POST /api/auth/logout` / `GET /api/auth/me`

登出使当前会话立即失效；`/me` 返回当前登录用户信息。

### `POST /api/me/password`

```json
{ "old_password": "...", "new_password": "..." }
```

成功后**除当前会话外的其他登录会话全部失效**；管理员重置他人密码同理。

---

## 用户与秘钥（管理接口）

| 方法与路径 | 角色 | 说明 |
|---|---|---|
| `GET /api/users` | admin | 用户列表 |
| `POST /api/users` | admin | `{username,password,role}`，role=admin/member |
| `DELETE /api/users/{id}` | admin | 删除用户（秘钥/会话/实例/机器人外键级联；不能删自己） |
| `POST /api/users/{id}/reset-password` | admin | `{password}`，并踢掉该用户全部会话 |
| `GET /api/keys` `POST /api/keys` `DELETE /api/keys/{id}` | 本人 | 秘钥自助管理 |
| `GET /api/users/{id}/keys` `DELETE /api/users/{id}/keys/{keyId}` | admin | 代管指定用户秘钥 |

创建秘钥请求：

```json
{ "name": "work-laptop", "scopes": ["agent", "api"], "permission": "all", "expires_at": "2026-12-31T23:59:59Z" }
```

`scopes` 缺省为两个都含；`expires_at` 缺省（空串）= 永久。响应中的 `secret`（`cp_` 开头）
**仅本次返回**，之后任何接口都无法再读到明文；列表只返回 `prefix`、scope、有效期与最近使用时间。

`permission` 控制通过该秘钥下发的任务在本地机器上的文件操作上限，与 `scopes` 正交：

| 值 | 含义 | 本地强制方式（CLI 模式） |
|---|---|---|
| `read` | 只读：禁止改文件与副作用命令 | Claude Code/CodeBuddy `--permission-mode plan`；Codex `--sandbox read-only` |
| `write` | 仅工作目录内可写，禁止系统命令 | `acceptEdits`；Codex `--sandbox workspace-write` |
| `all` | 读写与命令执行全开（缺省） | 沿用客户端 yaml 的 `permission_mode`（默认 bypassPermissions） |

> MCP stdio 模式协议本身不带沙箱，此时 read/write 仅通过提示词约束模型（软限制）；
> 需要硬隔离请把对应工具在客户端配置为 `mode: cli`。

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

请求头：

- `x-codeporter-mode: pull | direct`（默认 `pull`）；
- `x-codeporter-agent: <实例ID>`（可选）。不携带时按租户自动路由：名下 0 台→503，
  1 台→自动选择，多于 1 台→409（响应体附实例清单）。
- `x-codeporter-permission: read | write | all`（可选）：把本次调用收紧到指定权限，
  只能在秘钥的 `permission` 上限内收紧、无法提权（read 秘钥即使传 all 仍按 read 执行）；
  缺省取秘钥权限。

`stream=false` 返回完整的 `chat.completion`；`stream=true` 返回 SSE（`data: {...}` + `data: [DONE]`）。

---

## 网页控制台接口

### `POST /api/chat`

```json
{
  "messages": [{ "role": "user", "content": "重构这个函数" }],
  "model": "claude-code",
  "mode": "pull",
  "agent_id": "agt_ab12",
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

> 以上接口统一要求请求头携带 `X-Agent-Token: <秘钥>`（agent scope）、
> `X-Agent-ID: <实例ID>`（pull 也可继续用 `agentId` 查询参数）、可选 `X-Agent-Name`。
> 首次见到新实例 ID 时网关自动注册到秘钥属主名下；同名实例 ID 若属于其他用户返回 403。

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
- `GET /admin/agents`、`GET /admin/queues`（需 admin 会话令牌）
