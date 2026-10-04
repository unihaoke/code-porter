# 部署指南（公网 VPS + Docker Compose）

## 前置条件

- 一台有公网 IP 的 Linux VPS（1 核 2G 即可跑网关，AI 算力在你本机）
- 已安装 Docker 与 Docker Compose 插件（`docker compose version` 可查）
- 一个域名（生产环境强烈建议配 HTTPS）

---

## 一、准备配置

```bash
git clone <你的仓库> code-porter
cd code-porter
cp .env.example .env
```

### 配置优先级（.env > yaml）

同一项配置可以写在 `.env` 或 `backend/configs/*.yaml`，**两边都配时 `.env` 生效**；
`.env` 里没配（或留空）则回退用 yaml。优先级从高到低：

| 优先级 | 来源 | 说明 |
| --- | --- | --- |
| 1（最高） | 进程环境变量 | `docker-compose environment`、手动 `export` |
| 2 | `.env` 文件 | 自动查找：从配置文件所在目录逐级向上 |
| 3（最低） | `*.yaml` | `backend/configs/gateway.yaml`、`agent.yaml` |

规则要点：
- **仅非空才覆盖**：`.env` 里写 `ADMIN_TOKEN=`（空值）视为未配置，仍用 yaml。
- **不写回文件**：覆盖只作用于内存中的配置，`SaveAgent` 写回 yaml 时不会把 `.env` 的值落盘。
- **docker-compose** 会自动读根目录 `.env` 做 `${}` 替换，并已把网关相关变量注入 gateway 容器；
  本地直接 `go run` / 双击 exe 时，Go 会自行从配置文件目录向上找 `.env`。
- 想指定 `.env` 路径：设置 `CODEPORTER_ENV_FILE=/path/to/.env`。

常用变量（完整清单见 `.env.example`）：

**网关 / 数据库**

| 变量 | 覆盖的 yaml 字段 |
| --- | --- |
| `MYSQL_DSN` | 网关 `database.dsn`（compose 中由 MYSQL_USER/PASSWORD 等拼接，手工部署时直接给 DSN） |
| `MYSQL_DATABASE` / `MYSQL_USER` / `MYSQL_PASSWORD` / `MYSQL_ROOT_PASSWORD` | compose 启动 MySQL 8 容器与建库授权 |
| `AUTH_SESSION_TTL` | 网关 `auth.session_ttl`（默认 168h） |
| `GATEWAY_ADDR` | 网关 `server.addr`（监听） |
| `GATEWAY_PUBLIC_ADDR` | 网关 `server.public_addr`（对外 https 地址） |
| `BOT_DEFAULT_MODEL` | 网关 `bot.default_model` |
| `LOG_LEVEL` | 两端 `log.level` |

**本地客户端（在你的开发机上设置，不在 VPS 上）**

| 变量 | 覆盖的 yaml 字段 |
| --- | --- |
| `AGENT_KEY` | 客户端 `agent.key`（控制台创建的 cp_ 秘钥，含 agent scope） |
| `AGENT_ID` | 客户端 `agent.id`（一般留空自动生成） |
| `AGENT_NAME` | 客户端 `agent.name`（留空取主机名） |
| `AGENT_GATEWAY_ADDR` | 客户端 `gateway.addr`（连哪个网关） |
| `ANTHROPIC_API_KEY` / `OPENAI_API_KEY` | 客户端 `secrets.*`（注入 AI 工具子进程） |

> 旧变量 `ADMIN_TOKEN` / `API_KEY` / `AGENT_TOKEN` / `AGENT_TOKENS` 已全部废弃：
> 网关检测到只打印告警、不再生效；客户端检测到旧 token 会直接拒绝启动。

### 直接改 yaml（不使用 .env 时）

编辑 `backend/configs/gateway.yaml`，至少配置数据库连接：

```yaml
server:
  # 网关对外地址，机器人回调 URL 由它拼出，必须公网可达
  public_addr: "https://cp.example.com"

database:
  dsn: "codeporter:强密码@tcp(127.0.0.1:3306)/codeporter?parseTime=true&charset=utf8mb4"

auth:
  session_ttl: 168h
```

