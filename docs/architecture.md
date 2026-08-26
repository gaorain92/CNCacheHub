# CNCacheHub 技术架构

> 状态：v1.0（伴随 PRD v1.0）。记录关键技术决策 + 后续扩展预留。

## 1. 整体架构（v1.0 单节点）

```
┌────────────────────────────────────────┐
│  docker daemon / nerdctl / k3s         │  客户端
└──────────┬─────────────────────────────┘
           │ HTTPS (nginx 443/80)
┌──────────▼─────────────────────────────┐
│  nginx (反代 + SPA fallback + 静态)   │  边缘
└──────────┬─────────────────────────────┘
           │ HTTP 127.0.0.1:8082
┌──────────▼─────────────────────────────┐
│  cncachehub-server (Go binary)         │
│  ┌────────────────────────────────┐   │
│  │ chi router + middleware        │   │
│  │ ├ /v2/*  (proxy + access ctrl) │   │  ──→ upstream
│  │ ├ /r/*   (resource proxy)      │   │     (registry-1.docker.io)
│  │ ├ /api/* (admin/console API)   │   │
│  │ ├ /metrics (Prometheus)        │   │
│  │ ├ /healthz (LB)                │   │
│  │ └ /v1/dns (UDP/TCP :5353)      │   │  SteamCMD DNS
│  └────────────────────────────────┘   │
│  ┌────────────────────────────────┐   │
│  │ cache.FileStore                │   │
│  │ ├ /var/lib/cncachehub/cache/   │   │
│  │ │  └ blobs/<sha256[0:2]>/<...> │   │
│  │ └ LRU + Capacity 清理 (cron)   │   │
│  └────────────────────────────────┘   │
│  ┌────────────────────────────────┐   │
│  │ SQLite (modernc.org/sqlite)    │   │
│  │ └ 13 张表（migrations 0001-13） │   │
│  └────────────────────────────────┘   │
└────────────────────────────────────────┘
```

## 2. 包结构

| 包 | 职责 |
|---|---|
| `cmd/cncachehub` | main 入口、依赖装配、信号处理、graceful shutdown |
| `internal/api` | HTTP 路由 + handler + DTO + middleware (auth/access/log) |
| `internal/proxy` | 镜像反代核心：流式转发 + cache write + access log |
| `internal/cache` | FileStore (blob 落盘) + Policy (size/reserve) + LRU |
| `internal/storage` | SQLite 数据访问（13 张表） + bcrypt + 迁移 |
| `internal/dns` | SteamCMD DNS 启动器（miekg/dns，UDP+TCP :5353） |
| `internal/access` | 代理访问控制 middleware (Token + IP 白名单) |
| `internal/diagnostics` | 诊断中心（3 剧本）+ 诊断包导出（tar.gz） |
| `internal/preheat` | 预热任务执行器（goroutine + docker run） |
| `internal/metrics` | Prometheus text-format 渲染（无外部依赖） |
| `internal/log` | slog 包装 + 敏感字段脱敏（admin_password / token） |
| `internal/config` | env 加载 + 校验 + 启动时同步到 system_settings |
| `web/` | Vue 3 + Vite + Tailwind + Element Plus + Pinia |

## 3. 关键技术决策

### 3.1 SQLite + 本地文件系统（不做 Redis / Postgres）

**为什么**：单 VPS 自托管场景免外部依赖；备份就是拷贝 `cncachehub.db` + `cache/`；足够撑到 10w cache entries。

**约束**：
- 单进程写（多进程会 lock 失败）；暂不考虑读写分离。
- 备份脚本：cron 跑 `sqlite3 .backup` + rsync。

### 3.2 Prometheus 指标手写（不引 prometheus/client_golang）

**为什么**：
- 18 个指标，体量小（5KB 代码）；
- 减少依赖（cgo 链、版本耦合）；
- 完全可控 label 集合（防 cardinality 爆炸）。

**位置**：`internal/metrics/metrics.go` 输出 text-format 给 `/metrics` 端点。

### 3.3 SteamCMD DNS 用 5353 端口（不要 root）

LANCache 项目用 53 端口需要 root / setcap。CNCacheHub 选 5353，让用户在路由器 / 本机 hosts 上端口转发即可。

