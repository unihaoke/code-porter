# 开发指南

## 环境要求

- Go 1.23+
- Node.js **20.19+**（前端构建工具 Vite 6 的硬性要求；Docker 构建用 `node:22-alpine`）
- Docker（可选，仅部署时需要）

## 初始化

```bash
make init          # go mod tidy + npm install
```

## 常用命令

```bash
# 后端
make build-backend       # 编译 gateway 与 agent 到 backend/bin/
make run-gateway         # 运行网关（:8080，同时托管控制台）
make run-agent           # 运行本地 Agent
make test                # go test ./...
make vet fmt             # 静态检查 + 格式化

# 前端
make dev-web             # Vite 开发服务器 :5173（/api 代理到 :8080）
make build-frontend      # 构建到 frontend/dist
make web                 # 复制 frontend/dist → backend/web/dist（网关托管目录）
make typecheck           # vue-tsc 类型检查

# 发布
make release             # 交叉编译客户端 → dist/（含下载页）
make release-agent       # 只发客户端

# 容器
make docker-up / docker-down / docker-logs
```

## 本地联调推荐姿势

一个终端跑网关，一个终端跑 agent，一个终端跑前端热更新：

```bash
make build-frontend && make web     # 首次或改完前端后执行
make run-gateway                    # 终端 1：http://127.0.0.1:8080
make run-agent                      # 终端 2：连上网关
make dev-web                        # 终端 3：http://127.0.0.1:5173（热更新）
```

前端开发服务器会把 `/api`、`/v1`、`/webhook`、`/healthz` 代理到 `http://127.0.0.1:8080`，
改后端无需重启前端。

## 目录与分层约定

详见 [AGENTS.md](../AGENTS.md)。核心规则：

- 依赖方向 `infrastructure → application → domain`，领域层零外部依赖；
- 领域对象字段私有，用 `NewXxx(Spec)` + 访问器 + `Clone()`；
- 出站端口定义在 `internal/application/port`，实现在 `internal/infrastructure`；
- HTTP handler 只做协议转换，业务逻辑放用例。

## 测试

```bash
cd backend
go test ./... -count=1
```

现有覆盖：

| 位置 | 覆盖内容 |
|---|---|
| `internal/domain/task` | 状态机、队列容量、TTL、锁超时、背压释放 |
| `pkg/pool` | 并发上限、背压、panic 恢复 |
| `pkg/backoff` | 动态退避 |
| `test/gateway_flow_test.go` | 端到端：提交 → 拉取 → ACK → SSE 回传、队列满 429、锁超时回收 |

新增 HTTP 接口时，建议补端到端用例；手工验证时用 `curl --noproxy '*'`
（本机回环访问要绕过沙箱代理，否则可能返回 502）。

## 手工冒烟示例

```bash
cd backend
go build -o .smoke/gw.exe ./cmd/gateway
mkdir -p .smoke && ./.smoke/gw.exe -config configs/gateway.yaml &

# 控制台登录
curl --noproxy '*' -X POST http://127.0.0.1:8080/api/auth/login \
  -H 'X-Admin-Token: change-me-admin-token'

# 创建一个飞书机器人
curl --noproxy '*' -X POST http://127.0.0.1:8080/api/bots \
  -H 'X-Admin-Token: change-me-admin-token' -H 'Content-Type: application/json' \
  -d '{"name":"测试","channel":"feishu","webhook_url":"https://open.feishu.cn/open-apis/bot/v2/hook/demo"}'

# 模拟飞书的 URL 验证
curl --noproxy '*' -X POST http://127.0.0.1:8080/webhook/feishu/<bot_id> \
  -H 'Content-Type: application/json' -d '{"type":"url_verification","challenge":"ping"}'
```

仓库里另有可直接运行的 Python 冒烟脚本（禁用代理，输出更稳定）：

```bash
python backend/.smoke/bot_smoke.py    # 机器人回调链路
python backend/.smoke/chat_smoke.py   # 网页对话链路（含 SSE）
```

## 发布客户端

```bash
make release VERSION=v0.3.0
```

产物在 `dist/`：

```
dist/index.html      下载页（自动识别访客系统）
dist/SHA256SUMS.txt  校验和
dist/latest.json     机器可读索引
dist/*.zip|*.tar.gz  各平台客户端（含二进制 + 示例配置 + 快速上手说明）
```

把整个 `dist/` 丢到对象存储或 GitHub Pages 即可对外提供下载。

## 常见问题

**`npm install` 后报 `@esbuild/<platform> could not be found`**
这是 npm 跳过了 esbuild 的平台可选依赖（或 install 脚本被拦截）。
用 `--no-save` 补装，**不要**写进 `package.json` —— 平台包写进 dependencies 会让 Linux 构建报 `EBADPLATFORM`：

```bash
# 版本号取 node_modules/esbuild/package.json 中的 version
npm install --no-save --include=optional @esbuild/win32-x64@0.25.12
```

**依赖漏洞扫描**

```bash
npm audit --registry=https://registry.npmjs.org   # 国内镜像源不支持 audit，需指定官方源
```

前端构建依赖固定在 `vite@^6.4.3`：该版本修掉了
`CVE-2026-53632`（launch-editor NTLMv2 泄露）、`CVE-2026-53571`（`server.fs.deny` 绕过）
与 `CVE-2026-39365`（`.map` 路径穿越）等 dev server 漏洞，
**不要降回 5.x**——5.4.x 分支没有对应补丁。

**改了前端但页面没变化**
执行 `make build-frontend && make web`，并确认 `gateway.yaml` 的 `web.static_dir` 指向 `web/dist`。

**网关日志出现 gopsutil 报错**
Windows 上探测主机信息会调用 `reg.exe`，被沙箱拦截不影响主流程。

**本机 curl 访问 127.0.0.1 返回 502**
沙箱 HTTP 代理导致，用 `curl --noproxy '*'` 或改用 Python 脚本。
