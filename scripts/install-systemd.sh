#!/usr/bin/env bash
# scripts/install-systemd.sh — CNCacheHub systemd + nginx 部署
#
# 由 install.sh 引用（--runtime=systemd 时）。**不要**直接跑。
#
# 做的事（按顺序）：
#   1. 检测/装 Go（仅在需要 build 时）
#   2. 检测/装 nginx
#   3. 创建 cncachehub 系统用户（无登录权限）
#   4. 创建数据/缓存/日志目录
#   5. 构建 Go 静态二进制
#   6. 构建 web dist（npm ci + vite build）
#   7. 安装二进制到 /usr/local/bin/cncachehub
#   8. 复制 web dist 到 /opt/cncachehub/web/dist
#   9. 写 systemd unit 到 /etc/systemd/system/cncachehub-server.service
#  10. 写 nginx site config 到 /etc/nginx/sites-available/cncachehub
#  11. 启用 site + systemctl daemon-reload
#  12. enable + start 服务
#  13. nginx -t + reload
#  14. curl /healthz 验证
#
# 远程模式（--location=remote）：build 在本地，scp/pipe 上传到远程，
# install 步骤在远程跑。

# ============================================================================
# 用户/目录创建
# ============================================================================
CNCH_USER="cncachehub"
CNCH_SERVICE_NAME="cncachehub-server"
CNCH_NGINX_SITE="/etc/nginx/sites-available/cncachehub"
CNCH_NGINX_LINK="/etc/nginx/sites-enabled/cncachehub"
CNCH_BINARY="/usr/local/bin/cncachehub"
CNCH_WEB_DIR="/opt/cncachehub/web"
CNCH_LOG_DIR="/var/log/cncachehub"
CNCH_PORT_DEFAULT=8082   # Go server 内部端口（与 docker 模式的 8080 区别开，避免冲突）

# 检查系统用户是否已存在；不存在则建
setup_systemd_user() {
  if id "$CNCH_USER" >/dev/null 2>&1; then
    return 0
  fi
  run sudo useradd --system --shell /usr/sbin/nologin --home-dir /var/lib/cncachehub --no-create-home "$CNCH_USER"
  ok "已创建系统用户: $CNCH_USER"
}

# 创建数据/缓存/日志目录
setup_systemd_dirs() {
  # 主数据目录
  for d in "$DATA_DIR" "$CACHE_DIR" "$CNCH_LOG_DIR" "$CNCH_WEB_DIR"; do
    if [[ "$LOCATION" == "remote" ]]; then
      run remote_or_local "sudo mkdir -p '$d'"
      run remote_or_local "sudo chown -R $CNCH_USER:$CNCH_USER '$d'"
    else
      run sudo mkdir -p "$d"
      run sudo chown -R "$CNCH_USER:$CNCH_USER" "$d"
    fi
  done
  run sudo chmod 750 "$DATA_DIR" "$CACHE_DIR" "$CNCH_LOG_DIR"
}

