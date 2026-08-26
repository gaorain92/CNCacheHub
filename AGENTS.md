# CNCacheHub — AGENTS.md

> 这是 **项目级 agent 规则文件**。所有 AI agent（包括 sub-agent、Cursor / Codex / Claude Code / Aider / Devin / Gemini CLI 等）在本项目下工作前必须先读这个文件。
> 完整产品需求见 `docs/prd.md`（v1.0，简体中文）。

---

## 1. 一句话定位

**CNCacheHub**：面向国内开发者、运维团队、游戏服主的自托管下载加速中枢。核心是 Docker/OCI 镜像代理缓存，扩展支持 SteamCMD 缓存、常见开发资源（GitHub Release、Hugging Face、Playwright/Puppeteer、Terraform/Helm 等）下载加速。

- **首版 MVP**：Docker Hub pull-through cache + 缓存可视化 + 健康检查 + 配置生成。
- **次版迭代**：多 Registry、SteamCMD 缓存、资源加速中心、诊断中心、定时清理、小容量 VPS 优化。
- **v0.1.0（2026-08-04）**：P0 + P1 + P2 4/5 全部完成，剩多节点 / 高可用（明确不做）。
- **v0.1.1（2026-08-26，当前）**：HuggingFace 镜像 + nezha 风格一键安装 + security audit
  （clientip / body limit / 代理头过滤 / LIKE escape / path traversal / 安全头）+ CI 修复
  （go vet / shellcheck severity / docker build 路径 / DNS race / linux Chtimes）+ web copy 按钮 HTTP fallback。

---

## 2. 仓库结构（monorepo）

```
.
├── AGENTS.md              # 本文件 — 所有 agent 必读
├── README.md              # 项目说明 + 快速开始
├── Makefile               # 顶层：dev / build / test / release / release-upload / release-verify / install
├── docker-compose.yml     # 顶层编排（聚合 server / web，本地 dev 用）
├── .github/
│   └── workflows/ci.yml   # GitHub Actions：go test + web build + docker build + shellcheck
├── docs/
│   ├── prd.md                  # 完整产品需求文档
│   ├── security.md             # 安全部署指南（13 章）
│   ├── deploy-modes.md         # docker vs systemd 选型
│   └── release-process.md      # 发布流程 + tarball 结构
├── server/                # Go 后端
│   ├── cmd/cncachehub/    # main 入口
│   ├── internal/          # 业务代码（api / config / storage / cache / proxy / log / auth / dns / preheat / diagnostics / access / metrics / ratelimit / crypto）
│   ├── migrations/        # SQL 迁移（按编号顺序）
│   ├── go.mod
│   ├── Makefile
│   └── Dockerfile
├── web/                   # Vue 3 前端
│   ├── src/
│   │   ├── views/         # 页面（Dashboard / Docker / SteamCMD / Cache / Diagnostics / Settings / Logs / Clients / Preheat / Resources）
│   │   ├── components/    # 公共组件
│   │   ├── stores/        # Pinia 状态
│   │   ├── api/           # 后端 API 客户端
│   │   ├── router/
│   │   ├── i18n/          # zh-CN + en 翻译
│   │   └── styles/
│   ├── vite.config.ts
│   ├── tailwind.config.js
│   ├── package.json
│   └── Dockerfile
├── agent/                 # 预留：未来在每台被加速的机器上跑的轻量 agent
├── deploy/                # 部署脚本 + 生产 docker-compose + Caddy 反代配置（hardened）
├── scripts/
│   ├── install.sh         # 主安装脚本（init / update / uninstall）— 3 模式 × 3 runtime × 3 source × 2 location
│   ├── install-online.sh  # 一键安装包装（curl | bash 友好，参照 nezha.sh 风格）
│   └── install-systemd.sh # systemd 模式专用的 binary / web / unit / nginx 子脚本
└── prototype/             # 高保真 HTML 原型（不是产品代码，仅供设计参考）
```

---

## 3. 技术栈

