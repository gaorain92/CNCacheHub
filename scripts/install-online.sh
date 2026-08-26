#!/usr/bin/env bash
# scripts/install-online.sh — CNCacheHub 一键安装脚本（curl | bash 友好）
#
# 参照 nezha.sh 风格：单文件 + 自包含，env 自动检测 + 几个关键 prompt，
# 内部转给现有 install.sh 跑 express 模式做实际部署。
#
# 用法：
#   # 默认（GitHub）
#   curl -L https://raw.githubusercontent.com/gaorain92/CNCacheHub/main/scripts/install-online.sh | sudo bash
#
#   # 国内用 gitee 镜像
#   curl -L https://gitee.com/gaorain92/CNCacheHub/raw/main/scripts/install-online.sh | sudo CN=true bash
#
#   # 非交互（环境变量预设参数）
#   curl -L ... | sudo CNCH_ADMIN_PASSWORD=mysecret bash
#
#   # 试运行
#   curl -L ... | sudo bash -s -- --dry-run
#
# 它做：
#   1. 检测 arch / init system / CN（用 cloudflare trace）
#   2. 几个关键 prompt（admin 用户名+密码 / install path / http port）
#   3. 从 GitHub raw 拉 install.sh + install-systemd.sh 到 tmp
#   4. 用 express 模式调 install.sh 做实际部署
#   5. 启动后健康检查 + 调 /api/auth/init 建 admin
#   6. 打印 access URL + 凭据
#
# 环境变量（覆盖默认）：
#   CNCH_ADMIN_USER         admin 用户名（默认 cnch）
#   CNCH_ADMIN_PASSWORD     admin 密码（默认自动生成 16 位）
#   CNCH_HTTP_PORT          暴露 HTTP 端口（默认 80）
#   CNCH_DATA_DIR           数据目录（默认 /var/lib/cncachehub/data）
#   CNCH_RUNTIME            systemd | docker（默认 auto）
#   CN=true                 强制走 gitee 镜像
#   CNCH_REPO               仓库 owner/name（默认 gaorain92/CNCacheHub）
#   CNCH_BRANCH             git 分支（默认 main）
#   CNCH_VERSION            版本（默认 latest）
#   CNCH_BASE_URL           实际访问 base URL（默认自动探测；显式用于 init API）

set -euo pipefail

# ============================================================================
# 默认值 + 可覆盖环境变量
# ============================================================================
CNCH_ADMIN_USER="${CNCH_ADMIN_USER:-cnch}"
CNCH_ADMIN_PASSWORD="${CNCH_ADMIN_PASSWORD:-}"
CNCH_HTTP_PORT="${CNCH_HTTP_PORT:-80}"
CNCH_DATA_DIR="${CNCH_DATA_DIR:-/var/lib/cncachehub/data}"
CNCH_RUNTIME="${CNCH_RUNTIME:-}"
CNCH_REPO="${CNCH_REPO:-gaorain92/CNCacheHub}"
CNCH_BRANCH="${CNCH_BRANCH:-main}"
CNCH_VERSION="${CNCH_VERSION:-latest}"
CNCH_BASE_URL="${CNCH_BASE_URL:-}"
CNCH_FORCE_CN="${CNCH_FORCE_CN:-${CN:-}}"

# GitHub raw / 发行版
# 默认仓库为 gaorain92/CNCacheHub（开发者的 fork）；用户自建可
# 覆盖 CNCH_REPO 环境变量。
GITHUB_RAW_BASE="https://raw.githubusercontent.com/${CNCH_REPO}/${CNCH_BRANCH}"
GITEE_RAW_BASE="https://gitee.com/${CNCH_REPO}/raw/${CNCH_BRANCH}"

# ============================================================================
# 颜色（与 nezha.sh 一致）
# ============================================================================
if [[ -t 1 ]]; then
  C_RED=$'\033[0;31m'; C_GRN=$'\033[0;32m'; C_YEL=$'\033[0;33m'
  C_BLU=$'\033[0;34m'; C_DIM=$'\033[2m'; C_OFF=$'\033[0m'
else
  C_RED=""; C_GRN=""; C_YEL=""; C_BLU=""; C_DIM=""; C_OFF=""
fi

err()   { printf "${C_RED}✗ %s${C_OFF}\n" "$*" >&2; }
warn()  { printf "${C_YEL}⚠ %s${C_OFF}\n" "$*"; }
ok()    { printf "${C_GRN}✓ %s${C_OFF}\n" "$*"; }
info()  { printf "${C_BLU}▶ %s${C_OFF}\n" "$*"; }
title() { printf "\n${C_BLU}== %s ==${C_OFF}\n" "$*"; }

# 跨 root / 非 root 通用 sudo 包装（参考 nezha）
sudo_run() {
  if [[ "$(id -ru)" -ne 0 ]]; then
    command sudo "$@"
  else
    "$@"
  fi
}

# ============================================================================
# 检测
# ============================================================================