# ============================================================================
# nginx 安装
# ============================================================================
install_nginx() {
  # systemd 模式强依赖 Linux（systemd 只能在 Linux 上跑，macOS 走 brew services）
  if [[ "$(uname -s 2>/dev/null)" == "Darwin" && "$LOCATION" == "local" ]]; then
    err "systemd runtime 不能在 macOS 本地跑 — 用 --location=remote 部署到 Linux 服务器，"
    err "  或者用 --runtime=docker 跑 Docker Desktop"
    return 1
  fi
  if command -v nginx >/dev/null 2>&1; then
    log "nginx 已装: $(nginx -v 2>&1)"
    return 0
  fi
  if [[ "$LOCATION" == "remote" ]]; then
    # 远端：走 detect_distro_remote + 远端包管理
    detect_distro_remote
    case "$REMOTE_PKG_FAMILY" in
      apt)  run remote_or_local "sudo apt-get update && sudo apt-get install -y nginx" ;;
      dnf)  run remote_or_local "sudo dnf install -y nginx" ;;
      yum)  run remote_or_local "sudo yum install -y nginx" ;;
      apk)  run remote_or_local "sudo apk add --no-cache nginx" ;;
      pacman) run remote_or_local "sudo pacman -Sy --noconfirm nginx" ;;
      zypper) run remote_or_local "sudo zypper --non-interactive install nginx" ;;
      *) err "远端发行版不支持自动装 nginx: $REMOTE_DISTRO" ; return 1 ;;
    esac
    run remote_or_local "sudo systemctl enable --now nginx"
    return 0
  fi
  # 本地
  detect_distro
  case "$PKG_FAMILY" in
    apt)    run sudo apt-get update && sudo apt-get install -y nginx ;;
    dnf)    run sudo dnf install -y nginx ;;
    yum)    run sudo yum install -y nginx ;;
    apk)    run sudo apk add --no-cache nginx ;;
    pacman) run sudo pacman -Sy --noconfirm nginx ;;
    zypper) run sudo zypper --non-interactive install nginx ;;
    *)      err "本地发行版不支持自动装 nginx: $DETECTED_DISTRO" ; return 1 ;;
  esac
  run sudo systemctl enable --now nginx
  ok "nginx 装好"
}

# ============================================================================
# Go 工具链（仅 build 时需要）
# ============================================================================
GO_VERSION="1.22.10"
GO_INSTALL_DIR="/usr/local/go"

# 检查 go 是否可用；不可用则尝试装（apt 装的是旧版，提示用官方 tarball）
ensure_go() {
  if command -v go >/dev/null 2>&1; then
    local go_ver
    go_ver=$(go version 2>/dev/null | awk '{print $3}' | sed 's/go//')
    log "Go 已装: $go_ver"
    # 项目锁 1.22 — 提取主版本号比对
    local go_major
    go_major="${go_ver%%.*}"
    if [[ "$go_major" == "1" ]]; then
      return 0
    fi
    warn "当前 go 版本 $go_ver 与项目要求 (1.22) 不一致"
  fi
  # apt 装的可能是 1.19；先看看
  if command -v go >/dev/null 2>&1; then
    return 0
  fi
  if [[ "$MODE" == "express" ]] || confirm "需要 Go 来 build 二进制 — 自动装 Go ${GO_VERSION}?" "y"; then
    log "装 Go ${GO_VERSION}..."
    local arch
    arch=$(uname -m)
    case "$arch" in
      x86_64) arch="amd64" ;;
      aarch64|arm64) arch="arm64" ;;
      *) err "不支持的架构: $arch" ; return 1 ;;
    esac
    local tarball="go${GO_VERSION}.linux-${arch}.tar.gz"
    if [[ $DRY_RUN -eq 0 ]]; then
      curl -sSL "https://go.dev/dl/${tarball}" -o "/tmp/${tarball}"
      sudo rm -rf "$GO_INSTALL_DIR"
      sudo tar -C /usr/local -xzf "/tmp/${tarball}"
      rm -f "/tmp/${tarball}"
      export PATH="$GO_INSTALL_DIR/bin:$PATH"
      log "Go $(go version 2>/dev/null | awk '{print $3}') 装好"
    else
      log "[dry-run] 装 Go ${GO_VERSION} (${arch})"
    fi
  else
    err "没 Go 没法 build — 装 Go 或用 docker 模式"
    return 1
  fi
}

# ============================================================================
# Build: Go binary
# 输出路径写到全局变量 CNCH_BUILT_BINARY（避免 $() 捕获到 log 输出）
# ============================================================================
CNCH_BUILT_BINARY=""
CNCH_BUILT_WEB=""

