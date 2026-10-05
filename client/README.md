# CodePorter 客户端（Electron）

本地代理的图形客户端：**Electron 界面 + Go 核心**。

界面只负责展示与交互，所有调度、协议、CLI 调用逻辑仍在 Go 核心里
（`backend/`，1.2 万行、已有完整测试）。两者通过 stdin/stdout 的
JSON 行协议通信，因此升级界面不会影响任何核心行为。

```
┌─────────────────────────┐   ipcMain / contextBridge   ┌──────────────────┐
│  渲染进程（Vue 3）      │ ◄─────────────────────────► │  主进程（Node）   │
│  概览 / 配置 / 日志      │        Electron IPC         │  窗口 / 对话框    │
└─────────────────────────┘                              └────────┬─────────┘
                                                                 │ spawn + JSON 行
                                                                 ▼
                                                    ┌──────────────────────┐
                                                    │  Go 核心（-ipc）      │
                                                    │  调度/协议/CLI 调用    │
                                                    └──────────────────────┘
```

## 一键打包成 exe（Windows）

在**仓库根目录**双击 `build-client.bat`，或命令行执行：

```bat
build-client.bat
```

它会依次完成：环境检查 → 装依赖 → （增量）编译 Go 核心 → 构建界面 → 打包，
产物在 `client\release\`：

| 产物 | 说明 |
| --- | --- |
| `CodePorter-<版本>-portable.exe` | **免安装单文件**，双击即用，不写注册表、不需要安装 |
| `CodePorter-<版本>.zip` | 免安装 exe 打包失败时的回退产物（内容等价的压缩包） |

> 打包过程中的 `win-unpacked\` 是 electron-builder 的**必经中间目录**（先解包组装，
> 再压缩成单文件 exe / zip），体积约 200MB+，**不是分发物**。portable / zip 成功后
> 脚本会自动删除它，`release\` 只留下最终的 exe / zip。

默认**不再产出安装版**，因此也不需要下载 NSIS 工具链。偶尔需要安装版时：

```bash
cd client
npx electron-builder --win nsis --x64 --config electron-builder.config.cjs
```

单独执行某一步：

| 命令 | 作用 |
| --- | --- |
| `build-client.bat deps` | 只装 npm 依赖 |
| `build-client.bat core` | **强制**重编 Go 核心 |
| `build-client.bat app` | 只构建 Electron 界面 |
| `build-client.bat pack` | 只打包（复用已构建产物，不重新编译界面） |
| `build-client.bat dir` | **最快**：只产出解包目录，不做任何压缩，保留 `win-unpacked\` 供直接双击验证 |
| `build-client.bat zip` | 解包目录 + zip，跳过最慢的单文件压缩，完成后清理 `win-unpacked\` |
| `build-client.bat portable` | 只打免安装单文件 exe，失败不回退（CI 用） |

提速相关：

- **Go 核心增量编译**：完整构建时若没有任何 `.go` 源比现有 `codeporter-core.exe` 新，
  会自动跳过重编（界面/打包改动不再每次都链接一遍核心）；设 `FORCE_CORE=1` 强制重建。
- **日常本地验证用 `dir` 档**：省掉单文件 exe 的 7z 高压缩，构建最快。
- `SKIP_INSTALLER=1` —— 完整构建时连免安装 exe 也跳过，直接产出 zip
  （免安装 exe 打包失败时的自动回退也是这条路）。
- `ELECTRON_MIRROR` / `ELECTRON_BUILDER_BINARIES_MIRROR` / `GOPROXY` —— 换镜像源
  （脚本已设国内默认：Electron 用 npmmirror，打包工具链走
  `npmmirror.com/mirrors/electron-builder-binaries/`，避免访问 GitHub Releases）。

> **打包前请先关闭正在运行的客户端**。exe 被占用时 electron-builder
> 无法覆盖，会报 "is being used by another process"。

### 下载工具链失败：`proxyconnect tcp: dial tcp :0`

根因几乎总是**代理被设成了空/无效值**，而不是 GitHub 不通：

1. `~/.npmrc` 里残留 `proxy=null` / `https-proxy=null`（老版本 npm 的默认值）。
   electron-builder 会把 `null` 当成真实代理地址交给它的 Go 下载器，于是每次
   下载都是 `dial tcp :0`。执行 `npm config delete proxy && npm config delete https-proxy`
   即可（脚本已自动处理该情况）。
2. 环境变量 `HTTP_PROXY` / `HTTPS_PROXY` 是畸形值（如 `http://`）。脚本会自动
   忽略这类值；也可以手动 `set HTTPS_PROXY=` 清空。

