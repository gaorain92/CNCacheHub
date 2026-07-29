#!/usr/bin/env bash
# scripts/install.sh — CNCacheHub 一键部署脚本
#
# 三种模式：
#   interactive  缺省。逐步问关键参数，缺省值回车即可
#   express      必填项全部用缺省值，跳过所有交互，最快
#   expert       暴露所有可调参数（端口/路径/小VPS优化/TLS/registry...）
#
# 两种执行位置：
#   local        在部署目标机器上跑（要求 docker）
#   remote       在开发机跑，自动 ssh 到目标机器执行
#
# 常用：
#   ./scripts/install.sh                              # 交互模式
#   ./scripts/install.sh --mode=express               # 极速
#   ./scripts/install.sh --mode=expert \
#     --admin-password=xxx --http-port=8080 --tls-mode=off
#   ./scripts/install.sh --mode=express \
#     --host=root@1.2.3.4 --ssh-key=~/.ssh/id_rsa     # 远程部署
#   ./scripts/install.sh update --mode=express        # 增量升级
#   ./scripts/install.sh uninstall --purge            # 完全卸载
#
# 退出码：
#   0  成功
#   1  通用错误
#   2  依赖缺失（docker / ssh / sshpass）
#   3  用户中断
#   4  SSH 连接失败
#   5  部署后健康检查失败
#
# 兼容 macOS（brew install sshpass）+ Debian/Ubuntu（apt install sshpass）

set -euo pipefail

# ============================================================================
# 路径 & 常量
# ============================================================================
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
DEPLOY_DIR="$PROJECT_ROOT/deploy"
# 生成配置写到 deploy/generated/，原 deploy/{Caddyfile,docker-compose.yml} 保留为手写模板
GENERATED_DIR="$DEPLOY_DIR/generated"

CNCH_VERSION="0.1.0"
CNCH_IMAGE_TAG="cncachehub/server:${CNCH_VERSION}"
CNCH_DATA_DIR_DEFAULT="/var/lib/cncachehub"
CNCH_CACHE_DIR_DEFAULT="/var/lib/cncachehub/cache"
CNCH_HTTP_PORT_DEFAULT=80
CNCH_DASHBOARD_PORT_DEFAULT=8080
CNCH_ADMIN_PASSWORD_DEFAULT=""
CNCH_SMALL_VPS_DEFAULT="true"
CNCH_TLS_MODE_DEFAULT="off"   # off | self-signed | letsencrypt
CNCH_DOMAIN_DEFAULT=""
CNCH_ADMIN_EMAIL_DEFAULT=""
CNCH_REGISTRY_MIRROR_DEFAULT="auto"  # auto | off | <custom>
CNCH_PUBLIC_BASE_URL_DEFAULT=""

# ============================================================================
# 颜色 & 输出
# ============================================================================
if [[ -t 1 ]]; then
  C_RED=$'\033[0;31m'
  C_GRN=$'\033[0;32m'
  C_YEL=$'\033[0;33m'
  C_BLU=$'\033[0;34m'
  C_DIM=$'\033[2m'
  C_BLD=$'\033[1m'
  C_OFF=$'\033[0m'
else
  C_RED="" C_GRN="" C_YEL="" C_BLU="" C_DIM="" C_BLD="" C_OFF=""
fi

log()   { echo -e "${C_BLU}▶${C_OFF} $*"; }
ok()    { echo -e "${C_GRN}✓${C_OFF} $*"; }
warn()  { echo -e "${C_YEL}⚠${C_OFF} $*" >&2; }
err()   { echo -e "${C_RED}✗${C_OFF} $*" >&2; }
hr()    { echo -e "${C_DIM}─────────────────────────────────────────────────${C_OFF}"; }
title() { echo -e "\n${C_BLD}${C_BLU}$*${C_OFF}"; }