客户端编辑 `backend/configs/agent.yaml`：

```yaml
agent:
  id: ""        # 留空，首次启动自动生成 agt_...
  name: ""      # 留空取主机名
  key: "cp_控制台创建的秘钥"
gateway:
  addr: "https://cp.example.com"
```

---

## 二、启动

```bash
docker compose up -d --build
docker compose ps          # mysql 为 healthy，gateway 为 running/healthy
docker compose logs -f gateway
```

会启动两个容器：

| 容器 | 作用 | 端口 |
|---|---|---|
| `codeporter-mysql` | MySQL 8，账号/秘钥/会话/实例/机器人持久化（mysql-data 卷） | 仅容器网络内 |
| `codeporter-gateway` | Go 网关 + 托管 Vue 控制台 + IM 回调 | 映射到宿主 `${WEB_PORT:-80}` |

网关首次启动会自动执行数据库迁移（建表 + 种子化管理员 `admin/admin123`）。
浏览器打开 `http://<服务器IP>` → 用 **admin / admin123** 登录，然后立即在用户菜单修改密码。

### 防火墙

只需放行网页端口（默认 80，或你改过的 `WEB_PORT`）。
**不要**额外开放 9022 —— 网关只对容器内的 Nginx 暴露。

---

## 三、配置 HTTPS（生产必做）

IM 平台（飞书、企业微信）要求回调地址必须是 HTTPS。推荐在宿主用 Caddy 自动签发：

```bash
# 安装 Caddy（Debian/Ubuntu）
sudo apt install -y debian-keyring debian-archive-keyring apt-transport-https
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' | sudo gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' | sudo tee /etc/apt/sources.list.d/caddy-stable.list
sudo apt update && sudo apt install caddy
```

`/etc/caddy/Caddyfile`：

```
cp.example.com {
    reverse_proxy 127.0.0.1:8088
}
```

把 compose 的网页端口改成非 80（避免与 Caddy 冲突）：

```bash
echo "WEB_PORT=8088" >> .env
docker compose up -d
sudo systemctl reload caddy
```

然后把 `gateway.yaml` 的 `server.public_addr` 改成 `https://cp.example.com` 并重启网关。

---

## 四、日常运维

```bash
docker compose logs -f gateway     # 看网关日志
docker compose restart gateway     # 改完配置后重启生效
docker compose down                # 停止并移除容器
docker compose up -d --build       # 更新代码后重建
docker compose pull && docker compose up -d   # 用镜像时的更新方式
```

### 更新

```bash
git pull
docker compose up -d --build       # 会重新构建前后端镜像
```

### 数据持久化

机器人配置保存在名为 `gateway-data` 的卷里（容器内 `/app/data/bots.json`）：

```bash
docker compose down                # 不会删除卷，配置仍在
docker volume inspect codeporter_gateway-data   # 查看卷位置
docker run --rm -v codeporter_gateway-data:/data -v "$PWD":/backup alpine \
  tar czf /backup/bots-backup.tar.gz -C /data .
```

> 任务与队列在内存中，网关重启后任务记录会清空（机器人配置不会丢）。

---

## 五、开发机上启动 LocalAgent

```bash
# 下载 dist/ 中对应系统的客户端（或 make release 自己生成）
tar -xzf codeporter-agent-v0.2.0-linux-amd64.tar.gz
cd codeporter-agent-v0.2.0-linux-amd64
vim configs/agent.yaml   # 填 agent.id / agent.token / gateway.addr=https://cp.example.com
./codeporter-agent -config configs/agent.yaml
```

### 本地 AI 调用方式（CLI 直调 / MCP）

LocalAgent 支持两种调用本机 AI 工具（Claude Code / Codex / Trae / CodeBuddy）的方式，
由 `configs/agent.yaml` 中每个工具的 `mode` 字段决定：

