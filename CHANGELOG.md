# 更新日志

本项目的所有重要变更都会记录在此文件中。

格式遵循 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，
版本号遵循 [语义化版本](https://semver.org/lang/zh-CN/)。

## [Unreleased]

### Added

- **企业微信机器人（智能机器人 API 长连接）**：新增 `bots.wecom` 配置与环境变量
  （`WECOM_BOT_ID/SECRET/MODEL/ENABLED/WECOM_BOT_MENTION_ONLY`）；客户端以出站
  WebSocket 直连 `openws.work.weixin.qq.com`（不引入 Go SDK / 新依赖），支持订阅握手、
  30s 心跳、断线退避重连、`disconnected_event` 顶替感知、凭证测试；回复走
  `aibot_respond_msg` **流式 Markdown**（思考/工具过程渲染为灰色引用区，正文生成后折叠为
  摘要，失败红色提示，单帧 ≤20480 字节、10 分钟内 finish）；主动推送走 `aibot_send_msg`。
- **IM 机器人渠道可扩展架构**（简单工厂 + 策略）：`infrastructure/imbot` 工厂统一构造渠道
  runner；端口层新增 `IMReplyTarget`（ChatID + ReplyToken）、`IMMessageHandler`、
  `IMBotCredentialTester`、`IMBotCardPacer` 能力接口；消息编排收敛到渠道无关的
  `application/agent/IMBotService`（卡片节拍按渠道 pacer 取值：飞书 0.7s / 企微 2s）；
  `pkg/lockfile` 提供按「渠道 + 身份」命名的通用心跳单实例锁；单测覆盖协议帧、渲染、
  runner（httptest 模拟服务器）、工厂、锁。
- 客户端「概览」页机器人改为多渠道卡（飞书 / 企业微信各一行独立启停），「配置」页新增
  企业微信机器人卡片（Bot ID / Secret / 模型 / 仅响应 @ / 系统提示 / 测试连接）；
  IPC `bot.start` / `bot.stop` 支持 `{"channel":...}` 参数，`bot.test` 按渠道测试。
- 概览页「本地 AI 工具」卡片新增独立的**启动 / 停止**按钮：仅预热配置中已启用的工具
  （MCP 模式拉起常驻子进程并完成握手，CLI 模式做免额度可执行性探测），开机不自动启动；
  该运行时与代理、机器人三者独立启停。
- GitHub 社区与工程化文件：CI 工作流（Go + 网页控制台 + Electron 客户端）、
  Issue / PR 模板、贡献指南、安全策略、行为准则。
- README 双语化：`README.md` 为英文默认版，新增中文 `README.zh-CN.md`，两份顶部互链。
- 新增项目级 skill `github-oss-setup`（开源标准化流程 + Markdown 链接检查脚本）。

### Changed

- 飞书机器人完全运行在**本地客户端**（官方 SDK 长连接），消息本机闭环、单卡片流式输出；
  与代理服务、企业微信渠道各自拥有独立的生命周期、协程池、MCP 注册表与单实例锁；
  同机单实例锁从 `.feishu-bot.lock` 切换为 `pkg/lockfile` 的
  `.imbot-feishu-<身份哈希>.lock`。
- Electron 客户端「保存并重启本地 AI」会一并重启正在运行的本地 AI 工具运行时，
  机器人仅重启当前在运行的渠道（未运行的渠道不擅自启动）。

### Fixed

- 企业微信流式回复的超时收口窗口改为「平台流式寿命 - 20s」，避免长任务超时收口的
  终帧越过 10 分钟硬限被平台丢弃。
- 修复企微 runner 终态帧写入失败时流式登记条目不释放、断线重连后旧回调 req_id
  可能被带到新连接续发的问题（终态必注销、订阅成功即重置登记表）。
- 单实例锁改为 `O_CREATE|O_EXCL` 原子占用，消除跨进程并发启动的 check-then-write
  竞态；停止机器人时若任务池 20s 内未排空，则只放弃心跳（锁 20s 后自然失效）而非
  立即删锁，避免残留任务与新实例短暂双连接。
- 聚合启动（`bot.start` 不带 channel）现在逐渠道返回失败原因，单个渠道失败不影响
  其他渠道启动。
- 飞书「测试连接」在网络错误/响应解析失败时返回完整中文详情（原先可能出现空前缀文案）。

### Removed

- **移除网关侧全部机器人能力**：`/api/bots` CRUD、`/webhook/{feishu,wecom}` 回调、
  bots 数据表（迁移 0003）、控制台机器人菜单；机器人仅保留在本地客户端。
- 移除飞书独立受理文本（「已收到，正在本地处理中…」），流式卡片本身即回执。
- 移除废弃的 `server.public_addr` 配置与旧 `bot:` 配置节。

## v0.2.0 — 2026-10-05

### Added

- 多租户账号体系：账号密码登录、用户管理（admin）、服务端可吊销会话令牌。
- 连接秘钥：agent / api 双 scope、有效期、三级执行权限（read / write / all），
  权限取秘钥与请求头的最严交集，非法值 fail-closed；CLI 模式用沙箱参数硬强制。
- 本地客户端飞书长连接机器人：@机器人触发、消息去重、同机单实例文件锁、
  单张流式卡片（card JSON 2.0，思考过程实时推送）。
- Electron 图形客户端：概览 / 配置 / 日志三视图，代理与机器人独立控制，
  「测试 CLI 连接」「测试机器人凭证」「保存并重启本地 AI」。
- Agent 重连退避（1s–30s，短连接也退避），运行时在线状态内存缓存 + 身份信息落 MySQL。
- OpenAI 兼容接口 `/v1/chat/completions`（支持 stream 与指定实例）。
- 网页控制台：对话（SSE 流式）、秘钥、本地节点、任务、概览、用户管理。
- Docker Compose 一键部署（MySQL 8 + 网关 + 控制台），首次启动自动迁移与种子管理员。
- CLI / MCP 双调用模式适配 Trae、Claude Code、CodeBuddy、Codex；
  Claude Code stream-json 输出思考 / 工具过程并在飞书卡片折叠展示。
- Windows 免安装单文件客户端与三平台 GitHub Actions 发版流水线。

## v0.1.0 — 2026-10-04

### Added

- 项目初始化：DDD 分层的 Go 网关与本地 Agent，Pull 队列 + WebSocket 直连双通路，
  Vue 3 网页控制台与基础任务链路。

[Unreleased]: https://github.com/unihaoke/code-porter/compare/v0.2.0...HEAD