# ============================================================================
# 帮助
# ============================================================================
usage() {
  cat <<EOF
CNCacheHub 一键部署脚本 v${CNCH_VERSION}

用法:
  $0 [子命令] [参数]

子命令:
  init          初始化部署（默认）— 全栈首次安装
  update        增量升级 — 重新 build 镜像 + 重启服务
  uninstall     卸载 — 停服务 + 删容器，可选 --purge 删数据

部署模式:
  --mode=MODE        interactive（默认）| express | expert
  --location=LOC     local（默认）| remote

远程部署:
  --host=USER@HOST   目标主机（仅 remote 模式）
  --ssh-key=PATH     私钥文件（默认 ~/.ssh/id_rsa）
  --ssh-port=PORT    SSH 端口（默认 22）
  --ssh-password     使用密码登录（默认走密钥；需要 sshpass）

核心参数:
  --admin-password=PASS    管理员密码（必填；express 模式默认随机生成）
  --http-port=PORT         Caddy HTTP 端口（默认 80）
  --data-dir=PATH          数据目录（默认 ${CNCH_DATA_DIR_DEFAULT}）
  --cache-dir=PATH         缓存目录（默认 ${CNCH_CACHE_DIR_DEFAULT}）

注意：Go API 内部端口固定 8080（与 Caddy 反代配合），用户不暴露。

TLS（expert 模式可见）:
  --tls-mode=MODE          off | self-signed | letsencrypt（默认 off）
  --domain=DOMAIN          TLS 域名（letsencrypt 必填）
  --admin-email=EMAIL      证书注册邮箱（letsencrypt 必填）

高级（expert 模式可见）:
  --small-vps=true|false   启用小VPS优化（默认 true，限单对象 1MB / 缓存 20GB）
  --registry-mirror=MODE   auto | off | URL（默认 auto — 自动配 Docker 镜像）
  --public-base-url=URL    控制台显示的"公开访问地址"

通用:
  -h, --help               打印此帮助
  -V, --version            打印版本
  --dry-run                只打印要执行的命令，不真跑
  --non-interactive        等同于 --mode=express
  --purge                  uninstall 时一并删除数据目录

环境变量（覆盖默认）:
  CNCH_ADMIN_PASSWORD  CNCH_HTTP_PORT  CNCH_TLS_MODE  CNCH_DOMAIN 等
EOF
}

version() { echo "CNCacheHub install script v${CNCH_VERSION}"; }

# ============================================================================
# 参数解析
# ============================================================================
SUBCOMMAND="init"
MODE="interactive"
LOCATION="local"
DRY_RUN=0
PURGE=0

# SSH 远程相关
SSH_HOST=""
SSH_KEY="$HOME/.ssh/id_rsa"
SSH_PORT="22"
SSH_USE_PASSWORD=0

# 核心参数（带缺省）
ADMIN_PASSWORD="${CNCH_ADMIN_PASSWORD:-}"
HTTP_PORT="${CNCH_HTTP_PORT:-}"
DATA_DIR="${CNCH_DATA_DIR:-$CNCH_DATA_DIR_DEFAULT}"
CACHE_DIR="${CNCH_CACHE_DIR:-$CNCH_CACHE_DIR_DEFAULT}"

# TLS / 域名
TLS_MODE=""
DOMAIN="${CNCH_DOMAIN:-}"
ADMIN_EMAIL="${CNCH_ADMIN_EMAIL:-}"

# 高级
SMALL_VPS_OPT="${CNCH_SMALL_VPS_OPT:-}"
REGISTRY_MIRROR=""
PUBLIC_BASE_URL="${CNCH_PUBLIC_BASE_URL:-}"

parse_args() {
  while [[ $# -gt 0 ]]; do
    case "$1" in
      init|update|uninstall) SUBCOMMAND="$1"; shift ;;
      --mode=*)        MODE="${1#*=}"; shift ;;
      --location=*)    LOCATION="${1#*=}"; shift ;;
      --host=*)        SSH_HOST="${1#*=}"; shift ;;
      --ssh-key=*)     SSH_KEY="${1#*=}"; shift ;;
      --ssh-port=*)    SSH_PORT="${1#*=}"; shift ;;
      --ssh-password)  SSH_USE_PASSWORD=1; shift ;;
      --admin-password=*) ADMIN_PASSWORD="${1#*=}"; shift ;;
      --admin-password) shift; ADMIN_PASSWORD="$1"; shift ;;
      --http-port=*)   HTTP_PORT="${1#*=}"; shift ;;
      --http-port)     shift; HTTP_PORT="$1"; shift ;;
      --data-dir=*)    DATA_DIR="${1#*=}"; shift ;;
      --cache-dir=*)   CACHE_DIR="${1#*=}"; shift ;;
      --tls-mode=*)    TLS_MODE="${1#*=}"; shift ;;
      --domain=*)      DOMAIN="${1#*=}"; shift ;;
      --admin-email=*) ADMIN_EMAIL="${1#*=}"; shift ;;
      --small-vps=*)   SMALL_VPS_OPT="${1#*=}"; shift ;;
      --registry-mirror=*) REGISTRY_MIRROR="${1#*=}"; shift ;;
      --public-base-url=*) PUBLIC_BASE_URL="${1#*=}"; shift ;;
      --dry-run)       DRY_RUN=1; shift ;;
      --purge)         PURGE=1; shift ;;
      --non-interactive) MODE="express"; shift ;;
      -h|--help)       usage; exit 0 ;;
      -V|--version)    version; exit 0 ;;
      -*)              err "未知参数: $1"; usage >&2; exit 1 ;;
      *)               err "多余位置参数: $1"; usage >&2; exit 1 ;;
    esac
  done
}

