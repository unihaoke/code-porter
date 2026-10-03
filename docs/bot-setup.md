# IM 机器人接入指南

支持两个渠道：**飞书**与**企业微信**。两者都包含两件事：

1. **出站 Webhook** —— 把本地 AI 的结果发回群里（必填，否则只能收不能回）；
2. **入站回调** —— 群里 @机器人 的消息送进网关（需把回调地址配到 IM 平台）。

---

## 一、在控制台创建机器人

进入控制台「机器人」→「新建机器人」：

| 字段 | 说明 |
|---|---|
| 名称 | 便于识别即可 |
| 渠道 | 飞书 / 企业微信 |
| 默认模型 | 该机器人用哪个本地 AI 工具；留空用网关默认 |
| 投递通路 | 群聊建议「队列模式」，离线也不丢任务 |
| 出站 Webhook | 群机器人地址（见下文获取方式） |
| Token / AESKey / Secret | 回调安全凭据，见下文各渠道说明 |
| 附加系统提示 | 每次请求都会拼在用户问题前，例如「回答请简洁」 |
| 仅响应 @机器人 | 群聊中避免被无关消息触发 |

保存后列表中会显示**回调地址**，点「复制」，下一步要填到 IM 平台。

---

## 二、飞书

### 1. 获取出站 Webhook

群设置 → 群机器人 → 添加「自定义机器人」→ 复制 Webhook 地址
（形如 `https://open.feishu.cn/open-apis/bot/v2/hook/xxxxxxxx`）。

- 若机器人开启了「签名校验」，把密钥填到控制台的**签名 Secret**；
- 未开启则留空。

### 2. 配置入站回调（需要自建应用）

在[飞书开放平台](https://open.feishu.cn/app)创建**企业自建应用**，开启「机器人」能力：

1. 事件订阅 → 订阅方式选「将事件发送至开发者服务器」，填入控制台给出的回调地址
   （形如 `https://cp.example.com/webhook/feishu/bot_xxx`）；
2. 添加事件 **`im.message.receive_v1`（接收消息）**；
3. 若开启了 Encrypt Key，把该密钥填到控制台的 **AESKey**（43 位 Base64，留空表示不加密）；
4. Verification Token 填到控制台的 **Token**（填了就会校验，不匹配会拒绝）；
5. 权限管理里申请 `im:message`、`im:message:send_as_bot` 等消息权限，并发布版本；
6. 把机器人加进群，群里 @它 即可触发。

> 网关在收到 `url_verification` 时会原样回显 `challenge`，飞书据此判定地址有效。

---

## 三、企业微信

### 1. 获取出站 Webhook

群设置 → 群机器人 → 添加机器人 → 复制 Webhook 地址
（形如 `https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=xxxxxxxx`）。

### 2. 配置入站回调

企业微信接收消息需要**自建应用**（或支持回调的机器人）的回调配置：

1. 管理后台 → 应用 → 接收消息 → 设置 API 接收，URL 填控制台给出的回调地址
   （形如 `https://cp.example.com/webhook/wecom/bot_xxx`）；
2. 随机获取 **Token** 与 **EncodingAESKey**，分别填到控制台的 Token 与 AESKey；
3. 保存时企业微信会发起 GET 验证，网关会校验签名并解密 `echostr` 后原样返回明文；
4. 在群里 @机器人 即可触发。

> 企业微信回调**强制加密**，因此 Token 与 AESKey 二者缺一不可；
> 缺任一字段时机器人的 `can_receive` 为 false，消息会被忽略。

---

## 四、验证与排查

**快速自测回调地址是否可达：**

```bash
curl -X POST https://cp.example.com/webhook/feishu/bot_xxx \
  -H 'Content-Type: application/json' \
  -d '{"type":"url_verification","challenge":"ping","token":""}'
# 期望返回 {"challenge":"ping"}
```

**在群里 @机器人 后没有回复？** 按顺序排查：

| 现象 | 检查项 |
|---|---|
| 消息根本没到网关 | 回调地址是否公网可达（必须 https）、路径与 bot_id 是否一致 |
| 网关日志报签名/Token 不匹配 | 控制台与 IM 平台的 Token / AESKey 是否一致 |
| 受理了但群里没消息 | 出站 Webhook 是否填写；飞书签名 Secret 是否正确 |
| 群里只有「超时」提示 | 本地 Agent 是否在线、本机 AI 工具是否已登录 |
| 群聊没 @ 也触发 | 关闭「仅响应 @机器人」，或反之 |

网关日志关键字：`bot message accepted`、`bot replied`、`reply to im failed`。

---

## 五、安全建议

- 回调地址必须用 HTTPS（HTTP 会被 IM 平台拒绝，也容易被窃听）；
- 生产环境务必填写 Token / AESKey，避免任何人都能向你的网关投递消息；
- 群机器人 Webhook 等同于「往群里发消息的钥匙」，泄露后任何人都能往群里推送，请妥善保管；
- 控制台不会回传密钥明文，填错只能重新填一次覆盖。