| mode | 原理 | 前置要求 | 适用 |
| --- | --- | --- | --- |
| `cli`（**推荐**） | 每个任务启动一次 CLI 进程，传参数、读 stdout，进程退出即结束 | 装了 CLI 且**已登录** | 绝大多数用户 |
| `mcp` | 常驻 MCP stdio 子进程，走 JSON-RPC `tools/call` | 工具本身需开启 MCP Server | 需要工具的 MCP 能力时 |

**`mode: cli` 配置示例**（Claude Code，已实测）：

```yaml
mcp:
  claude_code:
    enabled: true
    mode: cli
    command: claude
    request_timeout: 5m
    cli:
      args: ["-p", "{{prompt}}", "--output-format", "json"]  # {{prompt}} 占位符
      model: sonnet                 # → --model
      permission_mode: bypassPermissions   # → --permission-mode（无人值守必须放开）
      max_turns: 0                  # → --max-turns，0 = 不限制
      output_format: json           # text / json / stream-json
      result_path: result           # 从 JSON 的该字段取正文
      prompt_via_stdin: false       # true 则提示词走 stdin，argv 里不再出现提示词
      include_stderr: false         # true 则把 stderr 也回传（调试）
```

各工具的默认 `args` 模板（`args` 留空时自动套用）：

| 工具 | 默认命令 | 默认参数 |
| --- | --- | --- |
| Claude Code | `claude` | `-p {{prompt}} --output-format json` |
| Codex | `codex` | `exec {{prompt}} --json --skip-git-repo-check` |
| Trae | `trae` | `-p {{prompt}}`（**未提供稳定公开 CLI 文档，需按 `trae --help` 调整**） |
| CodeBuddy | `codebuddy` | `-p {{prompt}} --output-format json`（按本机版本调整） |

> Claude Code 已实测通过；Trae / CodeBuddy 的 CLI 参数形态请先用 `<tool> --help` 核对，
> 不一致时在 yaml 里显式写 `cli.args` 覆盖。

### 选择工作目录（AI CLI 在哪工作）

Claude Code / Codex 这类 CLI 会在**工作目录**里读写文件、执行命令，所以这个目录
决定它「能看到哪些项目文件」。客户端里有两种方式设置：

- **窗口内选择**：打开客户端 → 「本地 AI（CLI 在此目录下工作）」分组 →
  点「浏览…」选目录，或点「默认」清空。
- **直接改配置**：`configs/agent.yaml` 的 `mcp.work_dir`（对所有工具生效），
  或某个工具下的 `work_dir`（覆盖全局）。

优先级：单个工具的 `work_dir` > 全局 `mcp.work_dir` > **客户端 exe 所在目录**。

> 为什么要显式指定：双击启动时进程的当前目录由 Explorer 决定（可能是用户目录甚至
> `System32`）。若不指定，AI CLI 会在一个随意的目录下工作，看不到你的项目代码。

工作目录不存在或不是文件夹时，任务会在调用前就明确报错，而不是让 CLI 报一堆
看不懂的错。

### 测试 CLI 连接

客户端窗口左下角有「**测试 CLI 连接**」按钮；命令行等价写法：

```bash
codeporter-agent.exe -test-cli -config configs/agent.yaml          # 安装检测 + 真实调用
codeporter-agent.exe -test-cli -no-probe -config configs/agent.yaml # 只检测安装，不消耗额度
```

它分两步，能把「没装」「没登录」「额度用尽」区分开：

| 结论 | 含义 | 处理 |
| --- | --- | --- |
| ✅ 可用 | 装好了，且真实调用成功拿到返回 | 无需处理 |
| ⏭ 未启用 | 该工具在配置里没开 | 想用就去配置里 `enabled: true` |
| ❌ 未安装 | 命令不在 PATH | 安装该 CLI，或改 `command` |
| ⚠️ 额度/配额不足 | **调用链已通、认证已过**，但账号额度用尽 | 给账号充值 / 关闭"仅用免费层"；**不需要配 API 密钥** |
| ⚠️ 认证失败 | 登录态无效 | 在本机重新登录该 CLI |
| ❌ 工作目录不可用 | 目录不存在或不是文件夹 | 用「浏览…」重选 |
| ❌ 调用失败 | 超时或 CLI 报错 | 看下方运行日志的详细信息 |