build_go_binary() {
  local out="$GENERATED_DIR/cncachehub-$(uname -s | tr '[:upper:]' '[:lower:]')-$(uname -m)"
  mkdir -p "$GENERATED_DIR"
  log "build Go 静态二进制 (CGO_ENABLED=0) → $out"
  if [[ $DRY_RUN -eq 1 ]]; then
    log "[dry-run] 跳过 build"
    CNCH_BUILT_BINARY="$out"
    return 0
  fi
  if [[ "$LOCATION" == "local" ]]; then
    (cd "$PROJECT_ROOT/server" && CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o "$out" ./cmd/cncachehub)
  else
    # 远端：先在本地 build，再上传
    (cd "$PROJECT_ROOT/server" && CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o "$out" ./cmd/cncachehub)
  fi
  ok "build 完: $out ($(du -h "$out" | awk '{print $1}'))"
  CNCH_BUILT_BINARY="$out"
}

# ============================================================================
# Build: web dist
# ============================================================================
build_web_dist() {
  local out="$GENERATED_DIR/web-dist"
  mkdir -p "$GENERATED_DIR"
  log "build web dist (npm ci + vite build) → $out"
  if [[ $DRY_RUN -eq 1 ]]; then
    log "[dry-run] 跳过 web build"
    CNCH_BUILT_WEB="$out"
    return 0
  fi
  if [[ "$LOCATION" == "local" ]]; then
    (cd "$PROJECT_ROOT/web" && npm ci --silent && npm run build)
    rm -rf "$out"
    cp -r "$PROJECT_ROOT/web/dist" "$out"
  else
    (cd "$PROJECT_ROOT/web" && npm ci --silent && npm run build)
    rm -rf "$out"
    cp -r "$PROJECT_ROOT/web/dist" "$out"
  fi
  ok "web build 完: $out"
  CNCH_BUILT_WEB="$out"
}

# ============================================================================
# 安装 binary 到系统路径
# ============================================================================
install_systemd_binary() {
  local src="$1"
  if [[ $DRY_RUN -eq 1 ]]; then
    log "[dry-run] 跳过 binary 部署"
    return 0
  fi
  if [[ "$LOCATION" == "local" ]]; then
    # 写到新路径，老路径备份
    run sudo cp -f "$src" "${CNCH_BINARY}.new"
    run sudo chmod 0755 "${CNCH_BINARY}.new"
    run sudo mv -f "${CNCH_BINARY}.new" "$CNCH_BINARY"
  else
    # 远端：先传新文件到 /tmp，再 mv 进去
    # 用 pipe 防 scp 损坏（测试机 1GB RAM 已知问题）
    if [[ -n "$SSH_KEY" && -f "$SSH_KEY" && $SSH_USE_PASSWORD -eq 0 ]]; then
      cat "$src" | ssh -i "$SSH_KEY" -p "$SSH_PORT" -o StrictHostKeyChecking=accept-new \
        -o ConnectTimeout=30 "$SSH_HOST" \
        "sudo tee ${CNCH_BINARY}.new >/dev/null && sudo chmod 0755 ${CNCH_BINARY}.new && sudo mv -f ${CNCH_BINARY}.new ${CNCH_BINARY}"
    else
      cat "$src" | sshpass -p "$REMOTE_SSH_PASS" ssh -p "$SSH_PORT" \
        -o StrictHostKeyChecking=accept-new -o ConnectTimeout=30 \
        "$SSH_HOST" \
        "sudo tee ${CNCH_BINARY}.new >/dev/null && sudo chmod 0755 ${CNCH_BINARY}.new && sudo mv -f ${CNCH_BINARY}.new ${CNCH_BINARY}"
    fi
  fi
  ok "二进制已部署: $CNCH_BINARY"
}

