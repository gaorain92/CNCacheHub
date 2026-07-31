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
CNCH_DATA_DIR_DEFAULT="/var/lib/cncachehub/data"
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
# 子脚本：systemd 模式专用
# ============================================================================
if [[ -f "$SCRIPT_DIR/install-systemd.sh" ]]; then
  # shellcheck source=install-systemd.sh
  source "$SCRIPT_DIR/install-systemd.sh"
fi

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
  --runtime=RUNTIME  docker（默认）| systemd
                     docker   = docker compose + server/web/caddy 容器
                     systemd  = Go 二进制 + nginx + systemd（不需要 docker）
  --source=SRC       local（默认）| git | release
                     local   = 用 \$PROJECT_ROOT 当前源码（开发模式）
                     git     = git fetch + checkout ref + build（保证最新 commit）
                     release = 从 GitHub Releases 拉预编译 tarball（保证最新 release）
  --version=VER      latest（默认）| vX.Y.Z | <git-ref>
  --git-url=URL      自定义 git remote URL（默认用 \$PROJECT_ROOT 的 origin）
  --release-url=URL  自定义 release 主机（默认 https://github.com/cncachehub/cncachehub/releases）

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
RUNTIME="docker"   # docker | systemd
DRY_RUN=0
PURGE=0

# 源码来源（怎么拿到要安装的代码 / 制品）
SOURCE="local"     # local | git | release
VERSION="latest"   # latest | vX.Y.Z | <git-ref>
GIT_URL=""         # 自定义 git remote URL（默认用当前 origin）
RELEASE_URL=""     # 自定义 release 主机（默认走 GitHub releases）

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
      --runtime=*)     RUNTIME="${1#*=}"; RUNTIME_CLI_SET=1; shift ;;
      --source=*)      SOURCE="${1#*=}"; SOURCE_CLI_SET=1; shift ;;
      --version=*)     VERSION="${1#*=}"; shift ;;
      --git-url=*)     GIT_URL="${1#*=}"; shift ;;
      --release-url=*) RELEASE_URL="${1#*=}"; shift ;;
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
  # macOS head close pipe 会让 tr SIGPIPE → 加 || true 防 set -e 误判
  LC_ALL=C tr -dc 'A-Za-z0-9' </dev/urandom | head -c 22 || true
}

random_hex() {
  LC_ALL=C tr -dc '0-9a-f' </dev/urandom | head -c 16 || true
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
  for cmd in curl; do
    if ! command -v "$cmd" >/dev/null 2>&1; then
      err "缺少依赖: $cmd"
      missing=1
    fi
  done
  # docker 是必需的；缺了的话下面 offer_install_docker 会处理
  if ! command -v docker >/dev/null 2>&1; then
    err "缺少依赖: docker"
    missing=1
  elif ! docker info >/dev/null 2>&1; then
    err "Docker 守护进程没跑或没权限（试试: sudo usermod -aG docker \$USER && newgrp docker）"
    missing=1
  fi
  return $missing
}

# ============================================================================
# Linux 发行版识别 — 5 大族：apt / dnf / yum / pacman / apk
# ============================================================================
# 解析 /etc/os-release，把 (id, family) 写到全局变量 DETECTED_DISTRO / PKG_FAMILY
# 不支持 macOS（BSD 工具链，Docker 走 Docker Desktop 不在这条路径）
DETECTED_DISTRO=""
PKG_FAMILY=""

detect_distro() {
  # macOS 走 brew，不在这里处理
  if [[ "$(uname -s 2>/dev/null)" == "Darwin" ]]; then
    DETECTED_DISTRO="macos"
    PKG_FAMILY="brew"
    return 0
  fi
  # 没有 /etc/os-release 的非 Linux 系统
  if [[ ! -f /etc/os-release ]]; then
    DETECTED_DISTRO="unknown"
    PKG_FAMILY="unknown"
    return 1
  fi
  # shellcheck disable=SC1091
  . /etc/os-release
  DETECTED_DISTRO="${ID:-unknown}"
  case "$ID" in
    # apt 族
    debian|ubuntu|linuxmint|elementary|pop|raspbian|kali|neon|zorin)
      PKG_FAMILY="apt" ;;
    # dnf 族（Fedora / RHEL 9+ / Alma / Rocky / CentOS Stream 9+）
    fedora|rhel|rocky|almalinux|centos|nobara|openmandriva)
      PKG_FAMILY="dnf" ;;
    # yum 族（CentOS 7 / RHEL 7 / Oracle Linux 7 / Amazon Linux 2）
    ol|amzn|centos-stream|rhel-7|cloudlinux|scientific)
      PKG_FAMILY="yum" ;;
    # pacman 族
    arch|manjaro|endeavouros|arcolinux|garuda)
      PKG_FAMILY="pacman" ;;
    # apk 族（Alpine）
    alpine)
      PKG_FAMILY="apk" ;;
    # openSUSE / SUSE
    opensuse*|sles|suse)
      PKG_FAMILY="zypper" ;;
    *)
      # 兜底 — 看哪个命令在
      if command -v apt-get >/dev/null 2>&1; then PKG_FAMILY="apt"
      elif command -v dnf >/dev/null 2>&1; then PKG_FAMILY="dnf"
      elif command -v yum >/dev/null 2>&1; then PKG_FAMILY="yum"
      elif command -v pacman >/dev/null 2>&1; then PKG_FAMILY="pacman"
      elif command -v apk >/dev/null 2>&1; then PKG_FAMILY="apk"
      else PKG_FAMILY="unknown"
      fi
      ;;
  esac
  log "检测到系统: ${C_BLD}${DETECTED_DISTRO}${C_OFF} (${PRETTY_NAME:-$ID}) → 包管理器: ${C_BLD}${PKG_FAMILY}${C_OFF}"
  return 0
}