| 层 | 选型 | 理由 |
|---|---|---|
| 后端 | Go 1.22+ | 单二进制、跨平台、低资源占用、反代性能强 |
| HTTP 框架 | `chi`（已选） | 轻量、middleware 灵活 |
| 数据库 | SQLite（`modernc.org/sqlite` 纯 Go 驱动） | 单 VPS 部署免外部依赖，备份就是拷贝文件 |
| 缓存元数据 | SQLite | 不引入 Redis |
| Blob 存储 | 本地文件系统（`/var/lib/cncachehub/cache/`） | 简单、可靠、可直接 rsync 备份 |
| 前端 | Vue 3 + Vite + TypeScript | 用户已有的偏好栈 |
| UI 库 | Tailwind CSS + Element Plus | 极客暗黑风 + 表格 / 表单组件 |
| 状态 | Pinia | Vue 3 官方推荐 |
| 路由 | Vue Router 4 | 标准 |
| HTTP 客户端 | `axios` + 拦截器 | 统一处理 token / error |
| 部署 | Docker Compose 或 systemd + nginx | 两种 runtime 都支持（见 §4.6） |
| 监控 | 标准 `/healthz`、`/metrics`（Prometheus） | 不引入额外依赖 |

---

## 4. 开发流程

### 4.1 本地开发

```bash
# 启动后端
cd server && go run ./cmd/cncachehub

# 启动前端（另一个终端）
cd web && npm install && npm run dev

# 或者用顶层 Makefile
make dev-server   # 后端
make dev-web      # 前端
```

### 4.2 测试

- **后端**：`go test ./...`（13 个包，~80 个测试函数）
- **前端**：`npm run type-check`（vue-tsc 严格模式）
- **覆盖率**：`go test -cover ./...` — 所有包 ≥ 60%，9 个包 ≥ 70%（ratelimit 100 / access 94.7 / metrics 93.9 / dns 92.8 / config 85.2 / preheat 84.6 / crypto 77.8 / proxy 77.6 / cache 77.3 / diagnostics 73.6 / storage 70.2 / log 69.2 / api 64.5）
- **集成测试**：`make test`（顶层 Makefile target，会跑 server + web）
- **CI**：`.github/workflows/ci.yml` 跑 4 个 job：go test + race + cover，web type-check + build，docker build，shellcheck。触发 push main / PR。

### 4.3 Commit 规范

按用户偏好：

```
<scope>: <中文一句话>

正文（可选）：详细解释为什么、改了什么、有什么副作用。
```

- `<scope>` 必须是英文，与目录名一致：`server` / `web` / `agent` / `deploy` / `docs` / `scripts` / `ci`
- 例：
  ```
  server: 增加 SQLite 迁移脚本与 storage 封装
  web: 搭建 Vue 3 + Vite + Tailwind 工程脚手架
  ci: 加 GitHub Actions 工作流
  ```
- 不允许 skip CI（commit message 也不要写 `[skip ci]`）
- squash 优先 — 一个 feature 一个 commit

### 4.4 发布 / 推送策略

```bash
# 1. 打制品（Linux amd64）
make release VERSION=v0.1.0

# 2. 验证制品结构
make release-verify VERSION=v0.1.0

# 3. 上传 GitHub Releases（自动从上一个 tag 摘 changelog 当 release notes）
make release-upload VERSION=v0.1.0
# 或自带 notes:
make release-upload VERSION=v0.1.0 NOTES_FILE=release-notes.md
# 或指定 repo（默认从 git remote origin 推断）:
make release-upload VERSION=v0.1.0 REPO=myorg/cncachehub
```

依赖：
- `gh` CLI（`brew install gh`）
- `gh auth status` 登录
- 默认推到 git remote origin（自动从 `git remote get-url origin` 推断 `owner/repo`）

如果用户没登录 gh：先 `gh auth login`。

### 4.5 部署模式

CNCacheHub 支持两种部署模式（互斥，二选一）：