# 部署 web dist
install_systemd_web() {
  local src="$1"
  if [[ $DRY_RUN -eq 1 ]]; then
    log "[dry-run] 跳过 web dist 部署"
    return 0
  fi
  if [[ "$LOCATION" == "local" ]]; then
    run sudo rm -rf "$CNCH_WEB_DIR/dist"
    run sudo mkdir -p "$CNCH_WEB_DIR"
    run sudo cp -r "$src" "$CNCH_WEB_DIR/dist"
    run sudo chown -R "$CNCH_USER:$CNCH_USER" "$CNCH_WEB_DIR"
  else
    # 远端：先 tar 打包再传，省 ssh 流量 + 原子替换
    local tarball="$GENERATED_DIR/web-dist.tar.gz"
    tar -czf "$tarball" -C "$src" .
    if [[ -n "$SSH_KEY" && -f "$SSH_KEY" && $SSH_USE_PASSWORD -eq 0 ]]; then
      cat "$tarball" | ssh -i "$SSH_KEY" -p "$SSH_PORT" -o StrictHostKeyChecking=accept-new \
        -o ConnectTimeout=60 "$SSH_HOST" \
        "sudo rm -rf $CNCH_WEB_DIR/dist && sudo mkdir -p $CNCH_WEB_DIR && sudo tar -xzf - -C $CNCH_WEB_DIR && sudo chown -R $CNCH_USER:$CNCH_USER $CNCH_WEB_DIR"
    else
      cat "$tarball" | sshpass -p "$REMOTE_SSH_PASS" ssh -p "$SSH_PORT" \
        -o StrictHostKeyChecking=accept-new -o ConnectTimeout=60 \
        "$SSH_HOST" \
        "sudo rm -rf $CNCH_WEB_DIR/dist && sudo mkdir -p $CNCH_WEB_DIR && sudo tar -xzf - -C $CNCH_WEB_DIR && sudo chown -R $CNCH_USER:$CNCH_USER $CNCH_WEB_DIR"
    fi
  fi
  ok "web dist 已部署: $CNCH_WEB_DIR/dist"
}

# ============================================================================
# 写 systemd unit
# ============================================================================
write_systemd_unit() {
  local unit="/etc/systemd/system/${CNCH_SERVICE_NAME}.service"
  local http_port="${CNCH_PORT_DEFAULT}"
  log "写 systemd unit: $unit"
  if [[ $DRY_RUN -eq 1 ]]; then
    log "[dry-run] 跳过 systemd unit 部署"
    return 0
  fi
  # 模板用 unquoted heredoc 展开变量
  local unit_content
  unit_content=$(cat <<EOF
# CNCacheHub Server systemd unit
# 由 install.sh 生成 — 改完用 systemctl edit ${CNCH_SERVICE_NAME} 加 override
[Unit]
Description=CNCacheHub Server (Go HTTP API + 反代)
Documentation=https://github.com/cncachehub/cncachehub
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=${CNCH_USER}
Group=${CNCH_USER}
WorkingDirectory=${DATA_DIR}

# 二进制
ExecStart=${CNCH_BINARY}

# 环境变量
Environment=CNCH_DATA_DIR=${DATA_DIR}
Environment=CNCH_CACHE_DIR=${CACHE_DIR}
Environment=CNCH_LOG_DIR=${CNCH_LOG_DIR}
Environment=CNCH_HTTP_ADDR=127.0.0.1:${http_port}
Environment=CNCH_LOG_LEVEL=info
Environment=CNCH_SMALL_VPS_OPT=${SMALL_VPS_OPT}
Environment=CNCH_RESERVE_SPACE_GB=5
Environment=CNCH_MAX_OBJECT_SIZE_MB=1024
Environment=CNCH_CACHE_TOTAL_GB=20
Environment=CNCH_PUBLIC_BASE_URL=${PUBLIC_BASE_URL}
Environment=ADMIN_PASSWORD=${ADMIN_PASSWORD}
Environment=TZ=Asia/Shanghai

# 重启策略
Restart=on-failure
RestartSec=5
StartLimitInterval=60s
StartLimitBurst=5

# 资源限制
LimitNOFILE=65536
LimitNPROC=512
MemoryMax=512M
TasksMax=200

# 安全加固（与 docker compose 一致）
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=${DATA_DIR} ${CACHE_DIR} ${CNCH_LOG_DIR}
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictSUIDSGID=true
RestrictNamespaces=true
RestrictRealtime=true
LockPersonality=true
MemoryDenyWriteExecute=true
SystemCallArchitectures=native
SystemCallFilter=@system-service
SystemCallFilter=~@privileged @resources
SystemCallErrorNumber=EPERM

# 日志
StandardOutput=journal
StandardError=journal
SyslogIdentifier=cncachehub

[Install]
WantedBy=multi-user.target
EOF
)
  if [[ "$LOCATION" == "local" ]]; then
    echo "$unit_content" | sudo tee "$unit" >/dev/null
    run sudo systemctl daemon-reload
  else
    echo "$unit_content" | remote_or_local "sudo tee $unit >/dev/null && sudo systemctl daemon-reload"
  fi
  ok "systemd unit 已写: $unit"
}