# 检测架构
detect_arch() {
  local uname_out
  uname_out=$(uname -m)
  case "$uname_out" in
    amd64|x86_64)  OS_ARCH="amd64" ;;
    i386|i686)     OS_ARCH="386" ;;
    aarch64|arm64) OS_ARCH="arm64" ;;
    *armv7*)       OS_ARCH="armv7" ;;
    riscv64)       OS_ARCH="riscv64" ;;
    *) err "不支持的架构: $uname_out"; exit 1 ;;
  esac
}

# 检测 init system（systemd / docker / both）
detect_init() {
  INIT_SYSTEM=""
  if command -v systemctl >/dev/null 2>&1 && [[ -d /run/systemd/system ]]; then
    INIT_SYSTEM="systemd"
  fi
  if command -v docker >/dev/null 2>&1; then
    if [[ -z "$INIT_SYSTEM" ]]; then
      INIT_SYSTEM="docker"
    else
      INIT_SYSTEM="systemd,docker"
    fi
  fi
  if [[ -z "$INIT_SYSTEM" ]]; then
    err "未检测到 systemd 或 docker — 二选一装一个再跑"
    exit 1
  fi
}

# 检测国内（cloudflare trace）
detect_cn() {
  # 强制模式优先
  if [[ -n "$CNCH_FORCE_CN" ]]; then
    USE_CN=true
    return
  fi
  # 试 cloudflare trace，超时 5s
  local trace
  trace=$(curl -A "Mozilla/5.0" -m 5 -s "https://blog.cloudflare.com/cdn-cgi/trace" 2>/dev/null || true)
  if echo "$trace" | grep -qw 'CN'; then
    USE_CN=true
  else
    USE_CN=false
  fi
}

# ============================================================================
# 拉 install.sh + install-systemd.sh
# ============================================================================
fetch_installer() {
  local tmp_dir raw_base
  tmp_dir=$(mktemp -d -t cncachehub-install.XXXXXX)
  INSTALLER_TMP_DIR="$tmp_dir"
  cleanup() { rm -rf "$tmp_dir"; }
  trap cleanup EXIT

  if [[ "$USE_CN" == "true" ]]; then
    raw_base="$GITEE_RAW_BASE"
  else
    raw_base="$GITHUB_RAW_BASE"
  fi

  info "从 $raw_base 拉 install.sh + install-systemd.sh"
  curl -fsSL "$raw_base/scripts/install.sh" -o "$tmp_dir/install.sh" \
    || { err "下载 install.sh 失败（$raw_base）"; return 1; }
  curl -fsSL "$raw_base/scripts/install-systemd.sh" -o "$tmp_dir/install-systemd.sh" \
    || { err "下载 install-systemd.sh 失败（$raw_base）"; return 1; }
  chmod +x "$tmp_dir/install.sh"

  ok "已下载 installer 脚本到 $tmp_dir"
}

# ============================================================================
# 询问关键参数
# ============================================================================
prompt_key_params() {
  # admin 密码
  if [[ -z "$CNCH_ADMIN_PASSWORD" ]]; then
    if [[ -t 0 ]]; then
      printf "${C_BLU}?${C_OFF} admin 密码（留空 = 自动生成 16 位强密码）："
      read -r CNCH_ADMIN_PASSWORD || true
    fi
    if [[ -z "$CNCH_ADMIN_PASSWORD" ]]; then
      # 优先用 openssl，fallback 到 /dev/urandom（Alpine minimal 可能没装 openssl）
      if command -v openssl >/dev/null 2>&1; then
        CNCH_ADMIN_PASSWORD=$(openssl rand -base64 18 | tr -d '/+=' | cut -c1-16)
      else
        CNCH_ADMIN_PASSWORD=$(LC_ALL=C tr -dc 'A-Za-z0-9' </dev/urandom | head -c 16)
      fi
      ok "自动生成 admin 密码: ${C_BLD}${CNCH_ADMIN_PASSWORD}${C_OFF}（首次登录后请改）"
    fi
  fi
}

# ============================================================================
# 决定 runtime
# ============================================================================
choose_runtime() {
  if [[ -n "$CNCH_RUNTIME" ]]; then
    RUNTIME="$CNCH_RUNTIME"
    return
  fi
  # systemd + docker 都在 → 优先 systemd（小 VPS 友好，免 docker 内存）
  if [[ "$INIT_SYSTEM" == "systemd,docker" ]]; then
    RUNTIME="systemd"
    info "检测到 systemd + docker 都在；优先 systemd（小 VPS 友好，免 docker 内存）"
  elif [[ "$INIT_SYSTEM" == "systemd" ]]; then
    RUNTIME="systemd"
  else
    RUNTIME="docker"
  fi
}

