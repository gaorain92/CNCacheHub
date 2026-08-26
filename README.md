# CNCacheHub

> 自托管下载加速中枢 — Docker / SteamCMD / GitHub / Hugging Face / 测试浏览器 / 云原生工具，一套网关搞定。

[完整 PRD](./docs/prd.md) · [部署模式选型](./docs/deploy-modes.md) · [安全指南](./docs/security.md) · [AGENTS.md](./AGENTS.md)

---

## 这是什么

CNCacheHub 是一个面向国内网络环境的**自托管代理缓存网关**，把 Docker 镜像、SteamCMD 游戏服务端文件、GitHub Release、Hugging Face 模型、Playwright/Puppeteer 浏览器二进制、Terraform Provider、Helm Chart 这些「国内下载速度慢或不稳定」的高频资源，通过统一入口进行：

- **代理** — 透明转发，用户侧无需改业务代码
- **缓存** — 首次拉到本地，二次秒出
- **预热** — 主动批量下载，热数据提前就位
- **诊断** — 失败原因可观测，给出修复建议
- **配置生成** — 一键复制 `daemon.json` / `containerd` 配置 / SteamCMD 命令

**不做**：开放代理、TLS 中间人、平台授权破解、明文凭据保存。

---

## 仓库结构

```
.
├── server/      Go 后端（HTTP API + SQLite + 缓存代理）
├── web/         Vue 3 + Vite + Tailwind 控制台
├── agent/       预留：被加速机器上的轻量 agent
├── deploy/      生产部署（docker-compose + Caddy 反代）
├── scripts/     项目级脚本
├── docs/        PRD、架构文档
├── prototype/   HTML 高保真原型（设计参考）
└── AGENTS.md    Agent 工作规则
```

---

## 快速开始（开发模式）

> 前提：Go 1.22+、Node 20+、Docker（可选）

```bash
# 1. 启动后端
cd server
go mod download
go run ./cmd/cncachehub
# → http://localhost:8080

# 2. 启动前端（另一个终端）
cd web
npm install
npm run dev
# → http://localhost:5173

# 3. 用顶层脚本统一启动
./scripts/dev.sh
```

健康检查：

```bash
curl http://localhost:8080/api/healthz
# → {"status":"ok","version":"dev","uptime":"3s"}
```

---

## 快速开始（生产模式）

**两种部署模式** — 选一个：
- **Docker Compose**（默认）— 自包含，需要 docker
- **systemd + 二进制**（`--runtime=systemd`）— 不需要 docker，更轻

详见 [docs/deploy-modes.md](./docs/deploy-modes.md) 选型指南。

### 方式 1：一键部署脚本（推荐）

```bash
# === Docker 模式（默认）===
./scripts/install.sh
# 交互向导会问：Docker 还是 systemd（默认 docker）

# 极速
./scripts/install.sh --mode=express

# 专家
./scripts/install.sh --mode=expert \
  --admin-password=xxx \
  --http-port=80 \
  --tls-mode=letsencrypt \
  --domain=cnch.example.com

# === systemd 模式（不依赖 docker）===
./scripts/install.sh --runtime=systemd
# 或
./scripts/install.sh --runtime=systemd --mode=express

# === 远程部署（开发机推到服务器）===
./scripts/install.sh --mode=express --host=root@1.2.3.4 --ssh-key=~/.ssh/id_ed25519

# === 升级 / 卸载 ===
./scripts/install.sh update --mode=express
./scripts/install.sh uninstall --purge
# 卸载时记得加 --runtime 跟当初一致，否则默认卸载 docker 模式
./scripts/install.sh uninstall --runtime=systemd --purge
```

支持的参数完整列表：`./scripts/install.sh --help`

**特性**：
- 三种模式：`interactive` / `express` / `expert` — 从零对话到一键静默
- 两种位置：`local`（在目标机上跑）/ `remote`（在开发机通过 ssh 推）
- 两种 runtime：`docker` / `systemd` — 任选
- 三个子命令：`init` / `update` / `uninstall`（含 `--purge` 删数据）
- TLS 模式：HTTP only / 自签证书 / Let's Encrypt（自动签发）
- 启动后健康检查 `/healthz`，失败给 docker compose logs / journalctl 提示
- 生成配置写到 `deploy/generated/`（已 gitignore，敏感信息不进 git）
- 自动识别 5 大 Linux 发行版（apt/dnf/yum/pacman/apk/zypper），缺 docker 时自动装

### 方式 2：手动部署

```bash
# Docker 模式
cd deploy
cp .env.example .env
docker compose up -d

# systemd 模式 — 见 scripts/install-systemd.sh 内的 write_systemd_unit
# 和 write_nginx_config，照着写到 /etc/systemd/system 和 /etc/nginx/sites-available
```

详细部署与安全建议见 [docs/security.md](./docs/security.md)。

---

## 项目状态

当前版本 **v0.1.1**（2026-08-26）。已发布的所有功能：
- Docker Hub / GHCR / Quay / k8s.io / 自定义 registry pull-through 缓存
- SteamCMD AppID 预热 + 内置 mini DNS server
- 资源加速中心（GitHub / HuggingFace / Playwright / Terraform / 自定义）
- HF 镜像端点（`HF_ENDPOINT=http://host/hf` 兼容）
- 11 个业务页面（Dashboard / Docker / SteamCMD / Cache / Logs / Settings / Clients / Preheat / Resources / HuggingFace / Diagnostics）
- 鉴权 / RBAC（admin / 普通用户两级）+ 访问控制（Token + IP 白名单）
- 通知（webhook 部分实现）
- 诊断剧本 + bundle 导出
- 14 个包 66-100% 测试覆盖 + CI 4-job（go / web / docker / shellcheck）

路线图与未完成项见 [AGENTS.md 第 10 节](./AGENTS.md#10-当前状态--路线图)。

---

## 许可

TBD — 暂未确定开源协议。
