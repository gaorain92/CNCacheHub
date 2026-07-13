#!/usr/bin/env bash
# 端到端冒烟测试 — 验证 server / web 关键路径
# 要求：服务已启动（开发模式或生产 docker-compose）

set -euo pipefail

BASE="${BASE:-http://localhost:8080}"
WEB="${WEB:-http://localhost:5173}"

red() { printf "\033[31m%s\033[0m\n" "$*"; }
green() { printf "\033[32m%s\033[0m\n" "$*"; }
yellow() { printf "\033[33m%s\033[0m\n" "$*"; }

check() {
    local name="$1"
    local cmd="$2"
    if eval "$cmd" >/dev/null 2>&1; then
        green "  ✓ $name"
    else
        red "  ✗ $name"
        FAILED=1
    fi
}

FAILED=0

echo ""
echo "== CNCacheHub 冒烟测试 =="
echo "  后端: $BASE"
echo "  前端: $WEB"
echo ""

# 后端
echo "[后端]"
check "healthz 端点" "curl -fsS '$BASE/healthz'"
check "api healthz 端点" "curl -fsS '$BASE/api/healthz'"
check "GET /api/version" "curl -fsS '$BASE/api/version'"

# 前端
echo "[前端]"
if curl -fsS "$WEB/" >/dev/null 2>&1; then
    green "  ✓ 前端首页可访问"
    # 检查关键标记
    if curl -fsS "$WEB/" | grep -q "CNCacheHub" 2>/dev/null; then
        green "  ✓ 前端包含 CNCacheHub 标识"
    else
        yellow "  ⚠ 前端可访问但未找到 CNCacheHub 标识（可能为开发模式 Vite 壳）"
    fi
else
    red "  ✗ 前端首页不可访问"
    FAILED=1
fi

echo ""
if [ $FAILED -eq 0 ]; then
    green "✓ 冒烟测试通过"
    exit 0
else
    red "✗ 冒烟测试失败"
    exit 1
fi
