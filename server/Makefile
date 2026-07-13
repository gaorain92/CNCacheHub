# CNCacheHub server Makefile
#
# 设计原则：
#   - 全部从仓库根目录的 server/ 目录运行；
#   - 不依赖额外工具（go vet 已够用，golangci-lint 留给以后）；
#   - 所有产物在 bin/ 目录下，.gitignore 已忽略。

BINARY        := cncachehub
BIN_DIR       := bin
PKG           := ./...
GO            := go
GOFLAGS       := -trimpath
LDFLAGS       := -s -w
BUILD_TAGS    :=

.PHONY: all build run test lint vet clean tidy

all: build

## build: 编译二进制到 bin/cncachehub
build:
	@mkdir -p $(BIN_DIR)
	$(GO) build $(GOFLAGS) -ldflags='$(LDFLAGS)' -o $(BIN_DIR)/$(BINARY) ./cmd/cncachehub
	@echo "built $(BIN_DIR)/$(BINARY)"

## run: 直接 go run
run:
	$(GO) run ./cmd/cncachehub

## test: 跑全部测试（含 -race）
test:
	$(GO) test $(PKG) -race -v

## lint / vet: go vet
lint vet:
	$(GO) vet $(PKG)

## tidy: go mod tidy
tidy:
	$(GO) mod tidy

## clean: 清理产物
clean:
	rm -rf $(BIN_DIR)
	rm -f coverage.out
