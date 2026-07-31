# CNCacheHub Makefile
#
# 用法:
#   make dev              # 启 server + web dev
#   make build            # 编译 server + web
#   make test             # 跑测试
#   make release VERSION=v0.1.0   # 打 release 制品
#   make install          # 装到本机
#   make clean            # 清理 build 产物

CNCH_VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
CNCH_COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "local")
CNCH_DATE    := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
GO_LDFLAGS   := -s -w -X main.version=$(CNCH_VERSION) -X main.commit=$(CNCH_COMMIT)
DIST_DIR     := dist
SERVER_DIR   := server
WEB_DIR      := web

.PHONY: help dev build test release install clean lint type-check

help: ## 打印所有目标
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'

dev: ## 启 dev server（需要两个终端：make dev-server / make dev-web）
	@echo "跑：cd server && go run ./cmd/cncachehub    # 终端 1"
	@echo "    cd web && npm run dev                 # 终端 2"

dev-server:
	cd $(SERVER_DIR) && go run ./cmd/cncachehub

dev-web:
	cd $(WEB_DIR) && npm run dev

build: build-server build-web ## 编译 server + web

build-server: ## 编译 Go server 静态二进制
	cd $(SERVER_DIR) && CGO_ENABLED=0 go build -ldflags="$(GO_LDFLAGS)" -o ../$(DIST_DIR)/cncachehub-server-$$(uname -s | tr A-Z a-z)-$$(uname -m) ./cmd/cncachehub
	@echo "✓ server build 完"

build-web: ## 编译 web dist
	cd $(WEB_DIR) && npm ci && npm run build
	@echo "✓ web build 完"

test: test-server test-web ## 跑所有测试

test-server: ## 跑 Go 测试
	cd $(SERVER_DIR) && go test ./... -count=1

test-web: ## 跑 web type-check + lint
	cd $(WEB_DIR) && npm run type-check && npm run lint

lint: ## 跑 lint
	cd $(SERVER_DIR) && go vet ./...
	cd $(WEB_DIR) && npm run lint

type-check: ## 类型检查
	cd $(SERVER_DIR) && go build ./...
	cd $(WEB_DIR) && npm run type-check

# === Release 制品 ===
# 用法: make release VERSION=v0.1.0
# 产物: dist/cncachehub-v0.1.0-linux-amd64.tar.gz
#       dist/cncachehub-v0.1.0-darwin-amd64.tar.gz
release: clean build-server build-web
	@if [ -z "$(VERSION)" ]; then echo "✗ VERSION 没设，用法：make release VERSION=v0.1.0"; exit 1; fi
	@mkdir -p $(DIST_DIR)
	@echo "打 Linux amd64 制品..."
	cd $(SERVER_DIR) && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="$(GO_LDFLAGS)" -o ../$(DIST_DIR)/cncachehub-server-$(VERSION)-linux-amd64 ./cmd/cncachehub
	cd $(WEB_DIR) && tar -czf ../$(DIST_DIR)/cncachehub-web-$(VERSION).tar.gz dist/
	@cat > $(DIST_DIR)/manifest.json <<EOF
{
  "name": "cncachehub",
  "version": "$(VERSION)",
  "commit": "$(CNCH_COMMIT)",
  "buildDate": "$(CNCH_DATE)",
  "components": {
    "server": {
      "binary": "cncachehub-server-$(VERSION)-linux-amd64"
    },
    "web": {
      "archive": "cncachehub-web-$(VERSION).tar.gz"
    }
  }
}
EOF
	@cd $(DIST_DIR) && tar -czf cncachehub-$(VERSION)-linux-amd64.tar.gz \
		cncachehub-server-$(VERSION)-linux-amd64 \
		cncachehub-web-$(VERSION).tar.gz \
		manifest.json
	@echo ""
	@echo "✓ Release 制品："
	@ls -la $(DIST_DIR)/*.tar.gz $(DIST_DIR)/manifest.json

release-darwin:
	@if [ -z "$(VERSION)" ]; then echo "✗ VERSION 没设"; exit 1; fi
	@mkdir -p $(DIST_DIR)
	cd $(SERVER_DIR) && CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -ldflags="$(GO_LDFLAGS)" -o ../$(DIST_DIR)/cncachehub-server-$(VERSION)-darwin-amd64 ./cmd/cncachehub
	tar -czf $(DIST_DIR)/cncachehub-$(VERSION)-darwin-amd64.tar.gz \
		-C $(DIST_DIR) cncachehub-server-$(VERSION)-darwin-amd64
	@echo "✓ Darwin 制品: $(DIST_DIR)/cncachehub-$(VERSION)-darwin-amd64.tar.gz"

# === 安装 / 卸载 ===
install: ## 跑 install.sh（需要本项目根目录）
	./scripts/install.sh init --runtime=docker

install-systemd: ## systemd 模式装本机
	./scripts/install.sh init --runtime=systemd

update: ## 升级到 latest release
	./scripts/install.sh update --source=release --version=latest

uninstall: ## 卸载
	./scripts/install.sh uninstall

# === 清理 ===
clean: ## 清理 build 产物
	rm -rf $(DIST_DIR)
	rm -rf $(SERVER_DIR)/bin
	rm -rf $(WEB_DIR)/dist
	rm -rf $(WEB_DIR)/node_modules/.vite
	@echo "✓ 清理完"
