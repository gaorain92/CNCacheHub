# Phase 1 — 第一刀：Docker Hub Pull-Through MVP

> 目标：在已部署的开发 VPS 上跑通 Docker Hub 镜像代理，让 `docker pull nginx:latest` 能过 CNCacheHub、落盘、二次命中。
>
> 后续模块（多 Registry / SteamCMD / 资源加速 / 鉴权）都共用本套架构，先把这条主路径打通。

## 1. Scope

### In scope
- Server: 缓存层（blob 落盘） + Registry v2 协议 + 路由挂载
- DB: `cache_entries` / `access_logs` / `registry_upstreams` 三张表
- Config: `CacheDir` / `UpstreamRegistry` / 小容量 VPS 三件套
- Web: `DockerView` 代理配置 + `DashboardView` 真实数据 + `LogsView` 真实日志
- 端到端 smoke test（测试机配 daemon.json → docker pull）

### Out of scope（明确不做，留给后续 Phase）
- 多 Registry（GHCR / Quay / K8s） — 表结构留位，但只跑 dockerhub
- Www-Authenticate token dance — 公开镜像匿名够用
- 控制台登录 / session / RBAC
- 预热（preheat）任务
- 定时清理（cron）— 数据先攒着
- Webhook / 飞书 / Telegram 通知

## 2. 改动文件清单

### Server
| 路径 | 状态 | 说明 |
|---|---|---|
| `server/internal/cache/cache.go` | 新建 | blob 落盘 / 命中查询 / bypass 旁路 (~200 行) |
| `server/internal/cache/cache_test.go` | 新建 | hit / miss / size-limit / disk-low |
| `server/internal/proxy/proxy.go` | 新建 | `/v2/*` 协议路由 + handler 装配 (~150 行) |
| `server/internal/proxy/upstream.go` | 新建 | 上游 HTTP 客户端（流式 + 超时 + UA） (~120 行) |
| `server/internal/proxy/rewrite.go` | 新建 | `nginx` → `library/nginx` 重写 (~50 行) |
| `server/internal/proxy/proxy_test.go` | 新建 | rewrite / 错误归一 / 404 / 401 |
| `server/internal/config/config.go` | 改 | 加 `CacheDir` / `UpstreamRegistry` / `SmallVPSOpt` / `MaxObjectSizeMB` / `ReserveSpaceGB` / `CacheTotalGB` |
| `server/internal/storage/migrations/20260717_001_phase1_cache.sql` | 新建 | 三张表 + 启动默认行 |
| `server/internal/api/api.go` | 改 | 挂 `/v2/*` 到 proxy.Mount()；新增 `/api/docker/*` `/api/cache/*` `/api/logs` `/api/dashboard/*` |
| `server/cmd/cncachehub/main.go` | 改 | 初始化 cache + proxy 依赖注入 |

### Web
| 路径 | 状态 | 说明 |
|---|---|---|
| `web/src/api/docker.ts` | 新建 | fetch upstream / daemon.json / upstreams 列表 |
| `web/src/api/cache.ts` | 新建 | fetch 缓存条目 / 删除 / 清理 |
| `web/src/api/logs.ts` | 新建 | fetch access_logs 分页 |
| `web/src/api/dashboard.ts` | 新建 | fetch 命中率 / 流量 / 错误数 |
| `web/src/stores/docker.ts` | 新建 | Pinia store |
| `web/src/stores/cache.ts` | 新建 | Pinia store |
| `web/src/stores/logs.ts` | 新建 | Pinia store |
| `web/src/stores/dashboard.ts` | 新建 | Pinia store |
| `web/src/views/DockerView.vue` | 重写 | 代理入口（IP+端口） / upstream 状态 / daemon.json 复制按钮 / 缓存条目表 |
| `web/src/views/DashboardView.vue` | 改 | 真实数据：缓存条数 / 命中率 / 24h 流量 / 错误数 / 上游延迟 |
| `web/src/views/LogsView.vue` | 改 | 真实 access_logs，分页 + 过滤 |
| `web/src/components/DaemonJsonBlock.vue` | 新建 | daemon.json 片段生成器 + 复制按钮 + 适用系统说明 |

### 部署
| 路径 | 状态 | 说明 |
|---|---|---|
| `deploy/nginx/cncachehub.conf` | 改 | `/v2/` 优先于 SPA fallback 走 `proxy_pass` |
| 测试机 `/etc/nginx/sites-available/cncachehub` | 改 | 同上 |
| 测试机 `~/.docker/daemon.json` | 改 | 加 `insecure-registries: ["<测试机 IP>"]` + `registry-mirrors: ["http://<测试机 IP>"]` |

## 3. 关键设计决策

