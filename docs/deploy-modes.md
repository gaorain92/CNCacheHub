# CNCacheHub 部署模式选型

> CNCacheHub 支持两种部署方式：**Docker Compose** 和 **systemd + 二进制**。
> 选哪种取决于你的运维偏好、目标环境约束和管理粒度需求。

## TL;DR

| | **Docker Compose** | **systemd** |
|---|---|---|
| 一句话 | 容器化，自包含 | 原生服务，传统 Linux 部署 |
| 需要 docker | ✅ 必须 | ❌ 不需要 |
| 需要 Go 工具链 | ❌ | ✅（仅 build 时） |
| 需要 node/npm | ❌ | ✅（仅 build 时） |
| 镜像体积 | ~200MB（3 镜像） | ~10MB（二进制）+ dist |
| 内存占用 | server 512m + web 128m + caddy 256m | server 256M (cgroup) |
| 启动时间 | ~5-10s（拉镜像 + 健康检查） | <1s |
| 升级 | `install.sh update`（重建镜像） | `install.sh update`（重建 + 重启） |
| 日志 | `docker compose logs` | `journalctl -u cncachehub-server` |
| 隔离 | 容器隔离（cap_drop + read_only） | systemd 沙箱（ProtectSystem + NoNewPrivileges） |
| 适合场景 | 容器化平台，多服务，CI/CD | 裸 VPS，资源紧，传统运维习惯 |

---

## 选哪个？

### 选 Docker Compose 当…

- 你已经在用 docker，希望**自包含**（一个 `docker compose up` 起一切）
- 你的平台有 docker 但不方便装 nginx / Go（VPS 镜像没这些）
- 你想**多机部署**、**可复制**（同一份 docker-compose.yml 跨机器）
- 你想要 caddy / nginx 这些反代组件**打包在 compose 里**
- 你愿意接受 docker daemon 的资源开销（~50MB 常驻）

### 选 systemd 当…

- 你的 VPS **没装 docker**，也不想装（许多 128MB 小鸡装 docker 反而更费）
- 你的发行版没 docker（如 openSUSE、CentOS 7）
- 你习惯 **systemctl + journalctl** 的运维模式
- 你想要**更轻的占用**（不要 docker daemon）
- 你的 CI/CD / 部署脚本已经走 SSH + scp

### 两个都支持的代价

- 我们的镜像同时维护 `server/Dockerfile` 和 `server/cmd/cncachehub/main.go`（同一份 Go 代码）
- web 前端构建产物对两边一样（`npm run build`）
- `install.sh` 同一个入口，CLI 加 `--runtime=docker|systemd` 切换
- 反代配置两边**结构不同**（Caddyfile vs nginx site），都各自有加固版

---

## Docker Compose 模式详解

### 架构

```
┌─────────────────────────────────────────────┐
│              Caddy (反代 :80)                │
│  /api/*  /healthz  /metrics  /              │
└──┬─────────────────┬──────────────────────┬──┘
   │ /api/*          │ /                    │
   ▼                 ▼                      ▼
┌──────────┐  ┌──────────┐         (Caddy 容器内置)
│ server   │  │   web    │
│  :8080   │  │   :80    │
│  (Go)    │  │  (nginx) │
└──────────┘  └──────────┘
       │         │
       ▼         ▼
   cncachehub_data   (named volume)
   cncachehub_cache  (named volume)
```

### 一键部署

```bash
# 1. 准备（开发机）
git clone <repo>
cd cncachehub

# 2. 跑安装向导
./scripts/install.sh
# 选 docker → 选 interactive → 一路回车

# 3. 验证
curl http://your-server/healthz
```

### 升级

```bash
# 在项目根目录
./scripts/install.sh update --mode=express
```

### 卸载

```bash
./scripts/install.sh uninstall              # 保留数据
./scripts/install.sh uninstall --purge     # 全删
```

### 加固项（详见 `deploy/docker-compose.yml`）

- `cap_drop: ALL` + 最小 `cap_add`
- `security_opt: no-new-privileges: true`
- `read_only: true` + `tmpfs` 给可写路径
- `mem_limit` / `memswap_limit` / `pids_limit`
- `healthcheck` 改用 `127.0.0.1`（避免 alpine `::1` 优先问题）
- 默认 bridge 网络（隔离 host 网络）

### Caddy 加固（详见 `deploy/Caddyfile`）

- `request_body` 限 100MB（防 DoS）
- `/metrics` 限内网 IP（防数据泄露）
- 严格响应头：X-Frame / X-Content-Type / CSP / -Server
- 限流（防登录爆破）由 Go server 中间件负责（caddy:2-alpine 没带 rate_limit 模块）

---

## systemd 模式详解

### 架构

```
┌──────────────────────────────────────────┐
│          nginx (反代 :80)                 │
│  /v2/  /r/  /api/  /healthz  /metrics    │
│  /assets/  /  → SPA fallback             │
└────────────┬─────────────────────────────┘
             │
             ▼
┌──────────────────────────────────────────┐
│  systemd: cncachehub-server              │
│  ├ User=cncachehub (无登录系统用户)      │
│  ├ NoNewPrivileges + ProtectSystem=strict│
│  ├ ReadWritePaths 仅数据/缓存/日志        │
│  └ ExecStart=/usr/local/bin/cncachehub   │
│           :8082 (内部)                   │
└─────┬──────────────────┬─────────────┬───┘
      │                  │             │
      ▼                  ▼             ▼
  /var/lib/        /var/lib/      /var/log/
  cncachehub/      cncachehub/    cncachehub/
    data/            cache/
```

