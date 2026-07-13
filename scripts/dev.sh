#!/usr/bin/env bash
# 一键启动开发环境
# 启动后端（端口 8080）和前端（端口 5173），Ctrl+C 全部停止

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

cleanup() {
    echo ""
    echo "[dev] 停止所有子进程..."
    kill 0 2>/dev/null || true
}
trap cleanup EXIT INT TERM

# 启动后端
(
    cd "$ROOT_DIR/server"
    if [ ! -d "vendor" ] && [ -f "go.sum" ]; then
        echo "[dev] server: go mod download"
        go mod download
    fi
    echo "[dev] server: go run ./cmd/cncachehub (http://localhost:8080)"
    go run ./cmd/cncachehub
) &

# 启动前端
(
    cd "$ROOT_DIR/web"
    if [ ! -d "node_modules" ]; then
        echo "[dev] web: npm install"
        npm install
    fi
    echo "[dev] web: vite dev (http://localhost:5173)"
    npm run dev
) &

echo ""
echo "[dev] 全部启动完成"
echo "  - 前端控制台: http://localhost:5173"
echo "  - 后端 API:   http://localhost:8080"
echo "  - 健康检查:   curl http://localhost:8080/healthz"
echo ""
echo "Ctrl+C 停止"
wait
