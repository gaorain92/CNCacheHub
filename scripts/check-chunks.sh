#!/usr/bin/env bash
# scripts/check-chunks.sh — 验证 server 上 dist 包含 main bundle 引用的所有 chunk。
# 防止"Vite dist 部署时漏传 view chunk 导致路由 404"再次发生。
#
# 用法：
#   ./scripts/check-chunks.sh                    # 用默认 DEPLOY_HOST
#   DEPLOY_HOST=user@1.2.3.4 ./scripts/check-chunks.sh

set -euo pipefail

HOST="${DEPLOY_HOST:-root@117.55.237.250}"
REMOTE_DIR="${DEPLOY_REMOTE_DIR:-/opt/cncachehub/web/dist}"
PASSWORD_FILE="${DEPLOY_PASSWORD_FILE:-$HOME/.ssh/.cnch_pw}"
LOCAL_DIST="$(cd "$(dirname "$0")/../web/dist" && pwd)"
HOST_ONLY="${HOST#*@}"

if [[ ! -f "$LOCAL_DIST/index.html" ]]; then
  echo "❌ $LOCAL_DIST/index.html 不存在 — 先 cd web && npm run build" >&2
  exit 1
fi

# 本地 main bundle 名 → 远端 main bundle 文件
INDEX_JS_LOCAL=$(grep -oE 'assets/index-[A-Za-z0-9_-]+\.js' "$LOCAL_DIST/index.html" | head -1)
INDEX_JS_REMOTE="assets/index-$(echo "$INDEX_JS_LOCAL" | grep -oE '[A-Za-z0-9_-]+\.js$')"
LOCAL_MAIN="$LOCAL_DIST/$INDEX_JS_LOCAL"

if [[ ! -f "$LOCAL_MAIN" ]]; then
  echo "❌ 本地 main bundle 不存在: $LOCAL_MAIN" >&2
  exit 1
fi

echo "▶ main bundle: $INDEX_JS_LOCAL"
echo "▶ 提取 main bundle 引用的所有 .js chunk 名"

# main bundle 里出现的 .js（裸 filename）—— macOS bash 3.2 兼容
CHUNKS_FILE=$(mktemp)
trap "rm -f $CHUNKS_FILE" EXIT
grep -oE '[A-Za-z0-9_./-]+\.js' "$LOCAL_MAIN" | sort -u > "$CHUNKS_FILE"

MISSING=0
OK=0
while IFS= read -r ref; do
  base=$(basename "$ref")
  # 跳过 main bundle 自身
  if [[ "$base" == "$(basename "$INDEX_JS_LOCAL")" ]]; then
    continue
  fi
  if [[ ! -f "$LOCAL_DIST/assets/$base" ]]; then
    # 不是 view chunk（可能是模块依赖），跳过
    continue
  fi
  if curl -s -o /dev/null --max-time 5 -w "%{http_code}" "http://$HOST_ONLY/assets/$base" | grep -q 200; then
    echo "  ✓ $base"
    OK=$((OK + 1))
  else
    echo "  ✗ MISSING: $base"
    MISSING=$((MISSING + 1))
  fi
done < "$CHUNKS_FILE"

echo
echo "▶ 结果: $OK OK, $MISSING missing"
if [[ $MISSING -gt 0 ]]; then
  echo "❌ $MISSING 个 chunk 缺失 — 重新跑 ./scripts/deploy-web.sh 补传" >&2
  exit 1
fi
echo "✅ 所有 view chunk 在 server 端就位"