### 一键部署

```bash
# 1. 准备（开发机）— 必须有 Go + node
git clone <repo>
cd cncachehub
go version  # ≥ 1.22
node --version  # ≥ 18

# 2. 跑安装向导
./scripts/install.sh --runtime=systemd
# 选 interactive → 一路回车

# 3. 验证
curl http://your-server/healthz
```

### 远程部署（开发机 → 服务器）

```bash
# 在开发机上（macOS 也能跑）
./scripts/install.sh --runtime=systemd \
  --location=remote \
  --host=root@1.2.3.4 \
  --ssh-key=~/.ssh/id_rsa \
  --http-port=80
# 二进制和 web dist 在**本地** build，pipe 上传到服务器
# 服务器不需要 Go / node（除非你用 --location=local 远端 build）
```

### 升级

```bash
# 重建二进制 + web dist + 重启服务
./scripts/install.sh update --runtime=systemd --mode=express

# 看日志
sudo journalctl -u cncachehub-server -f

# 重启单个服务
sudo systemctl restart cncachehub-server
```

### 卸载

```bash
./scripts/install.sh uninstall --runtime=systemd              # 保留数据
./scripts/install.sh uninstall --runtime=systemd --purge      # 全删 + 删用户
```

### 加固项（详见 `scripts/install-systemd.sh`）

#### systemd unit

- `User=cncachehub` / `Group=cncachehub`（无登录系统用户）
- `NoNewPrivileges=true`
- `PrivateTmp=true`
- `ProtectSystem=strict` + `ReadWritePaths=` 数据/缓存/日志
- `ProtectHome=true`
- `ProtectKernelTunables=true` / `ProtectKernelModules=true` / `ProtectControlGroups=true`
- `RestrictSUIDSGID=true` / `RestrictNamespaces=true` / `RestrictRealtime=true`
- `LockPersonality=true` / `MemoryDenyWriteExecute=true`
- `SystemCallArchitectures=native` + `SystemCallFilter=@system-service`
- `MemoryMax=512M` / `TasksMax=200` / `LimitNOFILE=65536`
- `StartLimitBurst=5` / `Restart=on-failure` / `RestartSec=5`

#### nginx

- `client_max_body_size 100m`（DoS 防护）
- `/metrics` 用 `allow`/`deny` 限内网
- 严格安全头（X-Frame / X-Content-Type / Referrer-Policy / XSS）
- `server_tokens off`
- 流式响应（`/v2/` `/r/`）关闭 `proxy_buffering`

### 端口

- **80** — 公网（nginx）
- **8082** — 内部（Go server，只 listen 127.0.0.1）
- 22 — SSH（你自己管理）

---

## 关键决策记录

### 为什么不两种模式都打包 docker？

许多小 VPS 装 docker 后资源被吃紧（一开 docker 就 200MB+），systemd 模式
是给**不想用 docker** 的用户的合理选择。两种模式都支持 = 覆盖更多部署场景。

### 为什么 systemd 模式要 build 在本地？

- 服务器装 Go 工具链太重（500MB+），小 VPS 不合适
- Web 前端 `npm ci` 在服务器上跑也吃资源
- 远程模式：本地 build，pipe 上传（防 scp 在 1GB RAM 测机损坏）

### 为什么 systemd 模式的 Go server 端口是 8082 而 docker 是 8080？

为了**两种模式可以同时跑在同一台机器上**（开发调试用），端口不冲突。
实际部署选一种就行。

### Caddy vs nginx？

- **Caddy**：自动 HTTPS，配置极简，但 caddy:2-alpine 不带 `rate_limit` 模块
- **nginx**：生态成熟，可用 `allow`/`deny` 做 IP 白名单，配置稍复杂

systemd 模式选 nginx 是因为能用 `allow/deny` 限 `/metrics` 的 IP，
而 caddy 的 remote_ip matcher 在 2-alpine 镜像里工作不稳定。

---

## 互操作性

如果你想从一种模式**迁移到另一种**：

### docker → systemd

```bash
# 1. 备份数据
docker compose -f deploy/generated/docker-compose.yml stop server
docker run --rm -v cncachehub_cncachehub_data:/data -v $(pwd):/backup \
  alpine cp /data/cncachehub.db /backup/

# 2. 停 docker
./scripts/install.sh uninstall --runtime=docker

# 3. 起 systemd
./scripts/install.sh init --runtime=systemd
# 把 db 拷回去
sudo cp cncachehub.db /var/lib/cncachehub/data/
sudo systemctl restart cncachehub-server
```

### systemd → docker

类似地，先备份 db，停 systemd，起 docker，拷 db 回来。

### 注意

- 密码存在 DB 里，迁移时**用户 / 密码不变**
- 缓存文件（`/var/lib/cncachehub/cache/`）可以丢，重建后下次下载会重新拉
- 配置（`deploy/generated/.env` vs systemd Environment）需要手动对应