| | docker (默认) | systemd |
|---|---|---|
| 适用 | 已有 docker 主机 / 多容器编排 | 1GB RAM 单 VPS，资源紧 |
| 进程隔离 | docker container（cap_drop + read_only） | systemd unit（ProtectSystem + ReadWritePaths） |
| 反代 | Caddy（自动 HTTPS） | nginx（systemd 装） |
| 安装 | `install.sh init --runtime=docker` | `install.sh init --runtime=systemd` |
| 升级 | `install.sh update` | `install.sh update` |
| 数据目录 | `/var/lib/cncachehub/data` | `/var/lib/cncachehub/data` |
| 一键 | `curl ... \| sudo bash`（见 install-online.sh） | `curl ... \| sudo bash`（推荐 systemd） |

详细对比见 `docs/deploy-modes.md`。

### 4.5.1 一键安装（curl | bash）

最简部署方式，参照 nezha.sh 风格：

```bash
# 国际
curl -L https://raw.githubusercontent.com/cncachehub/cncachehub/main/scripts/install-online.sh | sudo bash

# 国内（gitee 镜像）
curl -L https://gitee.com/cncachehub/cncachehub/raw/main/scripts/install-online.sh | sudo CN=true bash
```

`install-online.sh` 会自动：
- 检测 arch / init system / CN
- 拉 `install.sh` + `install-systemd.sh` 到 tmp
- 调 `install.sh init --mode=express --runtime=auto` 做实际部署
- 健康检查后调 `POST /api/auth/init` 创建 admin
- 打印 access URL + 凭据

环境变量（覆盖默认）：`CNCH_ADMIN_USER` / `CNCH_ADMIN_PASSWORD` / `CNCH_HTTP_PORT` / `CNCH_DATA_DIR` / `CNCH_RUNTIME=systemd|docker` / `CN=true`。

### 4.6 手动部署到测试机（不走 install.sh）

适用于已有 systemd 服务、要快速热更新 server binary 的场景（保留数据和配置）：

```bash
# 1. 在工作区打 tar（不含 .git / node_modules / dist / bin）
cd /path/to/cncachehub
tar --exclude='.git' -czf /tmp/cnch-server.tar.gz server/

# 2. 推到测试机（用 pipe 而非 scp — 1GB 测试机 scp 不稳定）
cat /tmp/cnch-server.tar.gz | sshpass -f ~/.ssh/.cnch_pw ssh root@<ip> \
  'cat > /tmp/cnch-server.tar.gz && \
   cd /opt/cncachehub && \
   find . -maxdepth 2 -name "server" -type d -exec rm -rf {} + && \
   tar -xzf /tmp/cnch-server.tar.gz && \
   find server -name "._*" -delete'  # 清理 macOS tar 残留

# 3. 在测试机重 build + 重启
ssh root@<ip> 'cd /opt/cncachehub/server && \
  CGO_ENABLED=0 go build -trimpath \
    -ldflags="-s -w -X main.version=v0.1.1 -X main.commit=local" \
    -o /usr/local/bin/cncachehub ./cmd/cncachehub && \
  cp /usr/local/bin/cncachehub /usr/local/bin/cncachehub.bak.$(date +%Y%m%d-%H%M%S) && \
  systemctl restart cncachehub-server'

# 4. 验证
curl http://<ip>/api/healthz
curl http://<ip>/api/version
```

注意：web dist 不需重 build（除非 web/src 有改动），nginx 直接 serve `/opt/cncachehub/web/dist/`。

---

## 5. 命名与代码风格

### 5.1 Go

- 包名小写、单词、单数（`api` / `storage` / `cache` / `proxy` / `preheat` / `dns`）
- 公开 API 必须有 godoc 注释
- error 优先返回，不要 panic；panic 仅在不可恢复的初始化错误
- 自写 `recovererMiddleware()`（server/internal/api/recoverer.go）记录完整 stack + panic value，**不要**换回 `chimw.Recoverer`
- 配置通过 `internal/config` 加载，**不要**在业务代码里直接 `os.Getenv`
- 上下文优先使用 `context.Context`，跨 goroutine 必须传递
- DB 写操作**不要**用可取消的 ctx（会被 cancel 阻断状态落地），用独立的 statusCtx / writeCtx