测试在后台线程执行，不会卡住界面；实测会发一个极短提示词（"只回复两个字：可以"）
以验证完整链路，因此会消耗极少量额度。

### AI 密钥还需要填吗？——CLI 模式下**不需要**

**结论：`mode: cli` 时通常不用填任何 AI 密钥。**

原因：claude / codex 这类 CLI 默认读取**本机登录态**（OAuth 登录或订阅凭证），
不是 API Key。`claude --help` 里 `--bare` 的说明也写明：只有 `--bare` 模式才
"strictly ANTHROPIC_API_KEY"（OAuth 与 keychain 都不读）。

本机实测（`claude -p "..." --output-format json`）返回：

```
{"type":"result","is_error":true,"api_error_status":403,
 "result":"Failed to authenticate. API Error: 403 ...
           code: AccessDenied, message: Free quota exhausted ..."}
```

注意这是 **403 额度耗尽**，不是"缺密钥"——说明认证已通过、调用链完全正常，
只是账号的免费额度用尽了。充值或关闭"仅用免费层"后即可正常返回。

只有以下情况才需要填密钥（写在 `.env` 的 `ANTHROPIC_API_KEY` / `OPENAI_API_KEY`）：

- 使用 `claude --bare` 之类的最小模式（该模式不读 OAuth/keychain）
- 想显式指定第三方 API Key 或切换账号
- 走 Bedrock / Vertex 等云厂商凭据

`mcp` 模式下是否需要密钥取决于该工具的 MCP Server 实现要求。

### 其他实现要点

- **stdin 必须关闭**：`os/exec` 在 `Stdin == nil` 时会自动接 `os.DevNull`
  （等价 `< /dev/null`）。否则 claude 会等待 stdin 输入、白等约 3 秒。
- **不要给 claude 加 `--verbose`**：实测带 `--verbose` 时，即使指定
  `--output-format json`，输出也会变成 stream-json 事件**数组**，形状与 json 不同。
  需要真流式时显式设 `output_format: stream-json`（解析器兼容对象与数组两种形状）。
- **超时 context 的 cancel 必须由收尾 goroutine 触发**：若在 `StreamRun` 里
  `defer cancel()`，函数一返回就取消 context，刚启动的 CLI 进程会被立刻杀掉。
- **无人值守必须放开权限**：不加 `permission_mode: bypassPermissions`，
  CLI 会卡在交互式权限确认直到超时。

### 免 make 直接编译客户端（仅需 Go 工具链）

不想装 make / 不想跑发布脚本时，进入 `backend/` 直接用 `go build` 即可产出本机客户端可执行文件：

```bash
cd backend

# 1) 在本机系统上直接编译（Windows 会产出 .exe，macOS/Linux 产出无后缀二进制）
go build -ldflags "-s -w" -o codeporter-agent ./cmd/agent

# 2) 跨平台编译：在任意系统上为 Windows 机器产出 codeporter-agent.exe
#    Windows 客户端务必加 -H windowsgui，否则双击会额外弹出一个黑色 cmd 窗口。
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 \
  go build -ldflags "-s -w -H windowsgui" -o codeporter-agent.exe ./cmd/agent

# 3) 运行（把 <path> 换成上一步产出的文件名）
codeporter-agent -config configs/agent.yaml
```

要点：
- 仅需 Go 1.23+，**不需要 make、不需要 Node**；`version` 包在未注入 ldflags 时会回落到内置默认版本号，编译即可运行。
- `-ldflags "-s -w"` 仅用于裁剪符号表、减小体积，可省略。
- **`-H windowsgui` 是 Windows 客户端的关键开关**：它把 exe 标记为「GUI 子系统」。若漏掉，Windows 双击时会按「控制台子系统」自动开一个 `cmd` 黑框（这就是你之前看到"客户端+命令行"一起起来的原因）。即便漏加，新版也会在启动时自动调用 `FreeConsole` 关闭该黑框；但加上最稳妥。
- 产出的是静态可执行文件，拷到目标机器直接运行，无需安装运行时。
- 同样方法可编译网关：`go build -ldflags "-s -w" -o codeporter-gateway ./cmd/gateway`（本地联调用，容器部署仍走 Dockerfile）。