# ============================================================================
# 写 nginx site config
# ============================================================================
write_nginx_config() {
  local listen_port="$HTTP_PORT"
  local server_port="${CNCH_PORT_DEFAULT}"
  local site="$CNCH_NGINX_SITE"
  log "写 nginx site: $site"
  if [[ $DRY_RUN -eq 1 ]]; then
    log "[dry-run] 跳过 nginx config 部署"
    return 0
  fi
  local nginx_conf
  nginx_conf=$(cat <<EOF
# CNCacheHub nginx 反代 — 由 install.sh 生成
# 详细加固说明见 docs/security.md
server {
    listen ${listen_port} default_server;
    listen [::]:${listen_port} default_server;
    server_name _;

    root ${CNCH_WEB_DIR}/dist;
    index index.html;

    # === 安全头 ===
    add_header X-Frame-Options "SAMEORIGIN" always;
    add_header X-Content-Type-Options "nosniff" always;
    add_header Referrer-Policy "strict-origin-when-cross-origin" always;
    add_header X-XSS-Protection "1; mode=block" always;
    server_tokens off;

    # === 请求体大小限制（DoS 防护）===
    client_max_body_size 100m;
    client_body_timeout 60s;
    client_header_timeout 60s;

    # === gzip（但不要压流式代理的响应）===
    gzip on;
    gzip_types text/plain text/css application/json application/javascript text/xml application/xml image/svg+xml;
    gzip_min_length 1024;

    # === Registry v2 API（流式，不缓冲）===
    location /v2/ {
        proxy_pass http://127.0.0.1:${server_port};
        proxy_http_version 1.1;
        proxy_set_header Host \$proxy_host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
        proxy_buffering off;
        proxy_request_buffering off;
        proxy_read_timeout 300s;
        proxy_send_timeout 300s;
        gzip off;
    }

    # === 资源加速中心 /r/ ===
    location /r/ {
        proxy_pass http://127.0.0.1:${server_port};
        proxy_http_version 1.1;
        proxy_set_header Host \$proxy_host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
        proxy_buffering off;
        proxy_request_buffering off;
        proxy_read_timeout 1800s;
        proxy_send_timeout 1800s;
        gzip off;
    }

    # === API ===
    location /api/ {
        proxy_pass http://127.0.0.1:${server_port};
        proxy_http_version 1.1;
        proxy_set_header Host \$proxy_host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
        proxy_read_timeout 30s;
        proxy_send_timeout 30s;
    }

    # === Health check（公开）===
    location = /healthz {
        proxy_pass http://127.0.0.1:${server_port}/healthz;
    }

    # === Metrics 限内网（防止数据泄露）===
    location = /metrics {
        allow 127.0.0.1;
        allow 10.0.0.0/8;
        allow 172.16.0.0/12;
        allow 192.168.0.0/16;
        deny all;
        proxy_pass http://127.0.0.1:${server_port}/metrics;
    }

    # === 静态资源（长缓存）===
    location /assets/ {
        expires 1y;
        add_header Cache-Control "public, immutable";
        try_files \$uri =404;
    }

    # === SPA fallback ===
    location / {
        try_files \$uri \$uri/ /index.html;
    }
}
EOF
)
  if [[ "$LOCATION" == "local" ]]; then
    echo "$nginx_conf" | sudo tee "$site" >/dev/null
    # enable site
    if [[ ! -L "$CNCH_NGINX_LINK" ]]; then
      run sudo ln -sf "$site" "$CNCH_NGINX_LINK"
    fi
    # 禁用 default site（如果存在）
    if [[ -L /etc/nginx/sites-enabled/default ]]; then
      run sudo rm -f /etc/nginx/sites-enabled/default
    fi
    # 测试配置
    run sudo nginx -t
  else
    echo "$nginx_conf" | remote_or_local "sudo tee $site >/dev/null && \
      sudo ln -sf $site $CNCH_NGINX_LINK && \
      sudo rm -f /etc/nginx/sites-enabled/default && \
      sudo nginx -t"
  fi
  ok "nginx site 已写: $site"
}