# 跨发行版装 docker（缺哪个装哪个）
# 必须在 detect_distro 之后调
install_docker() {
  local pm="$PKG_FAMILY"
  log "尝试用 ${pm} 装 docker..."
  case "$pm" in
    apt)
      # Debian / Ubuntu 官方仓库的 docker.io 比 docker-ce 简单，单 VPS 够用
      run sudo apt-get update
      run sudo apt-get install -y docker.io
      ;;
    dnf)
      # Fedora / RHEL 9+ / Alma 9+ — dnf-plugins 装 docker-ce 仓库麻烦
      # 直接用发行版自带的 moby-engine（Fedora） 或 podman 替代
      # Fedora 仓库里有 docker 本身（Fedora 39+）
      run sudo dnf install -y docker docker-compose
      ;;
    yum)
      # RHEL 7 / CentOS 7 / Amazon Linux 2 — 装 docker 走 extras 仓库
      run sudo yum install -y yum-utils
      run sudo yum install -y docker
      ;;
    pacman)
      # Arch / Manjaro
      run sudo pacman -Sy --noconfirm docker docker-compose
      ;;
    apk)
      # Alpine
      run sudo apk add --no-cache docker docker-compose
      ;;
    zypper)
      # openSUSE
      run sudo zypper --non-interactive install docker docker-compose
      ;;
    *)
      err "不支持的发行版: ${DETECTED_DISTRO} (${pm})。请手动装 docker 后再跑 install.sh"
      return 1
      ;;
  esac
  # 启动 + 开机自启（systemd 是绝大多数发行版的 init；Alpine 用 OpenRC 单独处理）
  if command -v systemctl >/dev/null 2>&1; then
    run sudo systemctl enable --now docker
  elif command -v rc-update >/dev/null 2>&1 && [[ "$PKG_FAMILY" == "apk" ]]; then
    run sudo rc-update add docker boot
    run sudo service docker start
  fi
  # 当前用户加入 docker 组（避免每次 sudo）
  local user_name
  user_name="${SUDO_USER:-${USER:-root}}"
  if [[ "$user_name" != "root" ]]; then
    run sudo usermod -aG docker "$user_name"
    warn "已把 $user_name 加入 docker 组 — 重新登录或 newgrp docker 生效"
  fi
  ok "docker 装好，验证一下..."
  if ! docker info >/dev/null 2>&1; then
    err "docker 装上了但 daemon 没起来 — 手动 systemctl status docker 看下"
    return 1
  fi
  ok "docker daemon OK"
}

# 检查并尝试装 docker（交互 / express 都走同一条路）
offer_install_docker() {
  if [[ $DRY_RUN -eq 1 ]]; then
    log "[dry-run] 跳过 docker 安装"
    return 0
  fi
  if command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then
    return 0
  fi
  # 没装 / 没起
  detect_distro
  if [[ -z "$PKG_FAMILY" || "$PKG_FAMILY" == "unknown" || "$PKG_FAMILY" == "brew" ]]; then
    err "没装 docker，且无法自动识别发行版。请先手动装 docker 后再跑。brew: brew install --cask docker"
    return 1
  fi
  if [[ "$MODE" == "express" ]]; then
    log "express 模式：自动装 docker..."
    install_docker
    return $?
  fi
  if confirm "检测到没装 docker — 用 ${PKG_FAMILY} 装吗?" "y"; then
    install_docker
    return $?
  fi
  err "需要 docker 才能继续 — 装上再重跑"
  return 1
}

# ============================================================================
# 远程版本的 detect / install（走 remote_or_local 包 SSH）
# 输出到 REMOTE_PKG_FAMILY / REMOTE_DISTRO
# ============================================================================
REMOTE_PKG_FAMILY=""
REMOTE_DISTRO=""

detect_distro_remote() {
  if [[ $DRY_RUN -eq 1 ]]; then
    REMOTE_DISTRO="dry-run"
    REMOTE_PKG_FAMILY="apt"
    return 0
  fi
  # 取 ID 和 ID_LIKE
  local info
  info=$(remote_or_local 'cat /etc/os-release 2>/dev/null | grep -E "^(ID|ID_LIKE)=" || true')
  if [[ -z "$info" ]]; then
    REMOTE_DISTRO="unknown"
    REMOTE_PKG_FAMILY="unknown"
    return 1
  fi
  REMOTE_DISTRO=$(echo "$info" | grep '^ID=' | head -1 | cut -d'=' -f2 | tr -d '"')
  # 判断包管理器
  if remote_or_local "command -v apt-get >/dev/null 2>&1"; then
    REMOTE_PKG_FAMILY="apt"
  elif remote_or_local "command -v dnf >/dev/null 2>&1"; then
    REMOTE_PKG_FAMILY="dnf"
  elif remote_or_local "command -v yum >/dev/null 2>&1"; then
    REMOTE_PKG_FAMILY="yum"
  elif remote_or_local "command -v pacman >/dev/null 2>&1"; then
    REMOTE_PKG_FAMILY="pacman"
  elif remote_or_local "command -v apk >/dev/null 2>&1"; then
    REMOTE_PKG_FAMILY="apk"
  elif remote_or_local "command -v zypper >/dev/null 2>&1"; then
    REMOTE_PKG_FAMILY="zypper"
  else
    REMOTE_PKG_FAMILY="unknown"
  fi
  log "远程系统: ${C_BLD}${REMOTE_DISTRO}${C_OFF} → 包管理器: ${C_BLD}${REMOTE_PKG_FAMILY}${C_OFF}"
  return 0
}

install_docker_remote() {
  # 在远程机器上装 docker（与 install_docker 镜像，但走 remote_or_local）
  local pm="$REMOTE_PKG_FAMILY"
  case "$pm" in
    apt)
      run remote_or_local "sudo apt-get update && sudo apt-get install -y docker.io"
      ;;
    dnf)
      run remote_or_local "sudo dnf install -y docker docker-compose"
      ;;
    yum)
      run remote_or_local "sudo yum install -y yum-utils && sudo yum install -y docker"
      ;;
    pacman)
      run remote_or_local "sudo pacman -Sy --noconfirm docker docker-compose"
      ;;
    apk)
      run remote_or_local "sudo apk add --no-cache docker docker-compose"
      ;;
    zypper)
      run remote_or_local "sudo zypper --non-interactive install docker docker-compose"
      ;;
    *)
      err "不支持的远程发行版: ${REMOTE_DISTRO} (${pm}) — 手动装 docker 后重跑"
      return 1
      ;;
  esac
  # 启动 daemon
  if remote_or_local "command -v systemctl >/dev/null 2>&1"; then
    run remote_or_local "sudo systemctl enable --now docker"
  elif [[ "$pm" == "apk" ]]; then
    run remote_or_local "sudo rc-update add docker boot && sudo service docker start"
  fi
  # 验证
  if remote_or_local "docker info >/dev/null 2>&1"; then
    ok "远程 docker daemon OK"
    return 0
  fi
  err "远程 docker 装上了但 daemon 没起来 — 手动 systemctl status docker 看下"
  return 1
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
  printf "%s" "$(echo -e "${C_BLU}?${C_OFF} $prompt [$default] ")" >&2 || true
  set +e; read -r answer; set -e
  answer="${answer:-$default}"
  [[ "$answer" =~ ^[Yy] ]]
}