### Windows 原生客户端（可视化填配置）

在 Windows 上，`codeporter-agent.exe` 是一个 **原生窗口程序**：双击即可弹出配置窗口，无需浏览器、无需 Node。

> **不会再有黑框**：客户端本身按要求以「GUI 子系统」构建（`-H windowsgui`），即使按控制台构建也会在启动时自动脱离控制台；同时，本地拉起 AI 工具（Trae / Claude Code / CodeBuddy / Codex 等）子进程时已加 `CREATE_NO_WINDOW`，任务运行时也不会再弹出任何命令行窗口。

**构建（推荐加 `-H windowsgui`，双击不弹黑框）：**

```bash
cd backend
go build -ldflags "-s -w -H windowsgui" -o codeporter-agent.exe ./cmd/agent
```

**使用步骤：**

1. 双击 `codeporter-agent.exe` → 弹出「CodePorter 本地代理」窗口。
2. 填写：网关地址、**连接秘钥**（控制台「秘钥」页创建，含 agent 权限；实例 ID 自动生成、只读）、Anthropic / OpenAI API Key、并发数与日志级别。
3. 点「保存并启动」：配置写入同目录 `configs/agent.yaml`（文件权限 0600），并立即开始连接网关拉取任务；窗口下方实时显示运行日志。
   「停止」用于停止代理，「仅保存配置」只写文件不启动。

AI 密钥仅存于本机 `agent.yaml`（文件权限 0600），并作为 `ANTHROPIC_API_KEY` / `OPENAI_API_KEY` 环境变量注入到本地 AI 工具进程。

> 非 Windows，或希望无界面运行（容器 / CI / 后台服务）：加 `-console` 参数，行为与原命令行模式一致。
> 若不想用窗口，也可直接编辑 `agent.yaml` 的 `gateway` / `agent` / `secrets` 段，再用 `-console` 启动。

看到日志出现 `pull` / `connected` 即表示已连上网关，控制台「本地节点」页会显示为在线。

自建为系统服务（systemd）：

```ini
[Unit]
Description=CodePorter LocalAgent
After=network.target

[Service]
ExecStart=/opt/codeporter/codeporter-agent -config /opt/codeporter/agent.yaml
Restart=always
RestartSec=5
WorkingDirectory=/opt/codeporter

[Install]
WantedBy=default.target
```

---

## 六、故障排查

| 现象 | 排查 |
|---|---|
| 网页打不开 | `docker compose ps` 看 gateway 是否 running；宿主防火墙是否放行端口 |
| 页面能开但接口 502 | gateway 容器是否 healthy；`docker compose logs gateway`；MySQL 是否 healthy |
| 登录失败 | 确认账号密码；忘记密码可由 admin 重置；默认 admin 首次登录后必须改密 |
| 客户端 401/403 | 秘钥是否含 `agent` scope、是否已过期/被删除；`X-Agent-ID` 是否随请求发送 |
| 客户端启动报「agent.token 已废弃」 | 旧配置请改用 `agent.key`/`AGENT_KEY`（控制台秘钥），见控制台「秘钥」页 |
| 节点一直离线 | agent.yaml 的 `gateway.addr` 是否正确；秘钥是否有效；实例 ID 是否自动生成成功 |
| 机器人回调失败 | `public_addr` 是否是公网 https 地址；飞书/企微后台保存回调时的报错 |
| 网页对话提示 409 / 无可用客户端 | 名下有多台实例需在页面选择；0 台时请先用秘钥启动客户端 |
| 网页对话一直转圈 | 本地 Agent 是否在线；本机 AI 工具是否已安装并登录 |
| SSE 不吐字 | Nginx 是否 `proxy_buffering off`（本项目 nginx.conf 已配置） |

查看网关自身健康：

```bash
docker compose exec gateway wget -qO- http://127.0.0.1:9022/healthz
curl https://cp.example.com/healthz
```
