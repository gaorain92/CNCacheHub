# CNCacheHub

> 自托管下载加速中枢 — Docker / SteamCMD / GitHub / Hugging Face / 测试浏览器 / 云原生工具，一套网关搞定。

[完整 PRD](./docs/prd.md) · [高保真原型](./prototype/index.html) · [AGENTS.md](./AGENTS.md)

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

### 方式 1：一键部署脚本（推荐）

```bash
# 交互模式 — 逐步问关键参数
./scripts/install.sh

# 极速模式 — 用默认值 + 随机管理员密码
./scripts/install.sh --mode=express

# 专家模式 — 暴露所有参数
./scripts/install.sh --mode=expert \
  --admin-password=xxx \
  --http-port=80 \
  --tls-mode=letsencrypt \
  --domain=cnch.example.com \
  --admin-email=admin@example.com

# 远程部署（在开发机上跑，自动 ssh 到目标）
./scripts/install.sh --mode=express --host=root@1.2.3.4 --ssh-key=~/.ssh/id_ed25519

# 升级 / 卸载
./scripts/install.sh update --mode=express
./scripts/install.sh uninstall --purge
```

支持的参数完整列表：`./scripts/install.sh --help`

**特性**：
- 三种模式：`interactive` / `express` / `expert` — 从零对话到一键静默
- 两种位置：`local`（在目标机上跑）/ `remote`（在开发机通过 ssh 推）
- 三个子命令：`init` / `update` / `uninstall`（含 `--purge` 删数据）
- TLS 模式：HTTP only / 自签证书 / Let's Encrypt（自动签发）
- 启动后健康检查 `/healthz`，失败给 docker compose logs 提示
- 生成配置写到 `deploy/generated/`（已 gitignore，敏感信息不进 git）

### 方式 2：手动部署

```bash
cd deploy
cp .env.example .env
# 编辑 .env，至少设置 ADMIN_PASSWORD 和 PUBLIC_DOMAIN
docker compose up -d
# → https://your-domain.example.com
```

详细部署与安全建议见 [`docs/prd.md` 第 19 章](./docs/prd.md)。

---

## 项目状态

当前处于 **Phase 0：项目骨架**。MVP（Docker Hub pull-through cache + 控制台）见路线图。

详见 [AGENTS.md 第 10 节](./AGENTS.md#10-当前状态--路线图)。

---

## 许可

TBD — 暂未确定开源协议。