prompt_value() {
  local prompt="$1"
  local default="$2"
  local secret="${3:-}"
  local validator="${4:-}"  # 可选：调用 validator "$value" 验证，失败重新问
  local answer
  # express 模式直接返回默认（连 secret 都不问 — 通常密码会用 generate 兜底）
  if [[ "$MODE" == "express" && -z "$secret" ]]; then
    echo -n "$default"
    return 0
  fi
  while true; do
    local read_rc=0
    if [[ "$secret" == "secret" ]]; then
      printf "%s" "$(echo -e "${C_BLU}?${C_OFF} $prompt [${C_DIM}<hidden>${C_OFF}${default:+ ($default chars)}] ")" >&2 || true
      set +e; read -r -s answer; read_rc=$?; set -e
      echo >&2 || true
    else
      printf "%s" "$(echo -e "${C_BLU}?${C_OFF} $prompt [${default}] ")" >&2 || true
      set +e; read -r answer; read_rc=$?; set -e
    fi
    # read_rc != 0 表示 stdin EOF（管道关闭）→ 用 default，不问 validator
    # read_rc == 0 && 空字符串 = 用户空回车 → 走 validator
    if [[ $read_rc -ne 0 ]]; then
      echo "$default"
      return 0
    fi
    answer="${answer:-$default}"
    if [[ -n "$validator" ]]; then
      if err_msg=$("$validator" "$answer" 2>&1); then
        echo "$answer"
        return 0
      else
        warn "输入无效: $err_msg（按 Ctrl-C 退出）"
        continue
      fi
    else
      echo "$answer"
      return 0
    fi
  done
}

