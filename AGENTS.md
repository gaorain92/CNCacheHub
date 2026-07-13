# CNCacheHub — AGENTS.md

> 这是 **项目级 agent 规则文件**。所有 AI agent（包括 sub-agent、Cursor / Codex / Claude Code / Aider / Devin / Gemini CLI 等）在本项目下工作前必须先读这个文件。
> 完整产品需求见 `docs/prd.md`（v1.0，简体中文）。

---

## 1. 一句话定位

**CNCacheHub**：面向国内开发者、运维团队、游戏服主的自托管下载加速中枢。核心是 Docker/OCI 镜像代理缓存，扩展支持 SteamCMD 缓存、常见开发资源（GitHub Release、Hugging Face、Playwright/Puppeteer、Terraform/Helm 等）下载加速。

- **首版 MVP**：Docker Hub pull-through cache + 缓存可视化 + 健康检查 + 配置生成。
- **次版迭代**：多 Registry、SteamCMD 缓存、资源加速中心、诊断中心、定时清理、小容量 VPS 优化。

---

## 2. 仓库结构（monorepo）

```
.
├── AGENTS.md              # 本文件 — 所有 agent 必读
├── README.md              # 项目说明 + 快速开始
├── docker-compose.yml     # 顶层编排（聚合 server / web）
├── docs/
│   ├── prd.md             # 完整产品需求文档
│   └── architecture.md    # 技术架构（待写）
├── server/                # Go 后端
│   ├── cmd/cncachehub/    # main 入口
│   ├── internal/          # 业务代码（api / config / storage / cache / proxy / log / auth）
│   ├── migrations/        # SQL 迁移
│   ├── go.mod
│   ├── Makefile
│   └── Dockerfile
├── web/                   # Vue 3 前端
│   ├── src/
│   │   ├── views/         # 页面（Dashboard / Docker / SteamCMD / Cache / Diagnostics / Settings / Logs / Clients / Preheat）
│   │   ├── components/    # 公共组件
│   │   ├── stores/        # Pinia 状态
│   │   ├── api/           # 后端 API 客户端
│   │   ├── router/
│   │   └── styles/
│   ├── vite.config.ts
│   ├── tailwind.config.js
│   ├── package.json
│   └── Dockerfile
├── agent/                 # 预留：未来在每台被加速的机器上跑的轻量 agent
├── deploy/                # 部署脚本 + 生产 docker-compose + Caddy 反代配置
├── scripts/               # 项目级脚本（dev / smoke-test / lint）
└── prototype/             # 高保真 HTML 原型（不是产品代码，仅供设计参考）
```

---

## 3. 技术栈

| 层 | 选型 | 理由 |
|---|---|---|
| 后端 | Go 1.22+ | 单二进制、跨平台、低资源占用、反代性能强 |
| HTTP 框架 | `chi` 或 `gin`（倾向 `chi`，更轻） | 轻量、middleware 灵活 |
| 数据库 | SQLite（`modernc.org/sqlite` 纯 Go 驱动） | 单 VPS 部署免外部依赖，备份就是拷贝文件 |
| 缓存元数据 | SQLite | 不引入 Redis |
| Blob 存储 | 本地文件系统（`/var/lib/cncachehub/cache/`） | 简单、可靠、可直接 rsync 备份 |
| 前端 | Vue 3 + Vite + TypeScript | 用户已有的偏好栈 |
| UI 库 | Tailwind CSS + Element Plus | 极客暗黑风 + 表格 / 表单组件 |
| 状态 | Pinia | Vue 3 官方推荐 |
| 路由 | Vue Router 4 | 标准 |
| HTTP 客户端 | `axios` + 拦截器 | 统一处理 token / error |
| 部署 | Docker Compose + Caddy | HTTPS 自动证书 |
| 监控 | 标准 `/healthz`、`/metrics`（Prometheus） | 不引入额外依赖 |

---

## 4. 开发流程

### 4.1 本地开发

```bash
# 启动后端
cd server && go run ./cmd/cncachehub

# 启动前端（另一个终端）
cd web && npm install && npm run dev

# 或者用顶层脚本统一启动
./scripts/dev.sh
```

### 4.2 测试

- 后端：`go test ./...`，每个 package 必须有 `*_test.go`
- 前端：`npm run type-check && npm run lint && npm run test`
- 集成测试：`./scripts/smoke-test.sh`（启动 docker-compose，验证关键 API）

### 4.3 Commit 规范

按用户偏好（`memory: VPSide commit message 用中文`）：

```
<scope>: <中文一句话>

正文（可选）：详细解释为什么、改了什么、有什么副作用。
```

- `<scope>` 必须是英文，与目录名一致：`server` / `web` / `agent` / `deploy` / `docs` / `scripts`
- 例：
  ```
  server: 增加 SQLite 迁移脚本与 storage 封装
  web: 搭建 Vue 3 + Vite + Tailwind 工程脚手架
  deploy: 增加生产 docker-compose 与 Caddy 反代
  ```

### 4.4 推送策略

- 本仓库暂时只本地 git init，不主动推 remote
- 用户明确要求推送时，再走 `git remote add` + `git push`

---

## 5. 命名与代码风格

### 5.1 Go

