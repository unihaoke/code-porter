<p align="right">
  <strong>English</strong> · <a href="README.zh-CN.md">简体中文</a>
</p>

<div align="center">

# CodePorter

**Turn the AI coding tools installed and logged in on your machine into a remotely callable API.**

[![CI](https://github.com/unihaoke/code-porter/actions/workflows/ci.yml/badge.svg)](https://github.com/unihaoke/code-porter/actions/workflows/ci.yml)
[![Release](https://github.com/unihaoke/code-porter/actions/workflows/release-client.yml/badge.svg)](https://github.com/unihaoke/code-porter/actions/workflows/release-client.yml)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.23-00ADD8.svg)](https://go.dev/)
[![Node](https://img.shields.io/badge/Node-20.19%2B-43853D.svg)](https://nodejs.org/)
[![Platform](https://img.shields.io/badge/Platform-Windows%20%7C%20macOS%20%7C%20Linux-lightgrey.svg)](#)
[![PRs Welcome](https://img.shields.io/badge/PRs-welcome-brightgreen.svg)](CONTRIBUTING.md)

**[Quick Start](#quick-start)** · **[Deployment](docs/deployment.md)** · **[API Reference](docs/api.md)** · **[Architecture](docs/architecture.md)** · **[FAQ](#faq)** · **[Contributing](CONTRIBUTING.md)**

</div>

CodePorter connects the **AI coding tools already installed and authenticated on your computer**
(Trae / Claude Code / CodeBuddy / Codex) to a public relay chain, so they can be invoked anytime from
**OpenAI-compatible clients**, **Feishu (Lark) / WeCom group bots**, and a **web chat console**.

> CodePorter ships **no AI models** and never uploads your code. It is a relay only:
> requests are dispatched to your own machine, the AI runs locally, and results travel back the same way.

---

## Features

- **Reuse your local subscription / login** — drives the local CLIs directly (`mode: cli`); no extra AI API key, no double billing.
- **OpenAI compatible** — `/v1/chat/completions` with `stream` support; existing clients, scripts and third-party programs work unchanged.
- **Outbound-only agent** — the LocalAgent dials the gateway itself (Pull queue + WebSocket direct); no public IP or port mapping required.
- **Multi-tenant** — accounts, dual-scope keys (`agent` / `api`), and three execution permission levels (read-only / workspace-write / full), with per-user resource isolation.
- **Dual transport** — the Pull queue never drops tasks while offline; Direct mode delivers millisecond-level streaming.
- **Local IM bot loops (pluggable channels)** — Feishu via the official long-connection SDK and WeCom via the Smart-Bot openws protocol; each channel runs an independent outbound-WebSocket loop on your machine and answers with streaming output (a single streaming card on Feishu, streaming Markdown on WeCom); no public domain needed, channels can be toggled separately.
- **Desktop client** — an Electron app with Overview / Settings / Logs views and independently controlled runtimes: the agent, each IM bot channel, and local AI tools.
- **One-command self-hosting** — Docker Compose (MySQL + gateway + console); clients for Windows / macOS / Linux.

---

## Architecture

```
                        ┌──────────── Public VPS ────────────┐
  OpenAI-compatible      │                                    │
  client (Bearer key) ───▶│          CodePorter Gateway        │
  Web console (login)  ───▶│  /v1/chat/completions /api/*      │
                        │     MySQL: users/keys/sessions/nodes│
                        └───────┬────────────────────┬─────────┘
                                │ Pull queue         │ WebSocket direct
                                │ (agent dials out)  │ (X-Agent-Token + instance ID)
                        ┌───────▼────────────────────▼─────────┐
                        │        Your dev machine · LocalAgent  │
                        │  outbound-only → worker pool → MCP    │
                        │  Trae / Claude Code / CodeBuddy/Codex │
                        │  Feishu / WeCom IM bots (local loop,  │
                        │  never traverses the gateway)         │
                        └───────────────────────────────────────┘
```

**Multi-tenancy**: one gateway serves many developers. Log into the web console and create a
**connection key** on the Keys page — you can grant `agent` (client connection) and/or `api`
(OpenAI calls) scopes, set an expiry, and pick an execution permission level. Keys prove ownership;
the local client generates an instance ID on first start (one ID per machine). Tasks and history are
isolated per user; regular users only see their own resources.

**Two transports**

| Transport | Trigger | Characteristics |
|---|---|---|
| **Pull queue** (default) | Agent polls for tasks | Survives disconnects, queues while offline; ideal for long tasks and group bots |
| **Direct** | WebSocket long connection | Millisecond-level interactive streaming for the web console; fails fast when the agent is offline |

---

## Repository layout

```
code-porter/
├── backend/              Go backend (DDD layers + standard Go layout)
│   ├── cmd/gateway/      Gateway entrypoint
│   ├── cmd/agent/        Local client entrypoint (CLI mode / -ipc core for Electron)
│   ├── internal/domain/          Domain layer (task / agent / apikey / user / model, zero external deps)
│   ├── internal/application/     Application layer (use cases + outbound ports)
│   ├── internal/infrastructure/  HTTP/SSE/WS, memory & MySQL repositories, MCP, IM bot channels (Feishu / WeCom long connection)
│   ├── pkg/               Utilities (apperr / pool / backoff / openai / version)
│   ├── configs/           gateway.yaml, agent.yaml
│   └── scripts/release/   Release artifact tooling
├── frontend/             Vue 3 + Vite web console (served by the gateway)
├── client/               Electron desktop client (UI + Go core over IPC)
│   ├── src/main/             Main process: window, core process, IPC, native dialogs
│   ├── src/preload/          Typed contextBridge API
│   ├── src/renderer/         Views: Overview / Settings / Logs
│   └── src/shared/           Types shared across all three sides
├── docs/                 Docs (architecture / API / deployment / development / bot setup) — Chinese
├── .github/workflows/    CI and cross-platform client releases
├── docker-compose.yml    One-command public-VPS deployment
├── AGENTS.md             Collaboration guide for AI coding assistants
└── Makefile              Top-level command entrypoint
```

Dependency direction is strictly one-way: `infrastructure → application → domain`.
The client UI is outside this hierarchy — it talks to the Go core only via a JSON-lines protocol over
stdin/stdout, so the core knows nothing about the UI.

---

## Quick start

### 1. Deploy the gateway (public VPS, Docker Compose)

```bash
# Prepare config: edit .env (recommended) or the yaml files directly
# Precedence: process env vars > .env file > backend/configs/*.yaml
cp .env.example .env
vim .env            # Set at least MYSQL_PASSWORD / MYSQL_ROOT_PASSWORD

# Start everything (MySQL 8 + gateway + web console; tables and admin seed run automatically)
docker compose up -d --build

# Open the console: http://<your-server-ip>
# Default credentials admin / admin123 (change them immediately after first login)
docker compose logs -f gateway
```

After the first login:

1. User menu (top-right) → **Change password** (startup logs keep warning while the default password is in use);
2. **Keys** page → create a key (check `agent` for the desktop client, `api` for programmatic calls).
   The plaintext key is shown **exactly once** — copy and save it;
3. Put the key in your local client (see below), or call `/v1/chat/completions` from any OpenAI
   client with `Authorization: Bearer cp_...`;
4. To onboard teammates, the admin creates accounts on the Users page; each user's resources are invisible to others.

> Account data (users / keys / sessions / instances) is persisted in MySQL's `mysql-data` volume;
> the task queue and online status stay in gateway memory (acceptable runtime state to reset on restart).
> The DSN is injected via `MYSQL_DSN` — URL-encode special characters in the password; see `.env.example`.

See [docs/deployment.md](docs/deployment.md) *(Chinese)* for HTTPS, upgrades, backups and troubleshooting.

### 2. Connect LocalAgent on your dev machine

**Recommended: the Electron desktop client** (Overview / Settings / Logs; the UI can evolve without touching the core):

| OS | Command | Artifact |
| --- | --- | --- |
| Windows | Double-click `build-client.bat` (or `make client-dist`) | Portable `CodePorter-<ver>-portable.exe` |
| macOS | `bash build-client.sh` (or `make client-dist-host`) | `CodePorter-<ver>-mac-<arch>.dmg` + `.zip` |
| Linux | `bash build-client.sh` | `CodePorter-<ver>-linux-<arch>.AppImage` |

You can also download prebuilt installers from [Releases](https://github.com/unihaoke/code-porter/releases).
The dmg requires macOS-only `hdiutil` and signing tooling, so it **can only be produced on macOS**;
cross-platform releases run in GitHub Actions automatically (push a `v*` tag to trigger
`.github/workflows/release-client.yml`).
Unsigned dmgs are blocked by Gatekeeper on first open — right-click → Open, or run
`xattr -cr /Applications/CodePorter.app`.

Development mode:

```bash
cd client
npm install
npm run dev:core      # Build the Go core into backend/bin/
npm start             # Build the UI and launch Electron
```

See [client/README.md](client/README.md) *(Chinese)* for details. The architecture is
**Electron UI + Go core**: the UI only renders and interacts, while scheduling, protocols and CLI
invocation all live in the Go core; the two communicate via JSON-lines over stdin/stdout.

<details>
<summary>Other options (CLI / headless single binary)</summary>

```bash
# Option 1: prebuilt headless client binary (no Node needed; run as a console program/service)
make release                       # Produces dist/ with 6 platform clients + a download page

# Option 2: run from source (headless)
make run-agent

# Option 3: cross-compile a Windows binary yourself (Go toolchain only)
cd backend
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags "-s -w" -o codeporter-agent.exe ./cmd/agent
# Run headless:
codeporter-agent.exe -config configs/agent.yaml
# Self-check local AI connectivity:
codeporter-agent.exe -test-cli -config configs/agent.yaml
```

> The single-binary client is a headless console program for servers/CLI use; pick the Electron client for a GUI.
> AI API keys are usually **not** needed — in `mode: cli`, Claude Code / Codex use the local login (subscription).

</details>

After creating a key with the **agent** scope in the console, configure the client with it
(in Electron, fill "Connection key" on the Settings page; for the CLI, set `agent.key` in
`backend/configs/agent.yaml` or the `AGENT_KEY` environment variable, and the gateway address via
`AGENT_GATEWAY_ADDR`).
The instance ID is generated on first start (`agt_...`) and written back to config; the machine name
defaults to the hostname. **Multiple machines may each use their own key or share one.**

> The legacy `agent.token` / `AGENT_TOKEN` is deprecated: the client now errors out and asks for a key.

> Prerequisite: at least one supported AI coding tool must be installed and authenticated locally. CodePorter is only the relay.

---

## Local development

```bash
make init           # Tidy Go deps + install frontend deps
make run-gateway    # Gateway on :9022 (also serves the console; run make web first)
make dev-web        # Frontend HMR on :5173, proxying /api to the gateway
make test           # Backend tests
```

The gateway serves the frontend build (`web.static_dir` defaults to `web/dist`).
After editing the frontend, run `make build-frontend && make web`, then open `:9022`.

See [docs/development.md](docs/development.md) *(Chinese)* for requirements, debugging setups and testing.

---

## Web console

| Page | Capabilities |
|---|---|
| Chat | Stream (SSE) conversations with your own local AI; pick the target client when several are online |
| Keys | Create/delete connection keys (agent/api scopes, expiry, three permission levels); plaintext shown once |
| Users (admin only) | Create users, assign roles, reset passwords, delete (cascading cleanup) |
| Local nodes | Online status, heartbeat, CPU/memory, queue and connected tools for your clients (admins see all) |
| Tasks | Status, source, retry count and errors of your recent tasks (admins see all) |
| Overview | Nodes / connections / tasks at a glance |

---

## IM bot setup

IM bots run in the **local client** over outbound WebSocket long connections — no public domain needed.
Channels are independent (enable either or both, start/stop each one separately):

- **Feishu (Lark)** — create an in-house app on the [Feishu Open Platform](https://open.feishu.cn/app),
  enable the bot capability, choose **long-connection event delivery**, and subscribe to
  `im.message.receive_v1`; put the App ID / App Secret into the "Feishu bot" card in the GUI
  (or `bots.feishu` in `configs/agent.yaml`). Answers arrive as a single streaming card,
  with the thinking/tool process streaming into the same card.
- **WeCom (企业微信)** — in the admin console open **Security & Management → Management Tools →
  Smart Bot (智能机器人)**, create a bot, enable **API mode** with the **long-connection**
  integration, and copy the Bot ID / Secret into the "WeCom bot" card (or `bots.wecom`).
  Answers arrive as streaming Markdown: the process shows in a gray quote block and collapses
  into a short summary once the answer body starts.

Then start each channel on the Overview page and @-mention the bot in a group.

Step-by-step: [docs/bot-setup.md](docs/bot-setup.md) *(Chinese)*.

---

## API surface

| Endpoint | Auth | Description |
|---|---|---|
| `POST /api/auth/login` `/logout` `/me` | Public / session | Password login (default admin/admin123), logout, current user |
| `/api/keys` `/api/users/*` | Session (users admin-only) | Self-service key management, user admin, password change |
| `POST /v1/chat/completions` | Key (api scope) | OpenAI-compatible endpoint (`stream` supported; `x-codeporter-agent` selects an instance) |
| `POST /api/chat` | Session | Web console chat (POST + SSE) |
| `/api/overview` `/api/agents` `/api/tasks` `/api/models` | Session | Console data (members: own / admin: all) |
| `/agent/pull` `/agent/ack` `/agent/health` `/agent/ws` | Key (agent scope) + instance ID | Internal LocalAgent endpoints |
| `GET /healthz` `/admin/agents` `/admin/queues` | Public / admin session | Health checks and operations |

Full field reference: [docs/api.md](docs/api.md) *(Chinese)*.

---

## Security

- **LocalAgent is outbound-only and listens on nothing** — no inbound ports, no router port mapping;
- The gateway must sit behind HTTPS (never expose plain HTTP in production);
- Passwords are stored with bcrypt; only SHA-256 hashes of keys are stored; web sessions use server-revocable tokens (7-day default);
- The default admin `admin/admin123` comes from the migration seed and **must be changed after first login** (startup logs keep warning);
- Plaintext keys are shown once at creation; keys support scopes, expiries and instant revocation;
- Three execution permission levels (read / workspace-write / full): hard-enforced via sandbox flags in CLI mode, soft-enforced via prompt guard in MCP mode;
- IM bot channel secrets (Feishu App Secret, WeCom Bot Secret) stay only in local config / environment variables and are **never sent back through the gateway or IPC in plaintext** (the UI only shows a "configured" boolean and a masked credential ID);
- Accounts / keys / sessions / instances live in MySQL; the task queue and online status are in-memory runtime state.

Please do **not** open public issues for security vulnerabilities — report them privately per [SECURITY.md](SECURITY.md).

---

## FAQ

**Will my code be uploaded?**
No. Tasks and prompts are relayed by the gateway to your own machine, where local AI CLIs read and write
local files. The gateway does not store code, and CodePorter contains no models itself.

**Do I need a separate AI API key?**
Usually not. `mode: cli` reuses the local CLI's subscription/login (e.g. Claude Code, Codex); keys are
only needed in `--bare` mode or with an explicitly configured third-party API key.

**Does the bot need a public domain or server?**
No. Both IM channels (Feishu and WeCom) run in the local client over outbound WebSocket long
connections and close the loop on your machine; each channel can run independently. The gateway
side only serves the OpenAI API and web console.

**Which operating systems are supported?**
Gateway: Linux (Docker). Local client: Windows / macOS / Linux, both Electron GUI and headless builds.

**Which AI coding tools are supported?**
Trae, Claude Code, CodeBuddy and Codex, behind a unified MCP/CLI adapter layer; adding a tool means extending that layer.

---

## Roadmap

- [ ] Task and session persistence (SQLite / Redis) for multi-replica gateways
- [ ] Finer-grained multi-tenant permissions
- [ ] Bot card-button interactions and multi-turn context
- [ ] Automatic repository context attachment
- [ ] Better metrics and alerting

---

## Contributing

Issues, PRs and doc improvements are welcome! Please read:

- [Contributing guide](CONTRIBUTING.md) — dev environment, commit conventions, PR flow
- [Code of conduct](CODE_OF_CONDUCT.md) — community expectations
- [Changelog](CHANGELOG.md) — release history

---

## License

Released under the [Apache License 2.0](LICENSE), free for personal and commercial use; retain the license and copyright notice when redistributing or deriving.

---

## Documentation

> Full documentation below is currently written in Chinese.

- [Architecture](docs/architecture.md)
- [API reference](docs/api.md)
- [Deployment guide](docs/deployment.md)
- [Development guide](docs/development.md)
- [Bot setup guide](docs/bot-setup.md)
- [Desktop client notes](client/README.md)
- [AI assistant collaboration guide](AGENTS.md)