### 3.1 缓存路径布局
```
${CacheDir}/v2/${registry}/${repo_path}/blobs/${digest}
${CacheDir}/v2/${registry}/${repo_path}/manifests/${digest_or_ref}.json
```
- content-addressable：`digest` 天然不可变，天然去重
- 按 `${registry}` 分桶：未来加 GHCR / Steam 不打架
- `${repo_path}` 保留 `/`：`library/nginx` 和 `bitnami/postgresql` 不冲突
- 写盘走 `tmp` + `rename`，原子替换，断电不残留

### 3.2 小容量 VPS 旁路（PRD #4 安全约束）
落盘前查两件事，**不满足任一就旁路**（仍转发，不缓存）：
1. 单对象 > `CNCH_MAX_OBJECT_SIZE_MB`（默认 1024 MB） → 旁路
2. `CacheDir` 文件系统可用空间 < `CNCH_RESERVE_SPACE_GB`（默认 5 GB） → 旁路

旁路 = 透明转发（`io.Copy(w, upstreamBody)`），不写盘、不算命中。响应头加 `X-CNCacheHub-Bypass: <reason>`，方便诊断。

**关键：不因为缓存失败而中断用户下载**（PRD 强约束 #4）。

### 3.3 library/* 自动补全
Docker 客户端拉 `nginx` 时，Registry URL 是 `/v2/nginx/manifests/latest`，但 Docker Hub 上是 `library/nginx`。中间件统一改成 `library/<name>`（参考 `distribution/distribution` 的官方实现）。

判断规则：`<name>` 不含 `/` 且不是 `library/` 开头 → 前缀补 `library/`。