清理后工具链会缓存到 `%LOCALAPPDATA%\electron-builder\Cache\`，之后不再联网。

### `EPERM: operation not permitted, symlink`

用 `.ts` 写的 electron-builder 配置需要先编译，编译时会往
`~/.cache/config-file-ts/` 建 `node_modules` 符号链接，非管理员权限下会失败。
所以配置改成了 `electron-builder.config.cjs`（直接 require，不用编译）。
改配置时请编辑这个 `.cjs` 文件。

有 make 的话也可以：`make client-dist`（等价于上面的完整流程）。

## macOS / Linux 打包

在仓库根目录执行（脚本会按 `uname` 自动选平台与架构）：

```bash
bash build-client.sh                        # 当前系统
PLATFORM=mac ARCH=arm64 bash build-client.sh  # 显式指定
USE_CN_MIRROR=0 bash build-client.sh        # CI / 海外网络：不切国内镜像
```

| 系统 | 产物 |
| --- | --- |
| macOS | `CodePorter-<版本>-mac-<arch>.dmg` + `.zip` |
| Linux | `CodePorter-<版本>-linux-<arch>.AppImage` |

- **dmg 只能在 macOS 上产出**：打包依赖系统的 `hdiutil` 与签名工具链，
  Linux/Windows 上跑不出 dmg；要在 CI 里拿 dmg 就用 `macos-latest` runner。
- 未签名的 dmg 会被 Gatekeeper 拦：首次打开用右键「打开」，或
  `xattr -cr /Applications/CodePorter.app`。有 Apple 证书时删掉配置里的
  `identity: null` / `gatekeeperAssess: false`，并设 `CSC_LINK` + `CSC_KEY_PASSWORD`。
- 三平台一起发版：push 一个 `v*` tag，`.github/workflows/release-client.yml`
  会用 windows / macos(arm64+x64) / ubuntu 三个 runner 各打一个包并建 Release。

## 快速开始（开发调试）

```bash
cd client
npm install                 # 若 electron 二进制未自动下载：
                             #   set ELECTRON_MIRROR=https://npmmirror.com/mirrors/electron/
                             #   node node_modules/electron/install.js

npm run dev:core            # 编译 Go 核心到 backend/bin/（--watch 持续编译）
npm start                   # 构建界面并启动 Electron
```

开发时通常开两个终端：`npm run dev:core`（watch 编译核心）+ `npm run dev:renderer`（Vite 热更新）。

## 常用命令

| 命令 | 作用 |
| --- | --- |
| `npm run dev:core` | 编译 Go 核心（`--watch` 持续监听 backend 变化） |
| `npm run build` | 编译主进程（tsc）+ 界面（vite） |
| `npm start` | 构建并启动客户端 |
| `npm run dist` | 打包 Windows 安装包 + 免安装版（输出到 `release/`） |
| `npm run dist:dir` | 只解包目录版，用于快速验证打包结果 |
| `npm run typecheck` | vue-tsc 类型检查 |

## 核心协议

前端 → 核心（stdin，每行一个 JSON）：

```json
{"id":"1","action":"agent.start","params":null}
```

核心 → 前端（stdout）：

```json
{"id":"1","ok":true,"result":{...}}
{"event":"log","data":{"level":"info","time":"11:30:48","msg":"..."}}
```

动作：`config.get` / `config.save` / `agent.start` / `agent.stop` / `agent.status` /
`cli.test` / `app.quit`
事件：`log` / `status` / `health` / `task` / `ready`

**两条重要约束**（改代码时务必遵守）：

1. **核心的 stdout 只承载协议消息。** 它的运行日志也走 `log` 事件（内部
   `ipcLogWriter` 把日志转成事件），所以任何时候都不能往 stdout 直接打印。
   否则前端会解析失败。诊断信息走 stderr。
2. **配置键名一律 snake_case，与 `agent.yaml` 完全一致。** Go 侧刻意用 yaml
   标签序列化（`cmd/agent/ipc.go` 的 `configToMap`），因为配置结构体只有
   yaml tag、没有 json tag，直接 `json.Marshal` 会得到 Go 字段名
   （`Agent`/`Gateway`…），前端就得维护第二套命名。

## 配置文件位置

| 场景 | 路径 |
| --- | --- |
| 开发态 | `backend/configs/agent.yaml` |
| 打包态 | `%APPDATA%/CodePorter/configs/agent.yaml`（首次运行从 `resources/core/configs/agent.yaml` 复制） |

## 工作目录

AI CLI（Claude Code / Codex…）在**工作目录**里读写文件、执行命令。
可在「配置 → 本地 AI → 工作目录」点「浏览…」选择，会写回 `mcp.work_dir`；
留空则使用客户端 exe 所在目录。

## 飞书机器人

在「配置 → 飞书机器人」里启用并填入企业自建应用的 **App ID / App Secret** 即可，
配置写回 `agent.yaml` 的 `bots.feishu` 节。机器人运行在 Go 核心内，
通过飞书官方长连接（WebSocket 出站）收发消息，**不需要公网域名、回调地址或加密配置**；
修改配置后需重启代理生效。平台侧前置条件与排错见根目录 `docs/bot-setup.md`。

## 环境变量（调试用）

| 变量 | 作用 |
| --- | --- |
| `CODEPORTER_SHOT=<png>` | 开发态启动后自动截图并退出，便于核对界面 |
| `CODEPORTER_TAB=logs\|settings\|dashboard` | 指定启动时打开的标签页（配合 hash 深链） |
| `VITE_DEV_SERVER_URL` | 开发态指向 Vite dev server |

## 目录结构

```
client/
├── src/
│   ├── main/        主进程：窗口、核心进程管理、IPC 转发、原生对话框
│   ├── preload/     contextBridge 暴露的类型化 API
│   ├── renderer/    Vue 3 界面（概览 / 配置 / 日志）
│   └── shared/      三方共用的类型定义
├── scripts/
│   └── build-core.mjs   编译 Go 核心
├── electron-builder.config.cjs
└── vite.config.ts
```

## 排查

| 现象 | 原因 / 处理 |
| --- | --- |
| 提示「未找到 Go 核心可执行文件」 | 先跑 `npm run dev:core`；打包版检查 `resources/core/` |
| 界面空白 | 看主进程 stdout 的 `[renderer]` 行（已把渲染进程 console 转发出来） |
| 安装后双击没反应 | 看日志：CLI 模式下需本机已装并登录对应 AI 工具 |
| npm install 后 electron 报缺二进制 | 手动执行 `node node_modules/electron/install.js` |