### 3.4 access control 应用到 `/v2/*` 和 `/r/*` 但 NOT `/api/*` / `/metrics`

`/api/*` 是控制台 API（已经有 session cookie 鉴权，admin-only）；`/metrics` 是 Prometheus scrape（习惯上不被内网 middleware 拦截）。
访问控制只针对"对客户端开放的代理路径"。

### 3.5 诊断包导出走流式 gzip+tar，不在内存拼大文件

`WriteBundle` 用 `archive/tar` + `compress/gzip` 边读 DB 边写 w，O(1) 内存。
单条文件失败不影响整个 bundle（`_ = add(...)` 吞错）。

## 4. 未来扩展预留（deferred）

### 4.1 多节点缓存（PRD §19.3 P2#5 — deferred）

**为什么 deferred**：
- 单 VPS 自托管是核心定位（PRD §1.0）；
- 多节点会引入一致性、leader election、节点间同步 3 大难题；
- 当前实现已经把 cache 路径设计成"sha256[0:16] 分桶" — 跨节点同步时可直接用同源路径做 rsync。

**未来接口预留**（不实现，只标位）：

#### DB schema
```sql
-- 后续 P2.5+ 加：
CREATE TABLE nodes (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    name            TEXT    NOT NULL UNIQUE,
    role            TEXT    NOT NULL DEFAULT 'standalone',  -- standalone | edge | hub
    api_base_url    TEXT    NOT NULL,
    auth_token      TEXT    NOT NULL,                       -- node <-> server 鉴权
    enabled         INTEGER NOT NULL DEFAULT 1,
    last_seen_at    INTEGER NOT NULL DEFAULT 0,
    created_at      INTEGER NOT NULL
);
CREATE TABLE node_health (
    node_id         INTEGER PRIMARY KEY,
    reachable       INTEGER NOT NULL DEFAULT 0,
    latency_ms      INTEGER NOT NULL DEFAULT 0,
    cache_entries   INTEGER NOT NULL DEFAULT 0,
    cache_bytes     INTEGER NOT NULL DEFAULT 0,
    last_check_at   INTEGER NOT NULL DEFAULT 0
);
```

#### 路由策略
- 一致性 hash：repo name → 节点（replica=2 写双份）；
- 客户端拉镜像时，server 看本地有 → 直接返；没有 → 转发到 owner 节点（X-Forwarded-To 头）后 stream 回来 + 落本地；
- 写：本地写 → async gossip 通知其它节点（pull-based 兜底）。

#### 资源加速中心 `resource_rules` 已经预留 `upstream_url` 字段
多节点时，每个 rule 可指定"默认由哪个节点服务"（`home_node_id`），其它节点做 read-through。

### 4.2 节点健康广播
当 standalone 时无需；多节点时 server 启一个 `node/health` endpoint，token 鉴权，节点每 30s 报心跳。

### 4.3 BundleSource 已经预留
`diagnostics.BundleSource` 可以加 `RemoteNode *NodeSnapshot` 字段，把多节点 topology 信息也包进 bundle（运维人员一眼看出拓扑）。

## 5. 部署

### 5.1 单 VPS（当前支持）
- `deploy/production-docker-compose.yml`：cncachehub-server + nginx + 可选 Caddy
- systemd：`/etc/systemd/system/cncachehub-server.service`（已就位）
- nginx 反代：location ordering 关键（`/v2/` `/r/` `= /metrics` `= /healthz` 必须在 `/` SPA fallback 之前）

### 5.2 多 VPS（未来）
- 节点 1：cncachehub-server + nginx + cache dir（rsync 到其它节点）
- 节点 2-9：同上
- 入口：caddy / haproxy 4 层按 repo hash 转发

## 6. 监控

- Prometheus 抓 `/metrics`（18 个指标）；
- 诊断包 `/api/diagnostics/bundle`（人工触发，5KB tar.gz 含 16 个文件）；
- 审计日志 `audit_logs`（admin 改值都会写一条）。

---

## 7. 测试

- 后端：每个 package 都有 `*_test.go`；go test ./...
- 前端：vue-tsc + vite build（tsc 严格模式）；
- 集成：E2E 用 curl 跑在用户的开发 VPS 上（具体 IP 见用户本地凭据），每个 P2 commit 后跑一遍。