### 5.2 TypeScript / Vue

- 组件用 `<script setup lang="ts">`
- API 客户端统一在 `src/api/`，**不要**在组件里直接 `axios`
- 状态用 Pinia store，**不要**用全局变量
- 路由懒加载（`() => import(...)`）
- 类型定义集中放 `src/types/`
- i18n：默认 zh-CN，en 翻译用 `src/i18n/`，运行时 vue-i18n
- health store 不能在 `onMounted` 里 fetch（router guard 还没完）— 用 `watch(() => auth.isAuthenticated, ...)`

### 5.3 数据库

- 表名复数下划线（`users` / `cache_entries` / `preheat_tasks` / `cleanup_tasks` / `resource_rules` / `resource_cache_entries`）
- 字段名小写下划线（`created_at` / `expires_at` / `last_access_at`）
- 主键统一 `id INTEGER PRIMARY KEY AUTOINCREMENT`
- 时间戳统一 INTEGER（Unix 秒）
- 敏感字段查询（pass 哈希、token）走 `storage` 包，**不要**在 handler 里直接拼 SQL

### 5.4 API

- RESTful 风格
- JSON 字段用 camelCase
- 错误响应统一 `{ "error": { "code": "string", "message": "string", "details": {} } }`
- 列表接口统一 `{ "items": [...], "total": N, "page": N, "pageSize": N }`
- 所有路径前缀 `/api/...`
- 鉴权 cookie（`X-Request-Id` 也走 ctx）

---

## 6. 安全约束（不可妥协）

以下边界由 PRD 明确，**任何 agent 不得越过**：

1. **白名单优先**：资源加速中心必须基于显式白名单规则（repo / 模型名 / 域名 / 版本规则），**不**做"任意 URL 开放代理"。
2. **不绕过平台授权**：不破解 Steam、GitHub、Hugging Face 等平台的账号或 token，不保存用户明文密码。
3. **敏感 URL 不缓存**：带 `?token=` `?signature=` `?session=` `?auth=` `?secret=` `?password=` 的 URL 默认不缓存（`hasSensitiveQuery` 大小写不敏感！），日志必须脱敏。
4. **小容量 VPS 安全**：单对象落盘大小限制（默认 1GB）、缓存总上限、系统保底空间（默认 5GB）、只读旁路必须生效。**不**因为缓存失败而中断用户下载请求（必须支持旁路转发）。
5. **HTTPS 证书**：默认只反代 HTTPS 上游，不做 TLS 中间人解密；自签证书场景通过可信 CA 列表配置。`httpClientWithInsecure` 仅供诊断剧本使用。
6. **认证与授权**：控制台 API 必须鉴权（Cookie + CSRF）；公开代理入口必须可独立配置访问控制；登录 5 次/分钟限流（`internal/ratelimit`）。
7. **日志脱敏**：所有日志中的 token / cookie / 密码必须脱敏（`***`）。诊断 bundle 不含 admin password。
8. **容器/进程加固**（任选其一即可，**不可同时缺**）：
   - docker: `cap_drop: [ALL]` + 必要 `cap_add` + `read_only: true` + `no-new-privileges: true` + mem/pids limit
   - systemd: `NoNewPrivileges=true` + `ProtectSystem=strict` + `ReadWritePaths=` + `MemoryDenyWriteExecute=true` + `SystemCallFilter=@system-service` + `MemoryMax=512M`
9. **安全头**（nginx / Caddy 必加）：`X-Frame-Options`、`X-Content-Type-Options`、`Referrer-Policy`、CSP、`server_tokens off`。
   server-side middleware 也会加 `X-Content-Type-Options: nosniff` / `X-Frame-Options: DENY` /
   `Referrer-Policy: no-referrer` 作为双保险（直连 :8082 时也生效）。