# ============================================================================
# 实际调 install.sh
# ============================================================================
run_install() {
  # source 模式：local（从 raw 拉下来的脚本当 source）
  local extra_args=(
    "--mode=express"
    "--runtime=$RUNTIME"
    "--source=local"
    "--location=local"
    "--version=$CNCH_VERSION"
    "--admin-password=$CNCH_ADMIN_PASSWORD"
    "--data-dir=$CNCH_DATA_DIR"
    "--http-port=$CNCH_HTTP_PORT"
    "--non-interactive"
  )

  title "调 install.sh 跑实际部署"
  info "runtime=$RUNTIME  source=local  data-dir=$CNCH_DATA_DIR  http-port=$CNCH_HTTP_PORT"

  sudo_run bash "$INSTALLER_TMP_DIR/install.sh" init "${extra_args[@]}"
}

# ============================================================================
# 健康检查 + admin init
# ============================================================================
probe_base_url() {
  # 优先用用户指定的，否则从 hostname 拿
  if [[ -n "$CNCH_BASE_URL" ]]; then
    BASE_URL="$CNCH_BASE_URL"
    return
  fi
  local ip
  ip=$(hostname -I 2>/dev/null | awk '{print $1}' | head -1)
  if [[ -z "$ip" ]]; then
    ip=$(hostname 2>/dev/null || echo "localhost")
  fi
  BASE_URL="http://${ip}:${CNCH_HTTP_PORT}"
}

# 健康检查：等服务起来（用公开的 /healthz，不需鉴权）
wait_for_healthy() {
  local url="${BASE_URL}/healthz"
  local retries=20
  info "等 $url 健康（最多 20s）"
  while (( retries-- > 0 )); do
    if curl -fsS --max-time 3 "$url" >/dev/null 2>&1; then
      ok "/healthz OK"
      return 0
    fi
    sleep 1
  done
  err "/healthz 没起来 — 跑 systemctl status cncachehub-server 看错误"
  return 1
}

# 检查是否已经 init 过（GET /api/auth/init-status）
is_already_initialized() {
  local url="${BASE_URL}/api/auth/init-status"
  curl -fsS --max-time 5 "$url" 2>/dev/null | grep -q '"initialized":true'
}

# POST /api/auth/init 建 admin
do_admin_init() {
  if is_already_initialized; then
    info "已经初始化过（DB 有 admin）— 跳过 init 步骤"
    return 0
  fi
  local url="${BASE_URL}/api/auth/init"
  info "POST $url  (user=${CNCH_ADMIN_USER})"
  local resp
  resp=$(curl -fsS --max-time 10 -X POST -H "Content-Type: application/json" \
    -d "$(printf '{"username":"%s","password":"%s"}' "$CNCH_ADMIN_USER" "$CNCH_ADMIN_PASSWORD")" \
    "$url" 2>&1) || { err "init 失败: $resp"; return 1; }
  ok "admin 用户已创建: $CNCH_ADMIN_USER"
}

# ============================================================================
# 收尾：打印信息
# ============================================================================
print_banner() {
  cat <<EOF

${C_GRN}== CNCacheHub 安装完成 ==${C_OFF}

  ${C_BLD}访问地址${C_OFF}      ${BASE_URL}
  ${C_BLD}控制台账号${C_OFF}    ${CNCH_ADMIN_USER}
  ${C_BLD}控制台密码${C_OFF}    ${CNCH_ADMIN_PASSWORD}  ${C_YEL}(首次登录后请改)${C_OFF}

  ${C_DIM}常用命令：${C_OFF}
    systemctl status cncachehub-server   # 查看服务状态（systemd）
    journalctl -u cncachehub-server -f   # 看日志
    /usr/local/bin/cncachehub --version  # 看版本
    bash <(curl -L ${GITHUB_RAW_BASE}/scripts/install-online.sh) update  # 升级

  ${C_DIM}文档：${C_OFF}
    https://github.com/${CNCH_REPO}#部署

EOF
}

# ============================================================================
# Main
# ============================================================================
main() {
  title "CNCacheHub 一键安装"

  # 必须 root
  if [[ "$(id -ru)" -ne 0 ]]; then
    err "请用 root 或 sudo 跑（安装要写 /etc/systemd、/usr/local/bin 等）"
    exit 1
  fi

  detect_arch
  detect_init
  detect_cn
  choose_runtime
  prompt_key_params
  probe_base_url

  cat <<EOF
${C_BLU}========================================${C_OFF}
  Architecture : ${OS_ARCH}
  Init system  : ${INIT_SYSTEM}
  Runtime      : ${RUNTIME}
  Mirror       : $([[ "$USE_CN" == "true" ]] && echo "国内 (gitee)" || echo "国际 (github)")
  Repo         : ${CNCH_REPO}
  Branch       : ${CNCH_BRANCH}
  Version      : ${CNCH_VERSION}
  Data dir     : ${CNCH_DATA_DIR}
  HTTP port    : ${CNCH_HTTP_PORT}
  Base URL     : ${BASE_URL}
  Admin user   : ${CNCH_ADMIN_USER}
${C_BLU}========================================${C_OFF}

EOF

  fetch_installer
  run_install
  if wait_for_healthy; then
    do_admin_init || true  # init 失败不致命 — 用户可后续手动 init
  fi
  print_banner
}

main "$@"
