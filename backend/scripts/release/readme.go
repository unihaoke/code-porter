package main

import "fmt"

// readmeFor 生成随压缩包分发的快速上手说明。
func readmeFor(product string, t target, relVersion, binName, archiveName string) string {
	if product == "agent" {
		return agentReadme(t, relVersion, binName, archiveName)
	}
	return gatewayReadme(t, relVersion, binName, archiveName)
}

// shellHint 给出当前平台的解压与运行示例，并返回配套的代码块语言标记。
func shellHint(t target, archiveName, binName, confName string) (unpack, run, lang string) {
	if t.goos == "windows" {
		lang = "powershell"
		unpack = "Expand-Archive .\\" + archiveName + " -DestinationPath ."
		run = ".\\" + binName + " -config " + confName
		return unpack, run, lang
	}
	lang = "bash"
	unpack = "tar -xzf " + archiveName
	run = "chmod +x ./" + binName + "\n./" + binName + " -config " + confName
	return unpack, run, lang
}

func agentReadme(t target, relVersion, binName, archiveName string) string {
	unpack, run, lang := shellHint(t, archiveName, binName, "agent.yaml")
	return fmt.Sprintf(`# CodePorter LocalAgent %s

平台：%s（%s/%s）

运行在**你的开发机**上：主动向网关发起出站连接，拉取任务，调用本机已安装的
AI 编码工具（Trae / Claude Code / CodeBuddy / Codex）执行，再把结果回传。
本机不监听任何端口，外部无法主动访问你的机器。

---

## 1. 前置条件

- 本机已安装并登录至少一个受支持的 AI 编码工具
- 已从网关管理员处获取 `+"`agent.id`"+` 与 `+"`agent.token`"+`

## 2. 解压

`+fence(lang, unpack)+`

## 3. 修改配置

编辑解压出来的 `+"`agent.yaml`"+`，只需替换这两项：

`+fence("yaml", `agent:
  id: "your-agent-id"          # 管理员分配
  token: "your-agent-token"    # 管理员分配

gateway:
  addr: "https://your-gateway-domain"   # 生产环境务必使用 https`)+`

其余字段（并发数、轮询间隔、直连开关、MCP 适配器）保持默认即可。

## 4. 启动

`+fence(lang, run)+`

## 5. 验证

`+fence(lang, binName+" -version")+`

输出示例：`+"`codeporter-agent "+relVersion+"`"+`

看到日志中出现 `+"`connected`"+` / `+"`pull`"+` 即表示已与网关建立连接。

---

## 安全说明

- 客户端**只出站、不监听**，无需开放任何入站端口或配置路由器端口映射
- `+"`agent.yaml`"+` 中含凭据，请勿提交到版本库
- 生产环境请把 `+"`gateway.addr`"+` 配成 `+"`https://`"+`，仅在自签证书调试时临时开启 `+"`insecure_tls: true`"+`
`, relVersion, t.label, t.goos, t.goarch)
}

func gatewayReadme(t target, relVersion, binName, archiveName string) string {
	unpack, run, lang := shellHint(t, archiveName, binName, "gateway.yaml")
	return fmt.Sprintf(`# CodePorter Gateway %s

平台：%s（%s/%s）

部署在**公网服务器**上：对外提供 OpenAI 兼容接口，对内通过 Pull 队列与
WebSocket 直连两条通路把任务下发到开发机上的 LocalAgent。

---

## 1. 解压

`+fence(lang, unpack)+`

## 2. 修改配置

编辑 `+"`gateway.yaml`"+`，至少替换以下占位值：

`+fence("yaml", `server:
  addr: ":8080"

auth:
  api_keys:
    - "sk-your-own-key"        # 调用方使用的 API Key

agents:
  - id: "your-agent-id"
    token: "your-agent-token"  # 需与客户端 agent.yaml 一致`)+`

## 3. 启动

`+fence(lang, run)+`

## 4. 健康检查

`+fence(lang, "curl http://127.0.0.1:8080/healthz")+`

## 5. 验证版本

`+fence(lang, binName+" -version")+`

---

## 生产建议

- 网关必须置于 HTTPS 之后（Nginx / Caddy 反代），不要直接暴露 HTTP
- 当前任务与队列状态保存在内存中，重启即丢失；多副本部署需先替换持久化实现
- 用 systemd 或容器编排托管进程，并配置日志轮转
`, relVersion, t.label, t.goos, t.goarch)
}

// fence 把内容包进带语言标记的代码块。
func fence(lang, body string) string {
	return "```" + lang + "\n" + body + "\n```"
}
