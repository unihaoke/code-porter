# CodePorter — 顶层 Makefile
#
# 统一入口：后端（Go）、前端（Vue）、发布产物（dist/）与容器部署。
# 需要单独操作某一侧时，直接进 backend/ 或 frontend/ 目录执行对应命令即可。

VERSION    ?= v0.2.0
BACKEND    := backend
FRONTEND   := frontend
DIST_DIR   := dist
GO         ?= go
WEB_STATIC := $(BACKEND)/web/dist

.PHONY: help all init build build-backend build-frontend web test vet fmt \
        release release-agent run-gateway run-agent dev-web \
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
	cd $(BACKEND) && $(GO) build -ldflags "-s -w" -o bin/codeporter-agent ./cmd/agent

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

run-agent: ## 启动本地 Agent
	cd $(BACKEND) && $(GO) run ./cmd/agent -config configs/agent.yaml

dev-web: ## 前端开发服务器（:5173，代理 /api 到本机网关）
	cd $(FRONTEND) && npm run dev

# ---------------- 发布产物 ----------------

release: ## 交叉编译并打包全部平台客户端到 dist/（含下载页）
	cd $(BACKEND) && $(GO) run ./scripts/release -version $(VERSION) -out ../$(DIST_DIR)

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
