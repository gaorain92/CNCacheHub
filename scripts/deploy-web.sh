#!/usr/bin/env bash
# scripts/deploy-web.sh — 把 web/dist 增量推送到测试机。
#
# 用法：
#   ./scripts/deploy-web.sh
#   DEPLOY_HOST=user@1.2.3.4 ./scripts/deploy-web.sh
#   DEPLOY_REMOTE_DIR=/srv/cnch/web ./scripts/deploy-web.sh
#
# 关键：scp -r dist/. 不会删 server 端已有的文件（保留多版本共存）。
#       想彻底清旧：在 server 上 `rm -rf $REMOTE_DIR/*` 再跑本脚本。
#
# 教训（2026-07-22）：曾用 `ls -t | tail | xargs rm` 按时间排序清 dist/chunks，
# 把 main bundle 通过 () => import() 动态 import 的 view chunks 删光，
# 浏览器打开后所有路由 chunk 404。本脚本只"加新文件"，不做"减旧文件"。

set -euo pipefail

HOST="${DEPLOY_HOST:-root@117.55.237.250}"
REMOTE_DIR="${DEPLOY_REMOTE_DIR:-/opt/cncachehub/web/dist}"
PASSWORD_FILE="${DEPLOY_PASSWORD_FILE:-$HOME/.ssh/.cnch_pw}"
LOCAL_DIST="$(cd "$(dirname "$0")/../web/dist" && pwd)"

if [[ ! -d "$LOCAL_DIST" ]]; then
  echo "❌ $LOCAL_DIST 不存在 — 先跑 cd web && npm run build" >&2
  exit 1
fi
if [[ ! -f "$LOCAL_DIST/index.html" ]]; then
  echo "❌ $LOCAL_DIST/index.html 不存在 — build 失败?" >&2
  exit 1
fi
if ! command -v sshpass >/dev/null; then
  echo "❌ sshpass 没装 — brew install sshpass" >&2
  exit 1
fi

HOST_ONLY="${HOST#*@}"

echo "▶ 推 dist 到 $HOST:$REMOTE_DIR"
sshpass -f "$PASSWORD_FILE" scp -o StrictHostKeyChecking=accept-new -o ConnectTimeout=10 -r \
  "$LOCAL_DIST/." "$HOST:$REMOTE_DIR/"

echo
echo "▶ 验证主页 + main bundle 是否 200"
echo -n "  GET /             => "
curl -s -o /dev/null -w "HTTP %{http_code} %{size_download}b\n" --max-time 5 "http://$HOST_ONLY/"
INDEX_JS=$(grep -oE 'assets/index-[A-Za-z0-9_-]+\.js' "$LOCAL_DIST/index.html" | head -1)
echo -n "  GET /$INDEX_JS  => "
curl -s -o /dev/null -w "HTTP %{http_code} %{size_download}b\n" --max-time 5 "http://$HOST_ONLY/$INDEX_JS"

echo
echo "✅ deploy-web 完成"
echo "  浏览器 hard-reload: Cmd+Shift+R 清掉 main bundle 缓存"
echo "  想验证所有 view chunk: ssh 到 server 跑 scripts/check-chunks.sh"