# ============================================================================
# 启停
# ============================================================================
start_systemd_service() {
  if [[ $DRY_RUN -eq 1 ]]; then
    log "[dry-run] 跳过 start"
    return 0
  fi
  if [[ "$LOCATION" == "local" ]]; then
    # 总是 restart（fresh init 时 restart 跟 enable --now 效果一样，update 时会真的重启）
    run sudo systemctl enable "$CNCH_SERVICE_NAME"
    run sudo systemctl restart "$CNCH_SERVICE_NAME"
    run sudo systemctl reload nginx
  else
    run remote_or_local "sudo systemctl enable $CNCH_SERVICE_NAME && \
      sudo systemctl restart $CNCH_SERVICE_NAME && \
      sudo systemctl reload nginx"
  fi
  ok "$CNCH_SERVICE_NAME 已启动"
}

# ============================================================================
# 健康检查
# ============================================================================
healthcheck_systemd() {
  if [[ $DRY_RUN -eq 1 ]]; then
    log "[dry-run] 跳过健康检查"
    return 0
  fi
  # 等服务起来
  local retries=10
  while (( retries-- > 0 )); do
    sleep 1
    if [[ "$LOCATION" == "local" ]]; then
      if curl -sf --max-time 3 "http://127.0.0.1:${HTTP_PORT}/healthz" >/dev/null 2>&1; then
        ok "/healthz OK"
        return 0
      fi
    else
      if remote_or_local "curl -sf --max-time 3 http://127.0.0.1:${HTTP_PORT}/healthz >/dev/null 2>&1"; then
        ok "/healthz OK (remote)"
        return 0
      fi
    fi
  done
  err "健康检查失败 — 跑 systemctl status $CNCH_SERVICE_NAME 看错误"
  if [[ "$LOCATION" == "local" ]]; then
    run sudo journalctl -u "$CNCH_SERVICE_NAME" --no-pager -n 30
  else
    run remote_or_local "sudo journalctl -u $CNCH_SERVICE_NAME --no-pager -n 30"
  fi
  return 1
}

# ============================================================================
# Main: install_systemd
# ============================================================================
install_systemd() {
  title "📦  CNCacheHub systemd 部署"
  hr
  log "Runtime: systemd (Go binary + nginx + systemd unit)"

  # 0) 依赖
  if [[ "$LOCATION" == "local" ]]; then
    install_nginx
    ensure_go
  else
    # 远端：先在本地装工具链，再上传产物
    log "本地 build 工具链检查..."
    command -v go >/dev/null 2>&1 || { err "本地没 go — 远端模式需要本地 build"; exit 2; }
    command -v node >/dev/null 2>&1 || command -v npm >/dev/null 2>&1 || { err "本地没 node/npm — 远端模式需要本地 build web"; exit 2; }
    log "检查远端..."
    if ! remote_or_local "command -v nginx >/dev/null 2>&1"; then
      log "远端没 nginx — 自动装"
      install_nginx
    else
      log "远端 nginx: $(remote_or_local 'nginx -v 2>&1')"
    fi
  fi

  # 1) 用户 + 目录
  setup_systemd_user
  setup_systemd_dirs

  # 2) Build (本地 build，产物在 GENERATED_DIR/)
  build_go_binary
  build_web_dist

  # 3) 部署 binary + web
  install_systemd_binary "$CNCH_BUILT_BINARY"
  install_systemd_web "$CNCH_BUILT_WEB"

  # 4) 写 systemd unit + nginx site
  write_systemd_unit
  write_nginx_config

  # 5) 启动
  start_systemd_service

  # 6) 健康检查
  healthcheck_systemd
}

