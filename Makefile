# CodePorter — 顶层 Makefile
#
# 统一入口：后端（Go）、前端（Vue）、发布产物（dist/）与容器部署。
# 需要单独操作某一侧时，直接进 backend/ 或 frontend/ 目录执行对应命令即可。

VERSION    ?= v0.2.0
BACKEND    := backend
FRONTEND   := frontend
CLIENT     := client
DIST_DIR   := dist
GO         ?= go
WEB_STATIC := $(BACKEND)/web/dist

# Windows GUI 子系统标记。只在 Windows 上追加：-H windowsgui 会让 exe 双击时不弹
# 黑色控制台窗口，但在 Linux/macOS 上作为链接参数会直接报错，故按 OS 条件注入。
ifeq ($(OS),Windows_NT)
WINGUI := -H windowsgui
else
WINGUI :=
endif

.PHONY: help all init build build-backend build-agent-windows build-frontend web test vet fmt \
        release release-agent run-gateway run-agent dev-web \
        client-deps client-core client-app client-dist client-dist-host \
        docker-build docker-up docker-down docker-logs clean dist-clean

help: ## 显示帮助
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk -F':.*?## ' '{printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

all: fmt vet test build ## 格式 → 静态检查 → 测试 → 构建

init: ## 初始化：整理后端依赖 + 安装前端依赖
	cd $(BACKEND) && $(GO) mod tidy
	cd $(FRONTEND) && npm install

# ---------------- 构建 ----------------

build: build-backend build-frontend web ## 构建后端与前端，并把前端产物落到 backend/web/dist

build-backend: ## 构建后端两个二进制到 backend/bin/
	cd $(BACKEND) && $(GO) build -ldflags "-s -w" -o bin/codeporter-gateway ./cmd/gateway
	cd $(BACKEND) && $(GO) build -ldflags "-s -w $(WINGUI)" -o bin/codeporter-agent ./cmd/agent

build-agent-windows: ## 构建可双击的 Windows 原生客户端（GUI 子系统，不弹 cmd）
	cd $(BACKEND) && CGO_ENABLED=0 GOOS=windows GOARCH=amd64 $(GO) build -ldflags "-s -w -H windowsgui" -o codeporter-agent.exe ./cmd/agent
	@echo "已生成 backend/codeporter-agent.exe（双击即弹客户端，无 cmd 黑框）"

build-frontend: ## 构建前端产物
	cd $(FRONTEND) && npm run build

web: ## 把前端产物复制到后端托管目录（网关 static_dir 默认指向它）
	rm -rf $(WEB_STATIC)
	mkdir -p $(BACKEND)/web
	cp -r $(FRONTEND)/dist $(WEB_STATIC)
	@echo "frontend dist -> $(WEB_STATIC)"

# ---------------- 质量 ----------------

fmt: ## 格式化 Go 代码
	cd $(BACKEND) && gofmt -w .

vet: ## 静态检查
	cd $(BACKEND) && $(GO) vet ./...

test: ## 运行测试
	cd $(BACKEND) && $(GO) test ./... -count=1

typecheck: ## 前端类型检查
	cd $(FRONTEND) && npm run typecheck

# ---------------- 运行 ----------------

run-gateway: ## 启动网关（默认 :9022，同时托管网页控制台）
	cd $(BACKEND) && $(GO) run ./cmd/gateway -config configs/gateway.yaml

run-agent: ## 启动本地 Agent（无界面命令行模式）
	cd $(BACKEND) && $(GO) run ./cmd/agent -console -config configs/agent.yaml

dev-web: ## 前端开发服务器（:5173，代理 /api 到本机网关）
	cd $(FRONTEND) && npm run dev

# ---------------- 发布产物 ----------------

release: ## 交叉编译并打包全部平台客户端到 dist/（含下载页）
	cd $(BACKEND) && $(GO) run ./scripts/release -version $(VERSION) -out ../$(DIST_DIR)

client-deps: ## 安装 Electron 客户端依赖
	cd $(CLIENT) && npm install --no-audit --no-fund

client-core: ## 编译 Electron 客户端用的 Go 核心
	cd $(CLIENT) && node scripts/build-core.mjs

client-app: ## 构建 Electron 界面与主进程
	cd $(CLIENT) && npm run build

client-dist: client-deps client-core ## 一键打包 Windows 客户端 exe（免安装单文件）
	cd $(CLIENT) && npx electron-builder --win --x64 --config electron-builder.config.cjs

client-dist-host: ## 按当前系统打包客户端（macOS/Linux 用 build-client.sh；三平台发布走 .github/workflows/release-client.yml）
	bash build-client.sh

release-agent: ## 只发布本地客户端
	cd $(BACKEND) && $(GO) run ./scripts/release -version $(VERSION) -out ../$(DIST_DIR) -only agent

# ---------------- 容器部署 ----------------

docker-build: ## 构建镜像
	docker compose build

docker-up: ## 后台启动（首次会自动构建）
	docker compose up -d --build

docker-down: ## 停止并移除容器
	docker compose down

docker-logs: ## 查看网关日志
	docker compose logs -f gateway

# ---------------- 清理 ----------------

clean: dist-clean ## 清理所有构建产物
	rm -rf $(BACKEND)/bin $(BACKEND)/web $(FRONTEND)/dist

dist-clean:
	rm -rf $(DIST_DIR)