10. **/metrics 限制**：只允许 127.0.0.1 + RFC1918 段访问（nginx allowlist），其他 IP 返 404
11. **客户端 IP 提取**：用 `internal/clientip.Real(r)` — 只信任 trusted proxy CIDR（默认
    loopback + RFC1918 + link-local + IPv6 ULA）设置的 `X-Forwarded-For` / `X-Real-IP`。
    **不要用 `chimw.RealIP` 或 `r.RemoteAddr` 直接判断** — 直连 :8082 的攻击者可以伪造
    `X-Forwarded-For` 绕过 IP 白名单 / rate limit。trusted 列表可用 `CNCH_TRUSTED_PROXIES`
    env var（逗号分隔 CIDR）覆盖。详见 `server/internal/clientip/`。
12. **上游代理头过滤**：`copyRequestHeaders` 跳过所有 `X-Forwarded-*` / `X-Real-IP` /
    `Forwarded` / `CF-Connecting-IP` / `True-Client-IP` / `Fastly-Client-IP` / `X-Client-IP` /
    `X-Original-Forwarded-For` 等。**不要在新增的 upstream 调用里直接透传客户端 headers** —
    不然攻击者可以通过 CNCacheHub 伪造 IP 给上游 registry / HF / GitHub。
13. **JSON body 限制**：所有 API 走 `decodeJSONBody(w, r, &req)`（1 MiB 上限 +
    `DisallowUnknownFields` + 拒绝 multi-document）。不要在新增 handler 里直接
    `json.NewDecoder(r.Body).Decode(...)` — 没有大小限制会被 DoS。

---

## 7. 性能约束

- 控制台 API P95 响应时间 ≤ 300ms
- 大文件下载流式转发（`safeMultiWriter` tee 到 client + cache），不一次性读入内存
- 小容量 VPS 优化下：单进程常驻内存目标 ≤ 256MB（实际 9-45MB），空闲 CPU 接近 0
- 缓存写入支持断点续传 + 原子替换（写到 tmp，再 rename）
- preheat 用独立 ctx 写 DB（不被用户 cancel 阻断），单测覆盖 84.6%

---

## 8. 不要做的事

明确禁止以下行为，避免 agent 跑偏：

- ❌ 不要引入 docker swarm / k8s / 微服务框架 — 这是一个单 VPS 自托管项目
- ❌ 不要引入 Redis / PostgreSQL / MySQL / 消息队列 — SQLite + 文件系统够用
- ❌ 不要做"通用 HTTP 代理" — 必须是白名单范围内的资源加速
- ❌ 不要做 TLS 中间人解密
- ❌ 不要保存用户明文密码 / token / cookie（除了 bcrypt 哈希后的用户密码）
- ❌ 不要做账号系统 / 社交登录 / OAuth
- ❌ 不要做付费功能 / 订阅 / 商业化
- ❌ 不要在前端硬编码假数据当真实数据用（演示数据放 `prototype/` 或加 `__demo` 前缀）
- ❌ 不要让 sub-agent 跨目录改不属于自己的代码（server agent 只能改 server/）
- ❌ 不要生成跟 PRD 冲突的方案
- ❌ 不要用 `systemctl enable --now`（不会重启已运行服务）— 用 `systemctl restart`
- ❌ 不要用 `$()` 捕获 log/ok 的输出（会污染返回值）— 用全局变量
- ❌ 不要在 heredoc 用单引号包变量（不会展开）— 用双引号
- ❌ 不要在 commit 写 `[skip ci]`
- ❌ 不要在 `git push` 时 force-push main
- ❌ 不要用 `chimw.RealIP`（会无条件信任 X-Forwarded-For，废掉 clientip.Real 的 trusted proxy 检查）
- ❌ 不要在 handler 里直接 `json.NewDecoder(r.Body).Decode(...)`（没 body 大小限制）— 用 `decodeJSONBody`
- ❌ 不要在新增的 upstream 请求里透传 `X-Forwarded-*` 等代理头（用户可伪造 IP 给上游）

---

## 9. Sub-agent 工作准则

当 root agent 派 sub-agent 时，prompt 必须包含：

