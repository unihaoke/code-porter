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

编辑 `backend/configs/gateway.yaml`，**至少修改这几项**：

```yaml
server:
  # 网关对外地址，机器人回调 URL 由它拼出，必须公网可达
  public_addr: "https://cp.example.com"

security:
  api_keys:
    - "sk-换成你自己的key"
  agent_tokens:
    - "换成你自己的agent令牌"
  admin_token: "换成你自己的控制台令牌"

agent:
  id: "local-pc"
  token: "换成你自己的agent令牌"   # 必须与 agent_tokens 中的一项一致
```

> `agent.token` 需与开发机上 `configs/agent.yaml` 中的 `agent.token` 完全一致。

---

## 二、启动

```bash
docker compose up -d --build
docker compose ps          # 两个服务都应是 healthy / running
docker compose logs -f gateway
```

会启动两个容器：

| 容器 | 作用 | 端口 |
|---|---|---|
| `codeporter-gateway` | Go 网关，任务调度与 IM 回调 | 8080（仅容器网络内） |
| `codeporter-web` | Nginx，托管 Vue 控制台并反代 API | 映射到宿主 `${WEB_PORT:-80}` |

浏览器打开 `http://<服务器IP>` → 用 `admin_token` 登录。

### 防火墙

只需放行网页端口（默认 80，或你改过的 `WEB_PORT`）。
**不要**额外开放 8080 —— 网关只对容器内的 Nginx 暴露。

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
vim agent.yaml          # 填 agent.id / agent.token / gateway.addr=https://cp.example.com
./codeporter-agent -config agent.yaml
```

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
| 网页打不开 | `docker compose ps` 看 web 是否 running；宿主防火墙是否放行端口 |
| 页面能开但接口 502 | gateway 容器是否 healthy；`docker compose logs gateway` |
| 控制台提示令牌错误 | `security.admin_token` 是否与输入一致；改完要 `restart gateway` |
| 节点一直离线 | agent.yaml 的 `gateway.addr` 是否是 `https://`；token 是否与网关一致 |
| 机器人回调失败 | `public_addr` 是否是公网 https 地址；飞书/企微后台保存回调时的报错 |
| 网页对话一直转圈 | 本地 Agent 是否在线；本机 AI 工具是否已安装并登录 |
| SSE 不吐字 | Nginx 是否 `proxy_buffering off`（本项目 nginx.conf 已配置） |

查看网关自身健康：

```bash
docker compose exec gateway wget -qO- http://127.0.0.1:8080/healthz
curl https://cp.example.com/healthz
```