prompt_choice() {
  # 多选项菜单
  # usage: prompt_choice "标题" "选项1" "选项2" "选项3" default_idx
  # default_idx 是最后一个位置参数
  # 菜单输出到 stderr，答案输出到 stdout（方便 command substitution 捕获）
  local title="$1"
  shift
  local -a opts=()
  while [[ $# -gt 1 ]]; do
    opts+=("$1")
    shift
  done
  local default_idx="${1:-1}"
  local count=${#opts[@]}
  local i=1
  {
    title "$title"
    for opt in "${opts[@]}"; do
      echo "  $i) $opt"
      i=$((i+1))
    done
  } >&2
  local answer
  while true; do
    # 先写 prompt 到 stderr（避免与 command substitution 捕获的 stdout 混淆）
    printf "%s" "$(echo -e "${C_BLU}?${C_OFF} 选择 [$default_idx]: ")" >&2 || true
    # 临时关 set -e：read 在 stdin EOF/SIGPIPE 时返回 1，会误触发脚本退出
    set +e
    read -r answer
    set -e
    # stdin EOF 时用默认
    [[ -z "$answer" ]] && answer="$default_idx"
    if [[ "$answer" =~ ^[0-9]+$ ]] && (( answer >= 1 && answer <= count )); then
      echo "$answer"
      return 0
    fi
    warn "请输入 1-$count 之间的数字"
  done
}

# ============================================================================
# 输入校验器
# ============================================================================
validate_port() {
  local val="$1"
  if [[ ! "$val" =~ ^[0-9]+$ ]] || (( val < 1 || val > 65535 )); then
    echo "端口必须是 1-65535 之间的整数"; return 1
  fi
}

# 路径必须在白名单前缀下（防 rm -rf /etc / 之类）
validate_path() {
  local val="$1"
  [[ -z "$val" ]] && { echo "路径不能为空"; return 1; }
  if [[ "$val" != /* ]]; then
    echo "路径必须以 / 开头"; return 1
  fi
  # 根路径
  if [[ "$val" == "/" ]]; then
    echo "禁止使用根路径 (/)"; return 1
  fi
  # 系统关键目录（前缀匹配）— /var 单独处理
  local prefix
  for prefix in /bin /sbin /usr /etc /boot /proc /sys /dev /root /lib /lib64 /lib32 /sbin; do
    if [[ "$val" == "$prefix" || "$val" == "$prefix/"* ]]; then
      echo "禁止使用系统目录 ($val) — 选 /opt /srv /home 或 /var/lib 之类"; return 1
    fi
  done
  # /var 子目录只允许 lib（其他是 log/run/tmp/lock/cache 等系统用途）
  if [[ "$val" == "/var" || "$val" == "/var/"* ]]; then
    if [[ "$val" != "/var/lib" && "$val" != "/var/lib/"* ]]; then
      echo "禁止使用 /var 系统子目录 ($val) — /var/lib 之外都是系统用途"
      return 1
    fi
  fi
  # 临时 / 内存文件系统（前缀匹配）
  for prefix in /tmp /var/tmp /run /dev/shm; do
    if [[ "$val" == "$prefix" || "$val" == "$prefix/"* ]]; then
      echo "禁止使用临时目录 ($val) — 容器重启会丢"; return 1
    fi
  done
  # 路径中不能有 . 或 .. 段（防 path traversal）
  if [[ "$val" =~ (^|/)\.\.?(/|$) ]]; then
    echo "路径包含 . 或 .. 相对引用"; return 1
  fi
  # 路径至少 2 层（防 /a）
  local depth
  depth=$(echo "$val" | tr '/' '\n' | grep -cv '^$' || true)
  if (( depth < 2 )); then
    echo "路径至少 2 层 (例: /var/lib/cncachehub)"; return 1
  fi
}

validate_email() {
  local val="$1"
  if [[ ! "$val" =~ ^[^@[:space:]]+@[^@[:space:]]+\.[^@[:space:]]+$ ]]; then
    echo "邮箱格式不对 (例: admin@example.com)"; return 1
  fi
}

validate_host() {
  local val="$1"
  if [[ ! "$val" =~ ^[a-zA-Z0-9_.-]+@[a-zA-Z0-9_.:-]+$ ]]; then
    echo "格式不对 (例: root@1.2.3.4 或 deploy@cnch.example.com)"; return 1
  fi
}

validate_password_strength() {
  local val="$1"
  if [[ ${#val} -lt 8 ]]; then
    echo "密码至少 8 位"; return 1
  fi
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

# 早期校验：CLI 直接传的参数也要走 validator（不依赖 wizard 的 prompt_value）
validate_cli_args() {
  local fail=0
  _check() {
    local name="$1" validator="$2" val="$3"
    if ! "$validator" "$val" >/dev/null 2>&1; then
      err "--$name 不合法: $val"
      "$validator" "$val" >&2 || true
      return 1
    fi
    return 0
  }
  # 注意：必须用 if，不能用 [[ -n X ]] && _check ... || fail=1
  # 否则空 X 走 || 直接 fail=1
  if [[ -n "$DATA_DIR" ]];       then _check data-dir       validate_path "$DATA_DIR"       || fail=1; fi
  if [[ -n "$CACHE_DIR" ]];      then _check cache-dir      validate_path "$CACHE_DIR"      || fail=1; fi
  if [[ -n "$HTTP_PORT" ]];      then _check http-port      validate_port  "$HTTP_PORT"     || fail=1; fi
  if [[ -n "$SSH_HOST" ]];       then _check host           validate_host  "$SSH_HOST"      || fail=1; fi
  if [[ -n "$SSH_PORT" ]];       then _check ssh-port       validate_port  "$SSH_PORT"      || fail=1; fi
  if [[ -n "$ADMIN_EMAIL" ]];    then _check admin-email    validate_email "$ADMIN_EMAIL"   || fail=1; fi
  if [[ -n "$ADMIN_PASSWORD" ]]; then _check admin-password validate_password_strength "$ADMIN_PASSWORD" || fail=1; fi
  # domain 单独校验（没有 validator 函数）
  if [[ -n "$DOMAIN" ]]; then
    if [[ ! "$DOMAIN" =~ ^[a-zA-Z0-9]([a-zA-Z0-9.-]*[a-zA-Z0-9])?\.[a-zA-Z]{2,}$ ]]; then
      err "--domain 不合法: $DOMAIN (例: cache.example.com)"
      fail=1
    fi
  fi
  return $fail
}

# ============================================================================
# 源码解析：local / git / release — 拿到要 build/install 的源码或制品
# 改全局 $PROJECT_ROOT 让后续 build 函数都跟着走
# ============================================================================
CNCH_STAGED_DIR=""   # release 模式解压后的临时目录（方便 cleanup）
CNCH_RESOLVED_VERSION=""   # 最终确定的版本号（manifest / build tag 用）

# git 模式：拉取到 $PROJECT_ROOT
#  - 如果 $PROJECT_ROOT 已是 git repo + 有 origin → git fetch + checkout
#  - 如果不是 git repo + 给了 GIT_URL → git clone
#  - 都不是 → 报错
resolve_source_git() {
  if [[ -d "$PROJECT_ROOT/.git" ]] && git -C "$PROJECT_ROOT" remote -v 2>/dev/null | grep -q origin; then
    log "git 模式: 已在 $PROJECT_ROOT 找到 git 仓库，fetch + checkout"
    run git -C "$PROJECT_ROOT" fetch --tags --prune --prune-tags 2>&1 | tail -3 || true
    local ref="$VERSION"
    if [[ "$VERSION" == "latest" ]]; then
      ref="HEAD"
    fi
    run git -C "$PROJECT_ROOT" checkout "$ref" 2>&1 | tail -2 || true
    # 取当前 commit 短 hash 作为版本号
    local sha
    sha=$(git -C "$PROJECT_ROOT" rev-parse --short HEAD 2>/dev/null || echo "unknown")
    CNCH_RESOLVED_VERSION="git-${sha}"
    log "git 解析: ref=$ref → $CNCH_RESOLVED_VERSION"
    return 0
  fi
  if [[ -n "$GIT_URL" ]]; then
    log "git 模式: 当前目录不是 git 仓库，按 --git-url=$GIT_URL clone"
    # 备份非空目录（避免覆盖）
    local backup=""
    if [[ -n "$(ls -A "$PROJECT_ROOT" 2>/dev/null)" && "$PROJECT_ROOT" != "/" ]]; then
      backup="${PROJECT_ROOT}.bak.$(date +%s)"
      warn "目标目录非空，备份到 $backup"
      run mv "$PROJECT_ROOT" "$backup"
      run mkdir -p "$PROJECT_ROOT"
    fi
    local ref=""
    if [[ "$VERSION" != "latest" ]]; then
      ref="--branch $VERSION"
    fi
    run git clone $ref --depth 1 "$GIT_URL" "$PROJECT_ROOT"
    local sha
    sha=$(git -C "$PROJECT_ROOT" rev-parse --short HEAD 2>/dev/null || echo "unknown")
    CNCH_RESOLVED_VERSION="git-${sha}"
    log "git 解析: → $CNCH_RESOLVED_VERSION"
    return 0
  fi
  err "git 模式需要 git 仓库或 --git-url=<remote>"
  return 1
}

# release 模式：从 GitHub Releases（或其他 host）下载预编译 tarball
# 默认主机 https://github.com/cncachehub/cncachehub/releases
resolve_source_release() {
  if [[ $DRY_RUN -eq 1 ]]; then
    log "[dry-run] 跳过 release 下载"
    return 0
  fi
  local host="${RELEASE_URL:-https://github.com/cncachehub/cncachehub/releases}"
  local version="$VERSION"
  # 1. 解析 version（latest → 查 API；具体 tag 直接用）
  if [[ "$version" == "latest" ]]; then
    log "查 $host 找最新 release…"
    local api_url
    if [[ "$host" == *"github.com"* ]]; then
      api_url="https://api.github.com/repos/cncachehub/cncachehub/releases/latest"
    else
      # 自定义 host（如 Gitea）— 简单约定 /repos/{owner}/{repo}/releases/latest
      api_url="$host/latest"
    fi
    local api_resp
    api_resp=$(curl -fsSL "$api_url" 2>/dev/null || true)
    if [[ -n "$api_resp" ]]; then
      # GitHub API: "tag_name": "vX.Y.Z"
      version=$(echo "$api_resp" | grep -oE '"tag_name":[[:space:]]*"[^"]+"' | head -1 | sed -E 's/.*"([^"]+)"$/\1/')
    fi
    if [[ -z "$version" ]]; then
      err "查不到最新 release — 显式指定 --version=vX.Y.Z 或用 --source=git"
      return 1
    fi
    CNCH_RESOLVED_VERSION="$version"
  else
    CNCH_RESOLVED_VERSION="$version"
  fi
  log "下载 release: $version"

  # 2. 下载 tarball
  local asset="cncachehub-${version}-linux-amd64.tar.gz"
  local download_url
  if [[ "$host" == *"github.com"* ]]; then
    download_url="https://github.com/cncachehub/cncachehub/releases/download/${version}/${asset}"
  else
    download_url="$host/download/${version}/${asset}"
  fi

  local staging="/tmp/cnch-release-${version}-$$"
  CNCH_STAGED_DIR="$staging"
  run mkdir -p "$staging"

  log "GET $download_url → $staging/$asset"
  if ! curl -fsSL --fail -o "$staging/$asset" "$download_url"; then
    err "下载失败 — 检查 --release-url / --version 是否正确"
    rm -rf "$staging"
    return 1
  fi

  log "解压到 $staging/ ..."
  run tar -xzf "$staging/$asset" -C "$staging"
  rm -f "$staging/$asset"
  # 验证 manifest
  if [[ ! -f "$staging/manifest.json" ]]; then
    err "tarball 里没 manifest.json — 不是合法的 release 制品"
    return 1
  fi
  # 切换 PROJECT_ROOT 到 staging
  PROJECT_ROOT="$staging"
  log "release 解析: → $CNCH_RESOLVED_VERSION (staged at $staging)"
  return 0
}

# local 模式：啥也不做，用当前 $PROJECT_ROOT
resolve_source_local() {
  if [[ "$VERSION" != "latest" ]]; then
    warn "--version=$VERSION 对 source=local 模式无效（忽略）— local 模式用当前源码"
  fi
  CNCH_RESOLVED_VERSION="local-dev"
  log "local 模式: 用 $PROJECT_ROOT 当前源码"
}

# 顶层：根据 SOURCE 调用对应解析器
resolve_source() {
  if [[ $DRY_RUN -eq 1 && "$SOURCE" != "local" ]]; then
    # dry-run 跳过 git fetch / release download，但记下来会干啥
    log "[dry-run] source=$SOURCE version=$VERSION"
    CNCH_RESOLVED_VERSION="dry-run"
    return 0
  fi
  case "$SOURCE" in
    local)   resolve_source_local ;;
    git)     resolve_source_git ;;
    release) resolve_source_release ;;
  esac
}

# ============================================================================
# 子命令：交互 + 参数解析
# ============================================================================

wizard_init() {
  title "🧙  CNCacheHub 部署向导"
  echo -e "${C_DIM}回车用默认值；Ctrl-C 随时退出${C_OFF}"
  hr

  # ============= 入口菜单：默认 vs 自定义 vs 完全专家 =============
  # 如果不是 init 子命令，跳过 runtime 询问
  if [[ "$SUBCOMMAND" == "init" ]]; then
    wizard_ask_runtime
  fi

  local main_choice
  if [[ "$MODE" == "express" ]]; then
    main_choice="1"   # express 模式直接走默认
  elif [[ "$MODE" == "expert" ]]; then
    main_choice="3"
  else
    # interactive
    main_choice=$(prompt_choice "选择部署方式" \
      "一键默认（用所有默认值 + 随机管理员密码）" \
      "自定义（我改几个参数）" \
      "完全专家（暴露所有参数）" \
      1)
  fi

  case "$main_choice" in
    1)  # 一键默认 — 只确认两件事：本地还是远程 + 密码是否随机
       wizard_minimal
       ;;
    2)  # 自定义 — 分组菜单驱动
       wizard_custom
       ;;
    3)  # 完全专家 — 一个个问所有
       wizard_expert
       ;;
  esac

  # 总结
  title "📋  配置总结"
  cat <<EOF
  Runtime:    $RUNTIME
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

# 一键默认
wizard_minimal() {
  # 唯一的两个问题：本地/远程 + 密码要不要自己设
  if [[ -z "$SSH_HOST" && -z "${WIZARD_SKIP_LOCATION:-}" ]]; then
    local loc
    loc=$(prompt_choice "部署位置" \
      "本机（当前服务器）" \
      "远程服务器（需要 SSH）" \
      1)
    [[ "$loc" == "2" ]] && LOCATION="remote"
  fi
  if [[ "$LOCATION" == "remote" ]]; then
    wizard_ask_ssh
  fi
  HTTP_PORT="${HTTP_PORT:-$CNCH_HTTP_PORT_DEFAULT}"
  DATA_DIR="${DATA_DIR:-$CNCH_DATA_DIR_DEFAULT}"
  CACHE_DIR="${CACHE_DIR:-$CNCH_CACHE_DIR_DEFAULT}"
  TLS_MODE="${TLS_MODE:-$CNCH_TLS_MODE_DEFAULT}"
  SMALL_VPS_OPT="${SMALL_VPS_OPT:-$CNCH_SMALL_VPS_DEFAULT}"
  REGISTRY_MIRROR="${REGISTRY_MIRROR:-$CNCH_REGISTRY_MIRROR_DEFAULT}"
  PUBLIC_BASE_URL="${PUBLIC_BASE_URL:-}"
  # minimal: 一键默认直接用随机密码，不再问"自己设还是随机"
  if [[ -z "$ADMIN_PASSWORD" ]]; then
    ADMIN_PASSWORD=$(gen_password)
    log "随机生成管理员密码: ${C_BLD}${ADMIN_PASSWORD}${C_OFF}"
  fi
}

# 自定义 — 分组菜单，按需进入
wizard_custom() {
  if [[ -z "$SSH_HOST" && -z "${WIZARD_SKIP_LOCATION:-}" ]]; then
    local loc
    loc=$(prompt_choice "部署位置" "本机" "远程 SSH" 1)
    [[ "$loc" == "2" ]] && LOCATION="remote"
  fi
  if [[ "$LOCATION" == "remote" ]]; then
    wizard_ask_ssh
  fi
  # 默认值
  HTTP_PORT="${HTTP_PORT:-$CNCH_HTTP_PORT_DEFAULT}"
  DATA_DIR="${DATA_DIR:-$CNCH_DATA_DIR_DEFAULT}"
  CACHE_DIR="${CACHE_DIR:-$CNCH_CACHE_DIR_DEFAULT}"
  TLS_MODE="${TLS_MODE:-$CNCH_TLS_MODE_DEFAULT}"
  SMALL_VPS_OPT="${SMALL_VPS_OPT:-$CNCH_SMALL_VPS_DEFAULT}"
  REGISTRY_MIRROR="${REGISTRY_MIRROR:-$CNCH_REGISTRY_MIRROR_DEFAULT}"
  PUBLIC_BASE_URL="${PUBLIC_BASE_URL:-}"
  # 问密码（如果还没设）
  wizard_ask_password

  # 分组菜单
  while true; do
    hr
    echo "  当前配置（回车直接开始部署，选数字改具体项）:"
    printf "   ${C_DIM}[1]${C_OFF} 端口:        %s\n" "$HTTP_PORT"
    printf "   ${C_DIM}[2]${C_OFF} 数据目录:    %s\n" "$DATA_DIR"
    printf "   ${C_DIM}[3]${C_OFF} 缓存目录:    %s\n" "$CACHE_DIR"
    printf "   ${C_DIM}[4]${C_OFF} TLS 模式:    %s\n" "$TLS_MODE"
    printf "   ${C_DIM}[5]${C_OFF} 管理员密码:  %s*** (${#ADMIN_PASSWORD} 字符)\n" "${ADMIN_PASSWORD:0:4}"
    printf "   ${C_DIM}[6]${C_OFF} 小VPS优化:   %s\n" "$SMALL_VPS_OPT"
    printf "   ${C_DIM}[7]${C_OFF} Registry:    %s\n" "$REGISTRY_MIRROR"
    printf "   ${C_DIM}[8]${C_OFF} 公开 URL:    %s\n" "${PUBLIC_BASE_URL:-<未设置>}"
    echo
    printf "   ${C_GRN}[c]${C_OFF} 继续 (用当前配置开始部署)\n"
    printf "   ${C_RED}[q]${C_OFF} 退出\n"
    echo
    local action
    printf "%s" "$(echo -e "${C_BLU}?${C_OFF} 操作 [c]: ")" >&2 || true
    set +e; read -r action; set -e
    action="${action:-c}"
    case "$action" in
      1) HTTP_PORT=$(prompt_value "Caddy HTTP 端口" "$HTTP_PORT" "" "validate_port") ;;
      2) DATA_DIR=$(prompt_value "数据目录" "$DATA_DIR" "" "validate_path") ;;
      3) CACHE_DIR=$(prompt_value "缓存目录" "$CACHE_DIR" "" "validate_path") ;;
      4) wizard_ask_tls ;;
      5) wizard_ask_password ;;
      6) SMALL_VPS_OPT=$(prompt_value "小VPS优化 (true|false)" "$SMALL_VPS_OPT") ;;
      7) REGISTRY_MIRROR=$(prompt_value "Docker registry 镜像 (auto|off|<url>)" "$REGISTRY_MIRROR") ;;
      8) PUBLIC_BASE_URL=$(prompt_value "公开 Base URL（用户从外部访问 CNCH 的地址）" "$PUBLIC_BASE_URL") ;;
      c|C) break ;;
      q|Q) err "用户取消"; exit 3 ;;
      *) warn "未知操作: $action" ;;
    esac
  done
}

# 完全专家 — 问完所有项
wizard_expert() {
  if [[ -z "$SSH_HOST" && -z "${WIZARD_SKIP_LOCATION:-}" ]]; then
    local loc
    loc=$(prompt_choice "部署位置" "本机" "远程 SSH" 1)
    [[ "$loc" == "2" ]] && LOCATION="remote"
  fi
  if [[ "$LOCATION" == "remote" ]]; then
    wizard_ask_ssh
  fi
  HTTP_PORT=$(prompt_value "Caddy HTTP 端口" "${HTTP_PORT:-$CNCH_HTTP_PORT_DEFAULT}" "" "validate_port")
  DATA_DIR=$(prompt_value "数据目录" "${DATA_DIR:-$CNCH_DATA_DIR_DEFAULT}" "" "validate_path")
  CACHE_DIR=$(prompt_value "缓存目录" "${CACHE_DIR:-$CNCH_CACHE_DIR_DEFAULT}" "" "validate_path")
  wizard_ask_tls
  wizard_ask_password
  SMALL_VPS_OPT=$(prompt_value "小VPS优化 (true|false)" "${SMALL_VPS_OPT:-$CNCH_SMALL_VPS_DEFAULT}")
  REGISTRY_MIRROR=$(prompt_value "Docker registry 镜像 (auto|off|<url>)" "${REGISTRY_MIRROR:-$CNCH_REGISTRY_MIRROR_DEFAULT}")
  PUBLIC_BASE_URL=$(prompt_value "公开 Base URL" "${PUBLIC_BASE_URL:-http://}")
}

# 共享：问 runtime
wizard_ask_runtime() {
  # 已经在 CLI 里指定了就跳过
  if [[ -n "${RUNTIME_CLI_SET:-}" ]]; then
    return 0
  fi
  local choice
  choice=$(prompt_choice "部署 runtime" \
    "docker (Docker Compose — 推荐，自包含可移植)" \
    "systemd (Go 二进制 + nginx + systemd — 不需要 docker)" \
    1)
  case "$choice" in
    1) RUNTIME="docker" ;;
    2) RUNTIME="systemd" ;;
  esac
}

# 共享：问 SSH 信息
wizard_ask_ssh() {
  if [[ -z "$SSH_HOST" ]]; then
    SSH_HOST=$(prompt_value "目标主机 (user@host)" "root@" "" "validate_host")
  fi
  SSH_PORT=$(prompt_value "SSH 端口" "$SSH_PORT" "" "validate_port")
  if [[ -f "$SSH_KEY" && -z "${WIZARD_SKIP_KEY:-}" ]]; then
    if ! confirm "用现成 SSH 密钥 $SSH_KEY ?" "y"; then
      SSH_KEY=$(prompt_value "SSH 私钥路径" "$HOME/.ssh/id_ed25519")
    fi
  elif [[ -z "${WIZARD_SKIP_KEY:-}" ]]; then
    SSH_KEY=$(prompt_value "SSH 私钥路径" "$HOME/.ssh/id_ed25519")
  fi
  if [[ ! -f "$SSH_KEY" ]]; then
    warn "密钥不存在: $SSH_KEY — 切换到密码模式"
    SSH_USE_PASSWORD=1
  fi
  if [[ $SSH_USE_PASSWORD -eq 1 ]]; then
    REMOTE_SSH_PASS=$(prompt_value "SSH 密码" "" "secret")
  fi
}

# 共享：问 TLS
wizard_ask_tls() {
  local choice
  choice=$(prompt_choice "TLS 模式" \
    "off (HTTP only, 测试/内网)" \
    "self-signed (自签证书, 浏览器警告)" \
    "letsencrypt (需要公网域名)" \
    1)
  case "$choice" in
    1) TLS_MODE="off" ;;
    2) TLS_MODE="self-signed" ;;
    3) TLS_MODE="letsencrypt"
       DOMAIN=$(prompt_value "域名" "${DOMAIN:-}")
       ADMIN_EMAIL=$(prompt_value "证书注册邮箱" "${ADMIN_EMAIL:-}" "" "validate_email")
       if [[ -z "$DOMAIN" ]]; then
         warn "letsencrypt 需要域名 — 改回 off"
         TLS_MODE="off"
       fi
       ;;
  esac
}

# 共享：问管理员密码
wizard_ask_password() {
  if [[ -n "$ADMIN_PASSWORD" ]] && confirm "已设密码 ${ADMIN_PASSWORD:0:4}***，要改吗?" "n"; then
    ADMIN_PASSWORD=""
  fi
  if [[ -z "$ADMIN_PASSWORD" ]]; then
    if [[ "$MODE" == "express" ]]; then
      ADMIN_PASSWORD=$(gen_password)
      log "随机生成管理员密码: ${C_BLD}${ADMIN_PASSWORD}${C_OFF}"
    else
      local p1 p2
      while true; do
        p1=$(prompt_value "管理员密码 (留空=随机, ≥8 字符)" "" "secret" "validate_password_strength")
        if [[ -z "$p1" ]]; then
          ADMIN_PASSWORD=$(gen_password)
          log "随机生成密码: ${C_BLD}${ADMIN_PASSWORD}${C_OFF}"
          break
        fi
        p2=$(prompt_value "再次输入确认" "" "secret")
        if [[ "$p1" == "$p2" ]]; then
          ADMIN_PASSWORD="$p1"
          break
        fi
        warn "两次密码不一致，重新输入"
      done
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
# 加固项详见 docs/security.md：
#   - cap_drop ALL + 最小 cap_add
#   - no-new-privileges
#   - mem_limit / pids_limit
#   - read_only + tmpfs（server/web）
#   - 不用 host 网络

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
      - CNCH_LOG_DIR=/var/log/cncachehub
      - CNCH_HTTP_ADDR=:${CNCH_DASHBOARD_PORT_DEFAULT}
      - CNCH_LOG_LEVEL=\${CNCH_LOG_LEVEL:-info}
      - CNCH_ADMIN_PASSWORD=\${ADMIN_PASSWORD}
      - CNCH_SMALL_VPS_OPT=\${SMALL_VPS_OPT:-true}
      - CNCH_RESERVE_SPACE_GB=\${RESERVE_SPACE_GB:-5}
      - CNCH_MAX_OBJECT_SIZE_MB=\${MAX_OBJECT_SIZE_MB:-1024}
      - CNCH_CACHE_TOTAL_GB=\${CACHE_TOTAL_GB:-20}
      - CNCH_PUBLIC_BASE_URL=\${PUBLIC_BASE_URL:-}
      - TZ=\${TZ:-UTC}
    volumes:
      - cncachehub_data:/var/lib/cncachehub
      - cncachehub_cache:/var/lib/cncachehub/cache
    # === SECURITY HARDENING ===
    read_only: true
    tmpfs:
      - /tmp:size=100M,mode=1777
      - /var/log:size=100M,mode=1777
    security_opt:
      - no-new-privileges:true
    cap_drop:
      - ALL
    cap_add:
      - CHOWN
      - DAC_OVERRIDE
      - FOWNER
      - SETUID
      - SETGID
    mem_limit: 512m
    memswap_limit: 512m
    pids_limit: 200
    shm_size: 64m
    ulimits:
      nofile:
        soft: 65536
        hard: 65536
    healthcheck:
      test: ["CMD", "wget", "--spider", "-q", "http://127.0.0.1:${CNCH_DASHBOARD_PORT_DEFAULT}/healthz"]
      interval: 30s
      timeout: 5s
      retries: 3
      start_period: 30s

  web:
    build:
      context: ../web
      dockerfile: Dockerfile
    container_name: cncachehub-web
    restart: unless-stopped
    expose:
      - "80"
    # === SECURITY HARDENING ===
    read_only: true
    tmpfs:
      - /var/cache/nginx:size=50M
      - /var/run:size=10M
      - /tmp:size=20M
    security_opt:
      - no-new-privileges:true
    cap_drop:
      - ALL
    cap_add:
      - CHOWN
      - DAC_OVERRIDE
      - FOWNER
      - SETUID
      - SETGID
    mem_limit: 128m
    memswap_limit: 128m
    pids_limit: 50
    healthcheck:
      test: ["CMD", "wget", "--spider", "-q", "http://127.0.0.1:80/"]
      interval: 30s
      timeout: 5s
      retries: 3
      start_period: 20s

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
    # === SECURITY HARDENING ===
    security_opt:
      - no-new-privileges:true
    cap_drop:
      - ALL
    cap_add:
      - NET_BIND_SERVICE
      - CHOWN
      - DAC_OVERRIDE
      - FOWNER
      - SETUID
      - SETGID
    mem_limit: 256m
    memswap_limit: 256m
    pids_limit: 100
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

networks:
  default:
    driver: bridge
EOF
  ok "已生成 $compose"
}

generate_caddyfile() {
  mkdir -p "$GENERATED_DIR"
  local caddyfile="$GENERATED_DIR/Caddyfile"
  local DASH_PORT="${CNCH_DASHBOARD_PORT_DEFAULT:-8080}"
  # 共享的 /metrics IP 白名单 + 安全头段（双引号以便 ${DASH_PORT} 展开）
  local metrics_block="
    @metrics path /metrics
    @internal_net remote_ip 127.0.0.1 10.0.0.0/8 172.16.0.0/12 192.168.0.0/16
    handle @metrics {
        handle @internal_net {
            reverse_proxy server:${DASH_PORT}
        }
        respond \"Not Found\" 404
    }"
  local common_headers='X-Frame-Options "SAMEORIGIN"
        X-Content-Type-Options "nosniff"
        X-XSS-Protection "1; mode=block"
        Referrer-Policy "strict-origin-when-cross-origin"
        Content-Security-Policy "default-src '\''self'\''; img-src '\''self'\'' data:; style-src '\''self'\'' '\''unsafe-inline'\''; script-src '\''self'\''"
        -Server'

  case "$TLS_MODE" in
    off|self-signed)
      cat > "$caddyfile" <<EOF
# HTTP 模式（无域名或自签证书）
# 由 install.sh 生成 — 详见 docs/security.md
:80 {
    request_body {
        max_size 100MB
    }
${metrics_block}
    reverse_proxy /healthz server:${DASH_PORT}
    reverse_proxy /api/* server:${DASH_PORT}
    reverse_proxy / web:80
    header {
        ${common_headers}
    }
    encode gzip zstd
    log {
        output stdout
    }
}
EOF
      ;;
    letsencrypt)
      cat > "$caddyfile" <<EOF
# HTTPS 模式 — ${DOMAIN}（Let's Encrypt 自动续期）
# 由 install.sh 生成 — 详见 docs/security.md
${DOMAIN} {
    request_body {
        max_size 100MB
    }
${metrics_block}
    reverse_proxy /healthz server:${DASH_PORT}
    reverse_proxy /api/* server:${DASH_PORT}
    reverse_proxy / web:80
    header {
        ${common_headers}
        Strict-Transport-Security "max-age=31536000; includeSubDomains"
    }
    encode gzip zstd
    log {
        output stdout
    }
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

  # 0) 依赖检查 — 按 runtime 分支
  if [[ "$RUNTIME" == "systemd" ]]; then
    install_systemd
    return $?
  fi

  # ---- 以下是 docker runtime ----
  log "检查依赖..."
  if [[ "$LOCATION" == "local" ]]; then
    if ! check_local_deps; then
      # docker 缺 / 没起 — 试着自动装（offer_install_docker 内部会问）
      if ! command -v docker >/dev/null 2>&1; then
        offer_install_docker || exit 2
        # 装完再验一次
        check_local_deps || exit 2
      else
        # docker 装了但 daemon 没起
        err "docker 装了但 daemon 没起来 — 跑: sudo systemctl start docker"
        exit 2
      fi
    fi
  else
    check_remote_deps || exit 2
    # 远程还需要目标机有 docker
    if ! remote_or_local command -v docker >/dev/null 2>&1; then
      err "目标机器没装 docker — 先装上 (apt: docker.io / dnf: docker / yum: docker)"
      if confirm "想自动 ssh 过去装 docker 吗?" "y"; then
        detect_distro_remote
        if [[ -n "$REMOTE_PKG_FAMILY" && "$REMOTE_PKG_FAMILY" != "unknown" ]]; then
          log "在 $SSH_HOST 上装 docker ($REMOTE_PKG_FAMILY)..."
          install_docker_remote || exit 2
        else
          err "无法识别远程系统 — 手动装好再重跑"
          exit 2
        fi
      else
        exit 2
      fi
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

  # 4) 摘要 — 按 runtime 分支
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
  Runtime:  $RUNTIME
  控制台:  ${public_url}
  用户名:  root
  密码:    ${ADMIN_PASSWORD}

  后续管理:
EOF
  if [[ "$RUNTIME" == "systemd" ]]; then
    if [[ "$LOCATION" == "local" ]]; then
      cat <<EOF
    sudo systemctl status $CNCH_SERVICE_NAME    # 服务状态
    sudo journalctl -u $CNCH_SERVICE_NAME -f    # 实时日志
    sudo systemctl restart $CNCH_SERVICE_NAME   # 重启服务
    sudo nginx -t && sudo nginx -s reload       # 重载 nginx
    $0 update --runtime=systemd                 # 升级
    $0 uninstall --runtime=systemd [--purge]    # 卸载
EOF
    else
      cat <<EOF
    ssh $SSH_HOST 'sudo systemctl status $CNCH_SERVICE_NAME'
    $0 update --runtime=systemd --host=$SSH_HOST
    $0 uninstall --runtime=systemd --host=$SSH_HOST [--purge]
EOF
    fi
  else
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
  fi

  # 5) 提示存密码
  warn "管理员密码只显示这一次！建议立即登入控制台修改或抄到密码管理器。"
}

# ============================================================================
# 执行：update
# ============================================================================
do_update() {
  # systemd 模式：走 install-systemd.sh 的 update_systemd
  if [[ "$RUNTIME" == "systemd" ]]; then
    if [[ "$LOCATION" == "local" ]]; then
      if [[ -d "$PROJECT_ROOT/.git" ]]; then
        run git -C "$PROJECT_ROOT" pull --ff-only
      fi
    else
      warn "remote 模式 update 不会 git pull — 请先在开发机器上 pull + 重新跑 install.sh"
    fi
    update_systemd
    return $?
  fi

  # ---- 以下是 docker runtime ----
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
  # systemd 模式
  if [[ "$RUNTIME" == "systemd" ]]; then
    uninstall_systemd
    return $?
  fi

  # ---- docker runtime ----
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
  case "$RUNTIME" in
    docker|systemd) ;;
    *) err "未知 runtime: $RUNTIME (支持: docker | systemd)"; exit 1 ;;
  esac
  case "$SOURCE" in
    local|git|release) ;;
    *) err "未知 source: $SOURCE (支持: local | git | release)"; exit 1 ;;
  esac

  # 早期校验：CLI 传的参数也走 validator
  if ! validate_cli_args; then
    err "参数校验失败 — 见上面错误"
    exit 1
  fi

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

  # 解析源码 / 制品（local / git / release）— 改 $PROJECT_ROOT
  if ! resolve_source; then
    err "源码解析失败 (source=$SOURCE version=$VERSION)"
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

  # 清理 release 模式的临时目录（失败时也清）
  if [[ -n "$CNCH_STAGED_DIR" && -d "$CNCH_STAGED_DIR" ]]; then
    rm -rf "$CNCH_STAGED_DIR"
  fi
}

main "$@"
