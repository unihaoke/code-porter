# 贡献指南

首先感谢你对 CodePorter 的兴趣！无论是提 Issue、修文档、报 Bug 还是写代码，都非常欢迎。
请花几分钟读完本指南，能让你的贡献更快被合入。

## 行为准则

参与本项目即表示你同意遵守 [行为准则](CODE_OF_CONDUCT.md)。遇到骚扰或不当行为请按其中方式联系维护者。

## 可以贡献什么

- **Bug 反馈**：使用 [Bug 报告模板](https://github.com/unihaoke/code-porter/issues/new?template=bug_report.yml)，附复现步骤与日志（注意脱敏）。
- **功能建议**：使用 [功能建议模板](https://github.com/unihaoke/code-porter/issues/new?template=feature_request.yml)，先说清楚场景与痛点。
- **文档改进**：错别字、过时内容、翻译与排版修正，直接提 PR 即可。
- **代码贡献**：功能开发 / Bug 修复建议先在 Issue 中对齐方案，避免返工。

## 开发环境

| 依赖 | 版本 | 说明 |
|---|---|---|
| Go | 1.23+ | 网关与本地 Agent（`backend/`） |
| Node.js | 20.19+ | 网页控制台与 Electron 客户端，固定 Vite 6.x，**不要降到 5.x** |
| Docker | 24+ | 仅本地一键部署网关时需要 |
| MySQL | 8.x | 集成测试可选（无 DSN 时相关用例自动 skip） |

初始化与常用命令：

```bash
make init            # go mod tidy + 安装前端依赖
make run-gateway     # 启动网关 :9022（先 make web 托管控制台）
make dev-web         # 控制台热更新 :5173
make run-agent       # 命令行方式启动本地 Agent
make fmt vet test    # Go 格式化 / 静态检查 / 全部测试
```

桌面客户端开发：

```bash
cd client
npm install
npm run dev:core     # 一个终端：watch 编译 Go 核心到 backend/bin/
npm run dev:renderer # 另一个终端：Vite 热更新
```

## 提交前检查（CI 以同一套命令为准）

```bash
# 后端（在 backend/ 下）
go build ./... && go vet ./... && go test ./...

# 网页控制台（在 frontend/ 下）
npm run typecheck && npm run build

# 桌面客户端（在 client/ 下）
npm run typecheck && npm run build:main && npm run build:renderer
# 改了 Go 代码还要重编 Electron 用的核心：
node scripts/build-core.mjs
```

Windows PowerShell 不支持 `&&` 时请改用 `;` 或分步执行。
本机访问 `127.0.0.1` 若被代理拦截返回 502，用 `curl --noproxy '*'`。

跑 MySQL 集成测试：

```bash
export CODEPORTER_TEST_MYSQL_DSN='root:root@tcp(127.0.0.1:3306)/codeporter_test?parseTime=true'
cd backend && go test ./...
```

## 代码约定

- **分层方向严格单向**：`infrastructure → application → domain`；领域层零第三方依赖。
- HTTP handler 只做协议转换，业务逻辑放在 application 用例；出站端口定义在 `application/port`，实现放 infrastructure。
- 领域对象字段私有，经 `NewXxx(Spec)` 构造、访问器读取、`Clone()` 复制。
- Go 代码必须 `gofmt`；导出标识符有注释；新增/修改逻辑请补单元测试（领域状态机、队列、适配器均有现成测试风格可参考）。
- 前端只与网关 `/api/*` 通信，不直连 Agent；客户端界面只调 store action，不直接碰 IPC。
- 新增客户端 IPC 功能时，Go 核心 ↔ `shared/types.ts` ↔ 主进程 ↔ preload ↔ 视图五层必须同步；
  字段命名一律 **snake_case 且与 yaml 标签一致**。详见 [AGENTS.md](AGENTS.md)。
- **禁止提交**：秘钥、App Secret、`.env`、打包产物（`client/release/`、`backend/bin/`）与本地截图。

## Commit 与 PR 规范

Commit message 使用简洁的祈使句，建议带类型前缀：

```
feat: 新增本地 AI 工具手动预热
fix: 修复 WebSocket 上下文提前取消导致的重连风暴
docs: 更新部署文档的 HTTPS 章节
test: 补充 MCP 预热单测
refactor: 抽离秘钥权限交集逻辑
chore: 升级 vite 到 6.4.3
```

提 PR 时：

1. 从最新 `master` 切分支，一个 PR 只做一件事；
2. 填写 [PR 模板](.github/PULL_REQUEST_TEMPLATE.md) 的清单，确认 CI 全绿；
3. 关联对应 Issue（`Closes #123`）；
4. UI 变更请附截图；接口 / 配置变更请同步更新 [docs/](docs/) 与示例配置；
5. 保持 Review 友好：变更较大时先开 Draft PR 或在 Issue 中讨论方案。

## 发布节奏

- 三平台桌面客户端发版：push `v*` tag 自动触发 [Release Client](.github/workflows/release-client.yml)；
- 用户可见变更请追加到 [CHANGELOG.md](CHANGELOG.md) 的「Unreleased」节，遵循 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/) 格式。

## 报告安全问题

请**不要**提公开 Issue 或 PR，按 [SECURITY.md](SECURITY.md) 私下联系维护者。