# ============================================================================
# 工具
# ============================================================================
gen_password() {
  # 22 字符 base62（足够强度 + 看得清）
  LC_ALL=C tr -dc 'A-Za-z0-9' </dev/urandom | head -c 22
}

random_hex() {
  LC_ALL=C tr -dc '0-9a-f' </dev/urandom | head -c 16
}

run() {
  if [[ $DRY_RUN -eq 1 ]]; then
    echo -e "${C_DIM}  DRY: $*${C_OFF}"
  else
    "$@"
  fi
}

# dry-run 包装：在 dry-run 模式下把命令 echo 后返回成功
run_dry() {
  if [[ $DRY_RUN -eq 1 ]]; then
    echo -e "${C_DIM}  DRY: $*${C_OFF}"
    return 0
  fi
  "$@"
}

# dry-run 时不真的跑前置依赖检查
check_local_deps() {
  if [[ $DRY_RUN -eq 1 ]]; then
    log "[dry-run] 跳过 docker / curl 检查"
    return 0
  fi
  local missing=0
  for cmd in docker curl; do
    if ! command -v "$cmd" >/dev/null 2>&1; then
      err "缺少依赖: $cmd"
      missing=1
    fi
  done
  if ! docker info >/dev/null 2>&1; then
    err "Docker 守护进程没跑或没权限（试试: sudo usermod -aG docker \$USER）"
    missing=1
  fi
  return $missing
}

# 远程执行封装 — local 模式直接跑，remote 模式 ssh 过去
remote_or_local() {
  if [[ "$LOCATION" == "remote" ]]; then
    if [[ -n "$SSH_KEY" && -f "$SSH_KEY" && $SSH_USE_PASSWORD -eq 0 ]]; then
      ssh -i "$SSH_KEY" -p "$SSH_PORT" -o StrictHostKeyChecking=accept-new \
        -o ConnectTimeout=10 "$SSH_HOST" "$@"
    else
      sshpass -p "$REMOTE_SSH_PASS" ssh -p "$SSH_PORT" \
        -o StrictHostKeyChecking=accept-new -o ConnectTimeout=10 \
        "$SSH_HOST" "$@"
    fi
  else
    "$@"
  fi
}

scp_to_remote() {
  local src="$1"
  local dst="$2"
  if [[ "$LOCATION" == "remote" ]]; then
    if [[ -n "$SSH_KEY" && -f "$SSH_KEY" && $SSH_USE_PASSWORD -eq 0 ]]; then
      scp -i "$SSH_KEY" -P "$SSH_PORT" -o StrictHostKeyChecking=accept-new \
        -o ConnectTimeout=10 "$src" "${SSH_HOST}:${dst}"
    else
      sshpass -p "$REMOTE_SSH_PASS" scp -P "$SSH_PORT" \
        -o StrictHostKeyChecking=accept-new -o ConnectTimeout=10 \
        "$src" "${SSH_HOST}:${dst}"
    fi
  else
    cp -r "$src" "$dst"
  fi
}

confirm() {
  local prompt="$1"
  local default="${2:-n}"
  local answer
  if [[ "$MODE" == "express" ]]; then
    [[ "$default" == "y" ]] && return 0 || return 1
  fi
  read -r -p "$(echo -e "${C_BLU}?${C_OFF} $prompt [$default] ")" answer
  answer="${answer:-$default}"
  [[ "$answer" =~ ^[Yy] ]]
}

prompt_value() {
  local prompt="$1"
  local default="$2"
  local secret="${3:-}"
  local answer
  if [[ "$MODE" == "express" ]]; then
    echo -n "$default"
    return 0
  fi
  if [[ "$secret" == "secret" ]]; then
    read -r -s -p "$(echo -e "${C_BLU}?${C_OFF} $prompt [${C_DIM}<hidden>${C_OFF}${default:+ ($default)}] ")" answer
    echo
  else
    read -r -p "$(echo -e "${C_BLU}?${C_OFF} $prompt [${default}] ")" answer
  fi
  answer="${answer:-$default}"
  echo "$answer"
}

# ============================================================================
# 依赖检查
# ============================================================================
check_local_deps() {
  : # 实际定义在 run() 之后（dry-run 包装版本）
}

check_remote_deps() {
  local missing=0
  for cmd in ssh; do
    if ! command -v "$cmd" >/dev/null 2>&1; then
      err "缺少依赖: $cmd"
      missing=1
    fi
  done
  if [[ $SSH_USE_PASSWORD -eq 1 ]] && ! command -v sshpass >/dev/null 2>&1; then
    err "用密码登录需要 sshpass（brew install sshpass / apt install sshpass）"
    missing=1
  fi
  if [[ -n "$SSH_KEY" && ! -f "$SSH_KEY" && $SSH_USE_PASSWORD -eq 0 ]]; then
    err "SSH 密钥不存在: $SSH_KEY"
    missing=1
  fi
  return $missing
}

