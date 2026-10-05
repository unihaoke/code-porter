# 安全策略

## 支持的版本

| 版本 | 安全修复 |
|---|---|
| 最新 master | ✅ |
| 最新 Release | ✅ |
| 更早版本 | ❌（请升级） |

## 报告漏洞

**请不要通过公开 Issue、PR 或讨论区报告安全问题。**

请通过 GitHub 的 [Security Advisories（私密漏洞报告）](https://github.com/unihaoke/code-porter/security/advisories/new) 提交，
我们会在收到后尽快确认并回复处理计划。

报告时请尽量包含：

1. 漏洞类型与影响范围（如越权、密钥泄露、RCE、注入等）；
2. 可复现的步骤、PoC 或关键代码位置；
3. 受影响的版本 / commit 与部署形态（Docker / 客户端 / 命令行）；
4. 你认为合适的修复建议（可选）。

### 响应时限（目标）

- **48 小时**内确认收到；
- **7 天**内给出初步评估与处理计划；
- 高危问题修复发布后会在 [CHANGELOG.md](CHANGELOG.md) 与 Release 说明中致谢（如你愿意）。

## 安全设计约定

CodePorter 在架构上遵循以下安全边界，审计或使用时可重点关注：

- **LocalAgent 只出站、不监听**：不开放任何入站端口，不做端口映射即可工作。
- **鉴权分层**：网页会话（可吊销令牌）、API 秘钥（bcrypt 无关，仅存 SHA-256 哈希）、
  Agent Token + 实例 ID 三者隔离；秘钥权限取「秘钥权限」与请求头
  `X-CodePorter-Permission` 的最严交集，非法权限值一律 fail-closed。
- **密钥不出本机**：飞书 App Secret 只存在本地配置 / 环境变量，绝不经网关或 IPC 明文回传，
  界面只展示「已配置」布尔状态。
- **权限强制**：CLI 模式通过 `--permission-mode` / `--sandbox` 硬强制 read/write；
  MCP stdio 模式无沙箱能力，仅靠提示词守卫软约束，高安全要求场景应使用 CLI 模式。
- **生产部署**：网关必须置于 HTTPS 反向代理之后；默认管理员密码首次登录必须修改。

## 加固建议

- 使用强 MySQL 密码并通过 `MYSQL_DSN` 注入，不要把 `.env` 提交进仓库；
- 定期轮换秘钥，离开项目的成员及时在「用户管理」中停用；
- 关注依赖更新：仓库配置了 Dependabot 与 `npm audit`（需用官方源
  `npm audit --registry=https://registry.npmjs.org`）。