### 3.4 Token 鉴权（先不做）
MVP 跳过 `Www-Authenticate` 协议 dance。
- 公开镜像（library/*、大多数常用 namespace）够用
- 私有镜像 → Phase 1.1（容器化部署时再做）
- 文档说明里加"私有镜像暂不支持"

### 3.5 流式传输
- **绝不用 `io.ReadAll`** —— 任何层都走 `io.Copy(dst, src)`
- manifest 透传上游 response body
- blob 落盘同时 `TeeReader` 给客户端
- `http.ServeContent` / `http.Flush` 周期性 push，避免缓冲

### 3.6 Access log
- 每个请求一行 INSERT（path / status / duration_ms / cached / client_ip / ts / bytes）
- 写入走独立 goroutine + channel，**不阻塞主请求**
- 启动时若 > 100k 行 → 截断保留最近 50k
- 错误摘要脱敏（`X-Forwarded-For` 之类不记）

### 3.7 上游选择
- 启动时 `registry_upstreams` 表只有 `dockerhub` 一行
- 后续加 GHCR / Quay 直接 INSERT + 改 `enabled=1`，无需代码改动

## 4. 数据库 Schema

```sql
-- 20260717_001_phase1_cache.sql

CREATE TABLE cache_entries (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  registry TEXT NOT NULL,             -- 'dockerhub' / 'ghcr' / 'steam' / ...
  repository TEXT NOT NULL,           -- 'library/nginx'
  digest TEXT NOT NULL,               -- 'sha256:abc...'
  media_type TEXT NOT NULL,           -- 'application/vnd.docker.image.rootfs.diff.tar.gzip'
  size_bytes INTEGER NOT NULL,
  storage_path TEXT NOT NULL,         -- 相对 CacheDir 的路径
  hit_count INTEGER NOT NULL DEFAULT 0,
  last_access_at INTEGER NOT NULL,
  created_at INTEGER NOT NULL,
  UNIQUE(registry, repository, digest)
);
CREATE INDEX idx_cache_entries_last_access ON cache_entries(last_access_at);
CREATE INDEX idx_cache_entries_registry ON cache_entries(registry);

CREATE TABLE access_logs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  ts INTEGER NOT NULL,
  method TEXT NOT NULL,
  path TEXT NOT NULL,                 -- '/v2/library/nginx/manifests/latest'
  status INTEGER NOT NULL,
  duration_ms INTEGER NOT NULL,
  cached INTEGER NOT NULL,            -- 0/1
  bypassed INTEGER NOT NULL DEFAULT 0,
  client_ip TEXT,
  bytes INTEGER NOT NULL DEFAULT 0,
  error TEXT                          -- 上游错误摘要
);
CREATE INDEX idx_access_logs_ts ON access_logs(ts);

CREATE TABLE registry_upstreams (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL UNIQUE,          -- 'dockerhub'
  upstream_url TEXT NOT NULL,         -- 'https://registry-1.docker.io'
  mirror_path TEXT NOT NULL,          -- '/v2'
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at INTEGER NOT NULL
);

-- 启动种子：默认 dockerhub
INSERT OR IGNORE INTO registry_upstreams (name, upstream_url, mirror_path, enabled, created_at)
VALUES ('dockerhub', 'https://registry-1.docker.io', '/v2', 1, strftime('%s','now'));
```

## 5. API 端点（除 `/v2/*` 代理外的新增）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/docker/upstreams` | 列出所有 enabled upstream |
| GET | `/api/docker/daemon.json?upstream=dockerhub` | 生成可直接用的 daemon.json 片段（JSON 字符串） |
| GET | `/api/cache/entries?page=1&pageSize=20&registry=dockerhub` | 分页 |
| DELETE | `/api/cache/entries/:id` | 删一条（删 blob 文件 + 元数据） |
| POST | `/api/cache/clean?strategy=lru&olderThan=7d` | 批量清理（先 dry-run，确认再真删） |
| GET | `/api/logs?page=1&pageSize=50&status=4xx` | access_logs 分页 |
| GET | `/api/dashboard/summary` | 命中率 / 缓存条数 / 24h 流量 / 错误数 / 上游延迟 |

错误响应统一 `{"error":{"code":"...","message":"..."}}`。

## 6. 验收标准

### Server
- [ ] `curl -s http://127.0.0.1:8082/v2/` → `200 {}`
- [ ] `curl -s http://127.0.0.1:8082/v2/library/nginx/manifests/latest` → 200 真实 manifest
- [ ] `curl -L http://127.0.0.1:8082/v2/library/nginx/blobs/sha256:...` → 落盘到 `${CacheDir}/v2/dockerhub/library/nginx/blobs/...`
- [ ] 重复拉同一 blob → `cache_entries.hit_count` 自增
- [ ] 单对象超 1024 MB → 响应头 `X-CNCacheHub-Bypass: size_limit`，不写盘
- [ ] 磁盘预留 < 5 GB → 响应头 `X-CNCacheHub-Bypass: disk_low`，不写盘
- [ ] `go test ./...` 全绿
- [ ] access_logs 行数 < 100k（启动时自动截断）

### Web
- [ ] `/` Dashboard 显示真实数据：缓存条数 / 命中率 / 24h 流量 / 错误数
- [ ] `/docker` 页面有"复制 daemon.json"按钮，复制内容可粘贴即用
- [ ] `/docker` 页面有缓存条目表（分页 + 删除按钮）
- [ ] `/logs` 页面有 access_logs 表（分页 + 状态过滤）
- [ ] `npm run build` 成功

### 端到端
- [ ] 测试机 `~/.docker/daemon.json` 配 `registry-mirrors` + `insecure-registries`
- [ ] `docker pull <测试机 IP>/library/nginx:latest`（或 `nginx:latest` 走 mirror）成功
- [ ] 二次 `docker pull` 命中率 100%（从 cache 出）
- [ ] Web Dashboard 数字和实际匹配

## 7. 风险 + 兜底

| 风险 | 兜底 |
|---|---|
| 1GB RAM 编译 modernc.org/sqlite OOM | 本机编译 → scp 过去（Phase 0 已踩） |
| 大 layer 内存爆 | `io.Copy`，缓存写入 `tmp` + `rename` |
| access_log 写阻塞主流程 | goroutine + 100ms 批量 flush |
| `/v2/` 被 nginx SPA fallback 抢 | nginx `/v2/` 走 `proxy_pass`，在 `try_files` 之前 |
| Docker daemon 27+ 拒非 HTTPS mirror | daemon.json 加 `insecure-registries: ["<测试机 IP>"]` |
| 跨太平洋拉 Docker Hub 慢 | 海外服务器直连 ~5MB/s，可接受 |
| 1GB RAM 跑 daemon proxy 内存爆 | `SmallVPSOpt=true` 时 GC 更激进 + 大对象早释放 |

## 8. 顺序（1 个完整工作块）

1. **config 扩展**（1 文件） — 30 min
2. **migration**（1 SQL） — 10 min
3. **cache 包**（PUT/GET/bypass） — 1.5 h
4. **proxy 包**（v2 协议 + upstream） — 2 h
5. **api 路由挂载 + main 初始化** — 30 min
6. **本机 go test + 编译 + scp + restart** — 30 min
7. **curl /v2/ + /v2/library/nginx/manifests 烟雾测试** — 15 min
8. **测试机 docker daemon.json + docker pull** — 30 min
9. **web API client + DockerView 重写** — 1.5 h
10. **web Dashboard 真实数据** — 30 min
11. **web build + nginx reload** — 10 min
12. **端到端验收 + 录屏** — 30 min

**总估：~8 小时**，按一个工作块 1 天吃完。

## 9. 不在 MVP 但留好接口

下面这些 Phase 1 MVP 不实现，但代码结构要留好：

- `RegistryUpstream` 表已经支持多 registry
- `cache.Store` 接口化（`Put / Get / Stat / Delete`），未来加 SteamCMD 同款接口
- `proxy` 包按 registry 分类函数（`dockerProxy` / `steamProxy` / ...），Phase 2 加新协议不动架构
- `access_logs.bypassed` 字段已经标好，未来旁路统计直接 `SELECT COUNT WHERE bypassed=1`
- config 加 `CacheTotalGB` 上限字段但先不强制，留给 Phase 1.2 cron 清理