# ============================================================================
# 子命令：交互 + 参数解析
# ============================================================================

wizard_init() {
  title "🧙  CNCacheHub 部署向导"
  echo -e "${C_DIM}回车用默认值；Ctrl-C 随时退出${C_OFF}"
  hr

  # 1) 部署位置
  if [[ "$MODE" != "express" ]]; then
    if [[ -z "$LOCATION" || "$LOCATION" == "local" ]]; then
      echo "部署位置:"
      echo "  1) 本机（已经在这台机器上，默认）"
      echo "  2) 远程机器（需要 SSH）"
      local loc_choice
      read -r -p "$(echo -e "${C_BLU}?${C_OFF} 选择 [1]: ")" loc_choice
      loc_choice="${loc_choice:-1}"
      if [[ "$loc_choice" == "2" ]]; then
        LOCATION="remote"
      fi
    fi

    # 远程模式额外问
    if [[ "$LOCATION" == "remote" && -z "$SSH_HOST" ]]; then
      SSH_HOST=$(prompt_value "目标主机 (user@host)" "root@")
      [[ -z "$SSH_HOST" || "$SSH_HOST" == "root@" ]] && { err "必须提供 host"; exit 1; }
      SSH_PORT=$(prompt_value "SSH 端口" "$SSH_PORT")
      if [[ -f "$SSH_KEY" ]]; then
        if ! confirm "用现成 SSH 密钥 $SSH_KEY ?" "y"; then
          SSH_KEY=$(prompt_value "SSH 私钥路径" "$HOME/.ssh/id_ed25519")
        fi
      else
        SSH_KEY=$(prompt_value "SSH 私钥路径" "$HOME/.ssh/id_ed25519")
        if [[ ! -f "$SSH_KEY" ]]; then
          warn "密钥不存在，切换到密码模式"
          SSH_USE_PASSWORD=1
        fi
      fi
      if [[ $SSH_USE_PASSWORD -eq 1 ]]; then
        read -r -s -p "$(echo -e "${C_BLU}?${C_OFF} SSH 密码: ")" REMOTE_SSH_PASS
        echo
        [[ -z "$REMOTE_SSH_PASS" ]] && { err "密码不能为空"; exit 1; }
      fi
    fi
  fi

  # 2) 核心参数
  if [[ "$MODE" == "expert" ]]; then
    HTTP_PORT=$(prompt_value "Caddy HTTP 端口" "${HTTP_PORT:-$CNCH_HTTP_PORT_DEFAULT}")
    DATA_DIR=$(prompt_value "数据目录" "${DATA_DIR:-$CNCH_DATA_DIR_DEFAULT}")
    CACHE_DIR=$(prompt_value "缓存目录" "${CACHE_DIR:-$CNCH_CACHE_DIR_DEFAULT}")
  else
    HTTP_PORT="${HTTP_PORT:-$CNCH_HTTP_PORT_DEFAULT}"
    DATA_DIR="${DATA_DIR:-$CNCH_DATA_DIR_DEFAULT}"
    CACHE_DIR="${CACHE_DIR:-$CNCH_CACHE_DIR_DEFAULT}"
  fi

  # 3) 管理员密码
  if [[ -z "$ADMIN_PASSWORD" ]]; then
    if [[ "$MODE" == "express" ]]; then
      ADMIN_PASSWORD=$(gen_password)
      log "随机生成管理员密码: ${C_BLD}${ADMIN_PASSWORD}${C_OFF}"
    else
      echo
      echo "管理员密码（首次登录用,以后可在控制台修改）:"
      local p1 p2
      while true; do
        read -r -s -p "$(echo -e "${C_BLU}?${C_OFF} 密码 [留空=随机生成]: ")" p1
        echo
        if [[ -z "$p1" ]]; then
          ADMIN_PASSWORD=$(gen_password)
          log "随机生成密码: ${C_BLD}${ADMIN_PASSWORD}${C_OFF}"
          break
        fi
        read -r -s -p "$(echo -e "${C_BLU}?${C_OFF} 再次输入: ")" p2
        echo
        if [[ "$p1" == "$p2" ]]; then
          ADMIN_PASSWORD="$p1"
          break
        fi
        warn "两次密码不一致，重新输入"
      done
    fi
  fi

  # 4) TLS（仅 expert）
  if [[ "$MODE" == "expert" ]]; then
    echo
    echo "TLS 模式:"
    echo "  1) off (默认 — HTTP only, 适合测试/内网)"
    echo "  2) self-signed (自签证书, 浏览器会警告)"
    echo "  3) letsencrypt (需要公网域名 + 80/443 可达)"
    local tls_choice
    read -r -p "$(echo -e "${C_BLU}?${C_OFF} 选择 [1]: ")" tls_choice
    tls_choice="${tls_choice:-1}"
    case "$tls_choice" in
      2) TLS_MODE="self-signed" ;;
      3) TLS_MODE="letsencrypt"
         DOMAIN=$(prompt_value "域名" "${DOMAIN:-}")
         ADMIN_EMAIL=$(prompt_value "证书注册邮箱" "${ADMIN_EMAIL:-}")
         [[ -z "$DOMAIN" || -z "$ADMIN_EMAIL" ]] && { err "letsencrypt 需要域名+邮箱"; exit 1; }
         ;;
      *) TLS_MODE="off" ;;
    esac
  else
    TLS_MODE="${TLS_MODE:-$CNCH_TLS_MODE_DEFAULT}"
  fi

  # 5) 高级（仅 expert）
  if [[ "$MODE" == "expert" ]]; then
    SMALL_VPS_OPT=$(prompt_value "小VPS优化 (true|false)" "${SMALL_VPS_OPT:-$CNCH_SMALL_VPS_DEFAULT}")
    REGISTRY_MIRROR=$(prompt_value "Docker registry 镜像 (auto|off|<url>)" "${REGISTRY_MIRROR:-$CNCH_REGISTRY_MIRROR_DEFAULT}")
    PUBLIC_BASE_URL=$(prompt_value "公开 Base URL（控制台里显示给用户的访问地址）" "${PUBLIC_BASE_URL:-http://}")
  else
    SMALL_VPS_OPT="${SMALL_VPS_OPT:-$CNCH_SMALL_VPS_DEFAULT}"
    REGISTRY_MIRROR="${REGISTRY_MIRROR:-$CNCH_REGISTRY_MIRROR_DEFAULT}"
    PUBLIC_BASE_URL="${PUBLIC_BASE_URL:-}"
  fi

  # 6) 总结
  title "📋  配置总结"
  cat <<EOF
  部署位置:    ${LOCATION}$([[ "$LOCATION" == "remote" ]] && echo " → $SSH_HOST")
  HTTP 端口:   $HTTP_PORT$([[ "$TLS_MODE" == "letsencrypt" ]] && echo " + 443")
  数据目录:    $DATA_DIR
  缓存目录:    $CACHE_DIR
  管理员密码:  ${ADMIN_PASSWORD:0:4}*** (${#ADMIN_PASSWORD} 字符)
  小VPS优化:   $SMALL_VPS_OPT
  TLS:         $TLS_MODE$([[ -n "$DOMAIN" ]] && echo " ($DOMAIN)")
  Registry:    $REGISTRY_MIRROR
EOF
  hr

  if [[ "$MODE" != "express" ]]; then
    if ! confirm "开始部署?" "y"; then
      err "用户取消"
      exit 3
    fi
  fi
}

wizard_update() {
  title "🔄  增量升级"
  if [[ "$MODE" != "express" ]]; then
    if ! confirm "确认要重新 build + restart 服务吗?" "y"; then
      err "用户取消"
      exit 3
    fi
  fi
}

wizard_uninstall() {
  title "🗑️   卸载 CNCacheHub"
  if [[ $PURGE -eq 0 && "$MODE" != "express" ]]; then
    if confirm "同时删除数据目录 (${DATA_DIR}) 和缓存?" "n"; then
      PURGE=1
    fi
  fi
  if [[ "$MODE" != "express" ]]; then
    if ! confirm "确认卸载?" "y"; then
      err "用户取消"
      exit 3
    fi
  fi
}

# ============================================================================
# 执行：init
# ============================================================================

build_env_file() {
  mkdir -p "$GENERATED_DIR"
  local env_file="$GENERATED_DIR/.env"
  cat > "$env_file" <<EOF
# Generated by install.sh v${CNCH_VERSION} on $(date -u +"%Y-%m-%dT%H:%M:%SZ")
ADMIN_PASSWORD=${ADMIN_PASSWORD}
HTTP_PORT=${HTTP_PORT}
DASHBOARD_PORT=${CNCH_DASHBOARD_PORT_DEFAULT}
DATA_DIR=${DATA_DIR}
CACHE_DIR=${CACHE_DIR}
SMALL_VPS_OPT=${SMALL_VPS_OPT}
TLS_MODE=${TLS_MODE}
DOMAIN=${DOMAIN}
ADMIN_EMAIL=${ADMIN_EMAIL}
REGISTRY_MIRROR=${REGISTRY_MIRROR}
PUBLIC_BASE_URL=${PUBLIC_BASE_URL}
TZ=$(date +%Z | grep -q "CST\|China" && echo "Asia/Shanghai" || echo "UTC")
EOF
  ok "已生成 $env_file"
}

generate_compose() {
  mkdir -p "$GENERATED_DIR"
  local compose="$GENERATED_DIR/docker-compose.yml"
  # 基础模板（根据 TLS 模式追加端口 + Caddyfile 选择）
  local expose_caddy="80"
  if [[ "$TLS_MODE" == "letsencrypt" ]]; then
    expose_caddy="80 443"
  fi

  cat > "$compose" <<EOF
# 由 install.sh 生成 — 不要手工改，用 install.sh update 重生
services:
  server:
    build:
      context: ../server
      dockerfile: Dockerfile
    container_name: cncachehub-server
    restart: unless-stopped
    expose:
      - "${CNCH_DASHBOARD_PORT_DEFAULT}"
    environment:
      - CNCH_DATA_DIR=/var/lib/cncachehub
      - CNCH_CACHE_DIR=/var/lib/cncachehub/cache
      - CNCH_HTTP_ADDR=:${CNCH_DASHBOARD_PORT_DEFAULT}
      - CNCH_LOG_LEVEL=\${CNCH_LOG_LEVEL:-info}
      - CNCH_ADMIN_PASSWORD=\${ADMIN_PASSWORD}
      - CNCH_SMALL_VPS_OPT=\${SMALL_VPS_OPT:-true}
      - CNCH_PUBLIC_BASE_URL=\${PUBLIC_BASE_URL:-}
      - TZ=\${TZ:-UTC}
    volumes:
      - cncachehub_data:/var/lib/cncachehub
      - cncachehub_cache:/var/lib/cncachehub/cache
    healthcheck:
      test: ["CMD", "wget", "--spider", "-q", "http://localhost:${CNCH_DASHBOARD_PORT_DEFAULT}/healthz"]
      interval: 30s
      timeout: 5s
      retries: 3

  web:
    build:
      context: ../web
      dockerfile: Dockerfile
    container_name: cncachehub-web
    restart: unless-stopped
    expose:
      - "80"
    healthcheck:
      test: ["CMD", "wget", "--spider", "-q", "http://localhost:80/"]
      interval: 30s
      timeout: 5s
      retries: 3

  caddy:
    image: caddy:2-alpine
    container_name: cncachehub-caddy
    restart: unless-stopped
    ports:
$([[ "$TLS_MODE" == "letsencrypt" ]] && echo "      - \"80:80\"" && echo "      - \"443:443\"" || echo "      - \"\${HTTP_PORT:-80}:\${HTTP_PORT:-80}\"")
    volumes:
      - ./Caddyfile:/etc/caddy/Caddyfile:ro
      - caddy_data:/data
      - caddy_config:/config
    depends_on:
      server:
        condition: service_healthy
      web:
        condition: service_healthy

volumes:
  cncachehub_data:
  cncachehub_cache:
  caddy_data:
  caddy_config:
EOF
  ok "已生成 $compose"
}

generate_caddyfile() {
  mkdir -p "$GENERATED_DIR"
  local caddyfile="$GENERATED_DIR/Caddyfile"
  case "$TLS_MODE" in
    off|self-signed)
      cat > "$caddyfile" <<EOF
# HTTP 模式（无域名或自签证书）
:80 {
    reverse_proxy /api/* server:${CNCH_DASHBOARD_PORT_DEFAULT}
    reverse_proxy /healthz server:${CNCH_DASHBOARD_PORT_DEFAULT}
    reverse_proxy /metrics server:${CNCH_DASHBOARD_PORT_DEFAULT}
    reverse_proxy / web:80
    header {
        X-Frame-Options "SAMEORIGIN"
        X-Content-Type-Options "nosniff"
        -Server
    }
    encode gzip zstd
}
EOF
      ;;
    letsencrypt)
      cat > "$caddyfile" <<EOF
# HTTPS 模式 — ${DOMAIN}
${DOMAIN} {
    reverse_proxy /api/* server:${CNCH_DASHBOARD_PORT_DEFAULT}
    reverse_proxy /healthz server:${CNCH_DASHBOARD_PORT_DEFAULT}
    reverse_proxy /metrics server:${CNCH_DASHBOARD_PORT_DEFAULT}
    reverse_proxy / web:80
    header {
        X-Frame-Options "SAMEORIGIN"
        X-Content-Type-Options "nosniff"
        Strict-Transport-Security "max-age=31536000; includeSubDomains"
        -Server
    }
    encode gzip zstd
    tls ${ADMIN_EMAIL}
}
EOF
      ;;
  esac
  ok "已生成 $caddyfile (TLS=$TLS_MODE)"
}

init_docker_registry_mirror() {
  [[ "$REGISTRY_MIRROR" == "off" ]] && { log "跳过 registry mirror 配置"; return; }
  local mirror_url=""
  case "$REGISTRY_MIRROR" in
    auto) mirror_url="https://docker.m.daocloud.io" ;;
    http://*|https://*) mirror_url="$REGISTRY_MIRROR" ;;
    *) return ;;
  esac

  local daemon_json="/etc/docker/daemon.json"
  if [[ "$LOCATION" == "remote" ]]; then
    warn "请在目标机器上手动配置 Docker daemon:"
    echo "  sudo tee $daemon_json <<JSON"
    echo "  { \"registry-mirrors\": [\"$mirror_url\"] }"
    echo "  JSON"
    echo "  sudo systemctl restart docker"
    return
  fi
  if [[ ! -d /etc/docker ]]; then
    warn "/etc/docker 不存在，跳过 registry mirror 配置"
    return
  fi
  if [[ -f "$daemon_json" ]]; then
    warn "$daemon_json 已存在，未自动覆盖；如需镜像加速请手动加 registry-mirrors"
    return
  fi
  if confirm "在 $daemon_json 配置 registry mirror: $mirror_url ?" "n"; then
    run sudo mkdir -p /etc/docker
    run sudo tee "$daemon_json" >/dev/null <<JSON
{
  "registry-mirrors": ["$mirror_url"]
}
JSON
    run sudo systemctl restart docker
    ok "已配置 Docker 镜像加速"
  fi
}

do_init() {
  title "🚀  开始部署 CNCacheHub"
  hr

  # 0) 依赖检查
  log "检查依赖..."
  if [[ "$LOCATION" == "local" ]]; then
    check_local_deps || exit 2
  else
    check_remote_deps || exit 2
    # 远程还需要目标机有 docker
    if ! remote_or_local command -v docker >/dev/null 2>&1; then
      err "目标机器没装 docker — 先 apt install docker.io / yum install docker"
      exit 2
    fi
  fi

  # 1) 生成配置
  log "生成配置文件..."
  if [[ "$LOCATION" == "local" ]]; then
    build_env_file
    generate_compose
    generate_caddyfile
  else
    # 远程：先在本地生成，scp 过去
    build_env_file
    generate_compose
    generate_caddyfile
    log "推送配置到 $SSH_HOST ..."
    scp_to_remote "$GENERATED_DIR/.env" "/tmp/cnch.env"
    scp_to_remote "$GENERATED_DIR/docker-compose.yml" "/tmp/cnch-docker-compose.yml"
    scp_to_remote "$GENERATED_DIR/Caddyfile" "/tmp/cnch-Caddyfile"
    run remote_or_local "sudo mkdir -p /opt/cncachehub/deploy"
    run remote_or_local "sudo mv /tmp/cnch.env /tmp/cnch-docker-compose.yml /tmp/cnch-Caddyfile /opt/cncachehub/deploy/"
  fi

  # 2) Docker compose up
  log "启动服务 (build + up)..."
  if [[ "$LOCATION" == "local" ]]; then
    cd "$GENERATED_DIR"
    run docker compose pull caddy 2>/dev/null || true
    run docker compose up -d --build
    cd "$PROJECT_ROOT"
  else
    run remote_or_local "cd /opt/cncachehub/deploy/generated && sudo docker compose pull caddy 2>/dev/null || true"
    run remote_or_local "cd /opt/cncachehub/deploy/generated && sudo docker compose up -d --build"
  fi

  # 3) 等健康检查
  if [[ $DRY_RUN -eq 1 ]]; then
    log "[dry-run] 跳过健康检查"
  else
    log "等待健康检查..."
    sleep 5
    if [[ "$LOCATION" == "local" ]]; then
      if ! curl -sf --max-time 5 "http://localhost:${HTTP_PORT}/healthz" >/dev/null; then
        err "健康检查失败 — 跑 docker compose -f $GENERATED_DIR/docker-compose.yml logs 看错误"
        exit 5
      fi
    else
      if ! remote_or_local curl -sf --max-time 5 "http://localhost:${HTTP_PORT}/healthz" >/dev/null; then
        err "健康检查失败 — ssh $SSH_HOST 跑 docker compose -f /opt/cncachehub/deploy/generated/docker-compose.yml logs 看错误"
        exit 5
      fi
    fi
  fi

  # 4) 摘要
  title "✅  部署完成"
  local public_url
  if [[ "$TLS_MODE" == "letsencrypt" && -n "$DOMAIN" ]]; then
    public_url="https://${DOMAIN}"
  else
    local host="${SSH_HOST#*@}"
    [[ -z "$host" ]] && host="$(hostname -I 2>/dev/null | awk '{print $1}' || true)"
    [[ -z "$host" ]] && host="$(ipconfig getifaddr en0 2>/dev/null || ipconfig getifaddr en1 2>/dev/null || echo "127.0.0.1")"
    [[ -z "$host" ]] && host="<this-host>"
    public_url="http://${host}:${HTTP_PORT}"
  fi

  cat <<EOF
  控制台:  ${public_url}
  用户名:  root
  密码:    ${ADMIN_PASSWORD}
  
  后续管理:
EOF
  if [[ "$LOCATION" == "local" ]]; then
    cat <<EOF
    cd $GENERATED_DIR && docker compose logs -f      # 看日志
    cd $GENERATED_DIR && docker compose restart      # 重启
    $0 update --mode=express                        # 升级
    $0 uninstall [--purge]                          # 卸载
EOF
  else
    cat <<EOF
    ssh $SSH_HOST 'cd /opt/cncachehub/deploy && sudo docker compose logs -f'
    $0 update --mode=express --host=$SSH_HOST     # 升级
    $0 uninstall --host=$SSH_HOST [--purge]       # 卸载
EOF
  fi

  # 5) 提示存密码
  warn "管理员密码只显示这一次！建议立即登入控制台修改或抄到密码管理器。"
}

# ============================================================================
# 执行：update
# ============================================================================
do_update() {
  log "拉新代码..."
  if [[ "$LOCATION" == "local" ]]; then
    if [[ -d "$PROJECT_ROOT/.git" ]]; then
      run git -C "$PROJECT_ROOT" pull --ff-only
    fi
  else
    warn "remote 模式 update 不会 git pull — 请先在开发机器上 pull + 重新跑 install.sh"
  fi

  log "重新 build + restart..."
  if [[ "$LOCATION" == "local" ]]; then
    cd "$GENERATED_DIR"
    run docker compose build --pull --no-cache
    run docker compose up -d
    cd "$PROJECT_ROOT"
  else
    run remote_or_local "cd /opt/cncachehub/deploy && sudo docker compose build --pull --no-cache"
    run remote_or_local "cd /opt/cncachehub/deploy && sudo docker compose up -d"
  fi

  log "等健康检查..."
  if [[ $DRY_RUN -eq 1 ]]; then
    log "[dry-run] 跳过健康检查"
  else
    sleep 5
    if [[ "$LOCATION" == "local" ]]; then
      curl -sf --max-time 5 "http://localhost:${HTTP_PORT}/healthz" >/dev/null && ok "健康" || err "健康检查失败"
    else
      remote_or_local curl -sf --max-time 5 "http://localhost:${HTTP_PORT}/healthz" >/dev/null && ok "健康" || err "健康检查失败"
    fi
  fi
  ok "更新完成"
}

# ============================================================================
# 执行：uninstall
# ============================================================================
do_uninstall() {
  log "停服务 + 删容器..."
  if [[ "$LOCATION" == "local" ]]; then
    cd "$GENERATED_DIR"
    run docker compose down --remove-orphans || true
    cd "$PROJECT_ROOT"
  else
    run remote_or_local "cd /opt/cncachehub/deploy/generated && sudo docker compose down --remove-orphans" || true
  fi

  if [[ $PURGE -eq 1 ]]; then
    warn "PURGE 模式：删数据 + 缓存 + 镜像"
    if [[ "$LOCATION" == "local" ]]; then
      run sudo rm -rf "$DATA_DIR"
      run docker volume rm cncachehub_cncachehub_data cncachehub_cncachehub_cache cncachehub_caddy_data cncachehub_caddy_config 2>/dev/null || true
      run docker rmi "$CNCH_IMAGE_TAG" 2>/dev/null || true
    else
      run remote_or_local "sudo rm -rf '$DATA_DIR'"
      run remote_or_local "sudo docker volume rm cncachehub_cncachehub_data cncachehub_cncachehub_cache cncachehub_caddy_data cncachehub_caddy_config" || true
    fi
    ok "数据已删除"
  fi
  ok "卸载完成"
}

# ============================================================================
# Main
# ============================================================================
main() {
  parse_args "$@"

  # 校验模式
  case "$MODE" in
    interactive|express|expert) ;;
    *) err "未知模式: $MODE"; exit 1 ;;
  esac
  case "$LOCATION" in
    local|remote) ;;
    *) err "未知位置: $LOCATION"; exit 1 ;;
  esac

  # 远程模式但没给 host
  if [[ "$LOCATION" == "remote" && -z "$SSH_HOST" && "$SUBCOMMAND" != "uninstall" ]]; then
    err "remote 模式必须 --host=USER@HOST"
    exit 1
  fi

  # 远程模式 + 密码登录
  if [[ "$LOCATION" == "remote" && $SSH_USE_PASSWORD -eq 1 && -z "${REMOTE_SSH_PASS:-}" ]]; then
    err "密码登录需要 REMOTE_SSH_PASS 环境变量"
    exit 1
  fi

  case "$SUBCOMMAND" in
    init)
      wizard_init
      do_init
      ;;
    update)
      wizard_update
      do_update
      ;;
    uninstall)
      wizard_uninstall
      do_uninstall
      ;;
  esac
}

main "$@"