1. **明确的范围**（改哪些目录、做什么交付件）
2. **明确的不做**（防止越界）
3. **验收标准**（如何判断完成了）
4. **依赖文件**（PRD 哪些章节、AGENTS.md 哪些条目）
5. **返回格式**（要求 sub-agent 返回变更清单 + 测试结果）

sub-agent 返回时必须：

- 列出新增 / 修改 / 删除的文件
- 列出运行的测试命令与结果（含 `go test -count=1 ./...`）
- 列出已知未完成项（必须诚实，不准隐瞒）
- 不返回无关对话内容

---

## 10. 当前状态 / 路线图

> 最近一次更新：2026-08-26（v0.1.1 发布 — HuggingFace 镜像 + security audit + CI 修复 + copy 按钮）

### Phase 0 — 项目骨架 ✅

- [x] 完整 PRD（`docs/prd.md`）
- [x] 高保真 HTML 原型（`prototype/index.html`）
- [x] Monorepo 目录 + AGENTS.md
- [x] Go server 骨架（HTTP + SQLite + config + health API）
- [x] Vue 3 web 骨架（含 i18n）
- [x] Docker Compose + systemd 部署脚本
- [x] CI（GitHub Actions 4 job：go test / web build / docker build / shellcheck）

### Phase 1 — Docker 加速 MVP ✅

- [x] Registry pull-through cache 代理（dockerhub / ghcr / quay / k8s）
- [x] Docker daemon.json 配置生成
- [x] 缓存条目元数据
- [x] 基础请求日志（access_log）
- [x] 总览仪表盘数据（dashboard summary）
- [x] Token dance（401 + Www-Authenticate → 自动拿 token 重试）

### Phase 2 — 扩展模块 ✅ 4/5

- [x] SteamCMD 缓存（preheat kind=steam）
- [x] 多 Registry（GHCR / Quay / k8s / 自定义）
- [x] 资源加速中心（GitHub / HF / Playwright / Terraform / 自定义）
- [x] 诊断中心（playbook + bundle handler）
- [x] 定时清理任务
- [x] 小容量 VPS 优化开关（CNCH_SMALL_VPS_OPT）
- [ ] 多节点 / 高可用（明确**不做**）

### Phase 3 — 完善

- [x] 鉴权 / RBAC（admin / 普通用户两级 — `IsAdmin` bool）
- [x] 通知（webhook 部分实现，邮件未做）
- [x] 自定义 panic recoverer（runtime.Stack + debug.Stack）
- [x] 健康检查 playbook（Docker pull / Steam DNS / 反代 / 5xx 错误率）
- [x] 测试覆盖 backfill（**14 个包** 0% → 66-100%，含 clientip 新包）
- [x] 真实 GitHub Release（v0.1.0 / v0.1.1）
- [ ] Helm Chart 部署

### 当前测试覆盖（`make test`，截至 v0.1.1）

| 包 | 覆盖率 |
|---|---|
| ratelimit | 100.0% |
| access | 95.3% |
| metrics | 93.9% |
| dns | 92.8% |
| clientip | 88.9% |
| config | 85.9% |
| preheat | 85.5% |
| crypto | 77.8% |
| cache | 77.3% |
| diagnostics | 73.6% |
| proxy | 71.3% |
| storage | 70.4% |
| log | 69.2% |
| api | 66.5% |

### 测试机部署状态

- **测试机**：用户的开发 VPS（IP 见用户本地 ~/.ssh/config `gaorain-64_27_6_157` 别名；1GB RAM / Debian 12）
- **runtime**：systemd（cncachehub-server.service）
- **当前 binary**：`/usr/local/bin/cncachehub` v0.1.1（tag 详见 git tag -l）
- **启动时间**：每次手动热更新会重启
- **登录凭据**：见用户本地密码文件（`~/.ssh/.cnch_pw` 等），**不要写进本项目任何文件**
- **数据目录**：`/var/lib/cncachehub/data/cncachehub.db`
- **web dist**：`/opt/cncachehub/web/dist/`（nginx serve）
- **nginx**：80 → 127.0.0.1:8082，含安全头 + /metrics 限制
