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

.PHONY: help dev build test release release-upload install clean lint type-check

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
	@printf '%s\n' \
		'{' \
		'  "name": "cncachehub",' \
		'  "version": "$(VERSION)",' \
		'  "commit": "$(CNCH_COMMIT)",' \
		'  "buildDate": "$(CNCH_DATE)",' \
		'  "components": {' \
		'    "server": {' \
		'      "binary": "cncachehub-server-$(VERSION)-linux-amd64"' \
		'    },' \
		'    "web": {' \
		'      "archive": "cncachehub-web-$(VERSION).tar.gz"' \
		'    }' \
		'  }' \
		'}' > $(DIST_DIR)/manifest.json
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

# === 上传 release 到 GitHub ===
# 用法: make release-upload VERSION=v0.1.0
# 依赖: gh CLI 已登录（gh auth status）
# 行为: 先跑 release 打 Linux amd64 制品，然后用 gh release create 上传
#
# 可选变量:
#   REPO = owner/repo  （默认从 git remote origin 推断）
#   NOTES_FILE = path/to/notes.md  （默认用 git log 上一个 tag 起的 commits）
release-upload: release ## 打制品并上传到 GitHub Releases（依赖 gh CLI + 登录 + 权限）
	@if [ -z "$(VERSION)" ]; then echo "✗ VERSION 没设，用法：make release-upload VERSION=v0.1.0"; exit 1; fi
	@if ! command -v gh >/dev/null 2>&1; then \
		echo "✗ gh CLI 没装，先装：brew install gh  或  https://cli.github.com/"; \
		exit 1; \
	fi
	@if ! gh auth status >/dev/null 2>&1; then \
		echo "✗ gh 没登录，先跑：gh auth login"; \
		exit 1; \
	fi
	@REPO=$(REPO); \
	if [ -z "$$REPO" ]; then \
		REPO=$$(git remote get-url origin 2>/dev/null | sed -E 's#^.*github.com[:/]+##; s#\.git$$##'); \
	fi; \
	if [ -z "$$REPO" ]; then \
		echo "✗ 推断不到 repo，设 REPO=owner/repo 重跑"; \
		exit 1; \
	fi; \
	echo "→ 上传 release 到 $$REPO @ $(VERSION)"; \
	NOTES_FLAG=""; \
	if [ -n "$(NOTES_FILE)" ] && [ -f "$(NOTES_FILE)" ]; then \
		NOTES_FLAG="--notes-file $(NOTES_FILE)"; \
	else \
		# 自动从上一个 tag 起的 commit 摘 changelog
		PREV_TAG=$$(git describe --tags --abbrev=0 2>/dev/null || true); \
		if [ -n "$$PREV_TAG" ]; then \
			RANGE="$$PREV_TAG..HEAD"; \
		else \
			RANGE="HEAD~20..HEAD"; \
		fi; \
		NOTES_FILE_TMP=$$(mktemp); \
		echo "## Changes" > $$NOTES_FILE_TMP; \
		echo "" >> $$NOTES_FILE_TMP; \
		git log --oneline --no-decorate $$RANGE >> $$NOTES_FILE_TMP 2>/dev/null || true; \
		NOTES_FLAG="--notes-file $$NOTES_FILE_TMP"; \
	fi; \
	gh release create "$(VERSION)" \
		$(DIST_DIR)/cncachehub-$(VERSION)-linux-amd64.tar.gz \
		$(DIST_DIR)/cncachehub-server-$(VERSION)-darwin-amd64 2>/dev/null || true \
		--repo "$$REPO" \
		--title "CNCacheHub $(VERSION)" \
		$$NOTES_FLAG \
		--target "$(shell git rev-parse HEAD)"; \
	echo ""; \
	echo "✓ Release 上传完：https://github.com/$$REPO/releases/tag/$(VERSION)"

# 验证 release 制品结构（CI / 本地用）
release-verify: ## 验证 release 制品结构（manifest.json + 关键文件）
	@if [ -z "$(VERSION)" ]; then echo "✗ VERSION 没设"; exit 1; fi
	@ARCHIVE=$(DIST_DIR)/cncachehub-$(VERSION)-linux-amd64.tar.gz; \
	if [ ! -f "$$ARCHIVE" ]; then \
		echo "✗ $$ARCHIVE 不存在，先跑 make release VERSION=$(VERSION)"; \
		exit 1; \
	fi; \
	echo "检查 $$ARCHIVE 内容..."; \
	tar -tzf "$$ARCHIVE"; \
	echo ""; \
	rm -rf /tmp/cnch-verify && mkdir -p /tmp/cnch-verify; \
	tar -xzf "$$ARCHIVE" -C /tmp/cnch-verify; \
	if [ -f /tmp/cnch-verify/manifest.json ]; then \
		echo "✓ manifest.json 存在"; \
		cat /tmp/cnch-verify/manifest.json; \
	else \
		echo "✗ manifest.json 缺失"; \
		rm -rf /tmp/cnch-verify; \
		exit 1; \
	fi; \
	rm -rf /tmp/cnch-verify; \
	echo ""; \
	echo "✓ 制品结构正确"

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