- 包名小写、单词、单数（`api` / `storage` / `cache` / `proxy`）
- 公开 API 必须有 godoc 注释
- error 优先返回，不要 panic；panic 仅在不可恢复的初始化错误
- 配置通过 `internal/config` 加载，**不要**在业务代码里直接 `os.Getenv`
- 上下文优先使用 `context.Context`，跨 goroutine 必须传递

### 5.2 TypeScript / Vue

- 组件用 `<script setup lang="ts">`
- API 客户端统一在 `src/api/`，**不要**在组件里直接 `axios`
- 状态用 Pinia store，**不要**用全局变量
- 路由懒加载（`() => import(...)`）
- 类型定义集中放 `src/types/`

### 5.3 数据库

- 表名复数下划线（`users` / `cache_entries` / `cleanup_tasks`）
- 字段名小写下划线（`created_at` / `expires_at` / `last_access_at`）
- 主键统一 `id INTEGER PRIMARY KEY AUTOINCREMENT`
- 时间戳统一 INTEGER（Unix 秒）

### 5.4 API

- RESTful 风格
- JSON 字段用 camelCase
- 错误响应统一 `{ "error": { "code": "string", "message": "string", "details": {} } }`
- 列表接口统一 `{ "items": [...], "total": N, "page": N, "pageSize": N }`
- 所有路径前缀 `/api/...`

---

## 6. 安全约束（不可妥协）

以下边界由 PRD 明确，**任何 agent 不得越过**：

1. **白名单优先**：资源加速中心必须基于显式白名单规则（repo / 模型名 / 域名 / 版本规则），**不**做"任意 URL 开放代理"。
2. **不绕过平台授权**：不破解 Steam、GitHub、Hugging Face 等平台的账号或 token，不保存用户明文密码。
3. **敏感 URL 不缓存**：带 `?token=` `?signature=` `?session=` 的 URL 默认不缓存，日志必须脱敏。
4. **小容量 VPS 安全**：单对象落盘大小限制、缓存总上限、系统保底空间、只读旁路必须生效。**不**因为缓存失败而中断用户下载请求（必须支持旁路转发）。
5. **HTTPS 证书**：默认只反代 HTTPS 上游，不做 TLS 中间人解密；自签证书场景通过可信 CA 列表配置。
6. **认证与授权**：控制台 API 必须鉴权（首版 Cookie + CSRF，后续可加 Token）；公开代理入口必须可独立配置访问控制。
7. **日志脱敏**：所有日志中的 token / cookie / 密码必须脱敏（`***`）。

---

## 7. 性能约束

- 控制台 API P95 响应时间 ≤ 300ms
- 大文件下载流式转发，不一次性读入内存
- 小容量 VPS 优化下：单进程常驻内存目标 ≤ 256MB，空闲 CPU 接近 0
- 缓存写入支持断点续传 + 原子替换（写到 tmp，再 rename）

---

## 8. 不要做的事

明确禁止以下行为，避免 agent 跑偏：

- ❌ 不要引入 docker swarm / k8s / 微服务框架 — 这是一个单 VPS 自托管项目
- ❌ 不要引入 Redis / PostgreSQL / MySQL / 消息队列 — SQLite + 文件系统够用
- ❌ 不要做"通用 HTTP 代理" — 必须是白名单范围内的资源加速
- ❌ 不要做 TLS 中间人解密
- ❌ 不要保存用户明文密码 / token / cookie
- ❌ 不要做账号系统 / 社交登录 / OAuth
- ❌ 不要做付费功能 / 订阅 / 商业化
- ❌ 不要在前端硬编码假数据当真实数据用（演示数据放 `prototype/` 或加 `__demo` 前缀）
- ❌ 不要让 sub-agent 跨目录改不属于自己的代码（server agent 只能改 server/）
- ❌ 不要生成跟 PRD 冲突的方案

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
- 列出运行的测试命令与结果
- 列出已知未完成项（必须诚实，不准隐瞒）
- 不返回无关对话内容

---

## 10. 当前状态 / 路线图

> 这部分会随项目进展更新，最近一次更新在文件 git log 里可查。

### Phase 0 — 项目骨架（当前）

- [x] 完整 PRD（`docs/prd.md`）
- [x] 高保真 HTML 原型（`prototype/index.html`）
- [ ] Monorepo 目录 + AGENTS.md
- [ ] Go server 骨架（HTTP + SQLite + config + health API）
- [ ] Vue 3 web 骨架
- [ ] Docker Compose 部署脚本

### Phase 1 — Docker 加速 MVP

- [ ] Registry pull-through cache 代理
- [ ] Docker daemon.json 配置生成
- [ ] 缓存条目元数据
- [ ] 基础请求日志
- [ ] 总览仪表盘数据

### Phase 2 — 扩展模块

- [ ] SteamCMD 缓存
- [ ] 多 Registry（GHCR / Quay / k8s）
- [ ] 资源加速中心
- [ ] 诊断中心
- [ ] 定时清理任务
- [ ] 小容量 VPS 优化开关

### Phase 3 — 完善

- [ ] 鉴权 / RBAC
- [ ] 通知（webhook / 邮件）
- [ ] 多节点 / 高可用（可能不做）
- [ ] Helm Chart 部署