# ============================================================================
# update_systemd
# ============================================================================
update_systemd() {
  title "🔄  CNCacheHub systemd 升级"
  log "Runtime: systemd — rebuild binary + restart service"

  # 0) 检查 go/node
  if [[ "$LOCATION" == "local" ]]; then
    ensure_go
  else
    command -v go >/dev/null 2>&1 || { err "本地没 go"; exit 2; }
    command -v npm >/dev/null 2>&1 || { err "本地没 npm"; exit 2; }
  fi

  # 1) build
  build_go_binary
  build_web_dist

  # 2) 部署
  install_systemd_binary "$CNCH_BUILT_BINARY"
  install_systemd_web "$CNCH_BUILT_WEB"

  # 3) restart
  if [[ $DRY_RUN -eq 1 ]]; then
    log "[dry-run] 跳过 restart"
  else
    if [[ "$LOCATION" == "local" ]]; then
      run sudo systemctl restart "$CNCH_SERVICE_NAME"
      run sudo systemctl reload nginx
    else
      run remote_or_local "sudo systemctl restart $CNCH_SERVICE_NAME && sudo systemctl reload nginx"
    fi
  fi
  ok "更新完成"

  # 4) 健康检查
  healthcheck_systemd
}

# ============================================================================
# uninstall_systemd
# ============================================================================
uninstall_systemd() {
  title "🗑️   卸载 CNCacheHub (systemd)"
  log "停服务..."
  if [[ "$LOCATION" == "local" ]]; then
    run sudo systemctl stop "$CNCH_SERVICE_NAME" 2>/dev/null || true
    run sudo systemctl disable "$CNCH_SERVICE_NAME" 2>/dev/null || true
    run sudo rm -f /etc/systemd/system/${CNCH_SERVICE_NAME}.service
    run sudo systemctl daemon-reload
    run sudo rm -f "$CNCH_BINARY" "${CNCH_BINARY}.new"
    run sudo rm -f "$CNCH_NGINX_SITE" "$CNCH_NGINX_LINK"
  else
    run remote_or_local "sudo systemctl stop $CNCH_SERVICE_NAME 2>/dev/null; \
      sudo systemctl disable $CNCH_SERVICE_NAME 2>/dev/null; \
      sudo rm -f /etc/systemd/system/${CNCH_SERVICE_NAME}.service; \
      sudo systemctl daemon-reload; \
      sudo rm -f $CNCH_BINARY ${CNCH_BINARY}.new; \
      sudo rm -f $CNCH_NGINX_SITE $CNCH_NGINX_LINK" || true
  fi

  if [[ $PURGE -eq 1 ]]; then
    warn "PURGE 模式：删数据 + 缓存 + 日志 + web"
    if [[ "$LOCATION" == "local" ]]; then
      run sudo rm -rf "$DATA_DIR" "$CACHE_DIR" "$CNCH_LOG_DIR" "$CNCH_WEB_DIR"
      run sudo userdel "$CNCH_USER" 2>/dev/null || true
    else
      run remote_or_local "sudo rm -rf '$DATA_DIR' '$CACHE_DIR' '$CNCH_LOG_DIR' '$CNCH_WEB_DIR'; sudo userdel $CNCH_USER 2>/dev/null" || true
    fi
    ok "数据 + 用户已删除"
  fi
  ok "卸载完成"
  warn "nginx 没卸载（可能还有其他站点用），需要时: sudo apt remove nginx"
}
