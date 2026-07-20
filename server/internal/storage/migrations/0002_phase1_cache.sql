-- 0002_phase1_cache.sql
-- Phase 1：把 0001 留的骨架表扩字段 + 新建 registry_upstreams。
--
-- 命名约定同 0001（复数下划线 / id INTEGER PK / INTEGER 时间戳）。
-- ALTER TABLE 不可逆：所有 DDL 在新表重建 / 字段弃用时再走 0003 迁移。
--
-- 注：SQLite 的 ALTER TABLE ADD COLUMN 不支持 NOT NULL DEFAULT（必须给 default 或允许 NULL）。
-- 这里所有新增列都给 NOT NULL + DEFAULT，保证 INSERT 时不会因为历史 row 缺字段报错。

-- ============================================================
-- cache_entries: 已落盘的缓存对象元数据
-- ============================================================
ALTER TABLE cache_entries ADD COLUMN registry       TEXT    NOT NULL DEFAULT 'dockerhub';
ALTER TABLE cache_entries ADD COLUMN repository     TEXT    NOT NULL DEFAULT '';
ALTER TABLE cache_entries ADD COLUMN digest         TEXT    NOT NULL DEFAULT '';
ALTER TABLE cache_entries ADD COLUMN media_type     TEXT    NOT NULL DEFAULT 'application/octet-stream';
ALTER TABLE cache_entries ADD COLUMN size_bytes     INTEGER NOT NULL DEFAULT 0;
ALTER TABLE cache_entries ADD COLUMN storage_path   TEXT    NOT NULL DEFAULT '';
ALTER TABLE cache_entries ADD COLUMN hit_count      INTEGER NOT NULL DEFAULT 0;
ALTER TABLE cache_entries ADD COLUMN last_access_at INTEGER NOT NULL DEFAULT 0;

-- 删掉 0001 的 DEFAULT 之后再加 UNIQUE 约束（SQLite 限制：被引用列不能有非 NULL DEFAULT）
-- 实际上 SQLite 允许 UNIQUE on TEXT DEFAULT ''，但多条空 record 会冲突。
-- 解法：partial unique index，只在 digest 非空时强制唯一。
CREATE UNIQUE INDEX IF NOT EXISTS idx_cache_entries_unique
    ON cache_entries(registry, repository, digest)
    WHERE digest != '';

CREATE INDEX IF NOT EXISTS idx_cache_entries_last_access ON cache_entries(last_access_at);
CREATE INDEX IF NOT EXISTS idx_cache_entries_registry    ON cache_entries(registry);
CREATE INDEX IF NOT EXISTS idx_cache_entries_size        ON cache_entries(size_bytes);

-- ============================================================
-- request_logs: 访问日志（每个 HTTP 请求一行，PRD §9.2 缓存一致性可观测用）
-- ============================================================
ALTER TABLE request_logs ADD COLUMN method       TEXT    NOT NULL DEFAULT 'GET';
ALTER TABLE request_logs ADD COLUMN path         TEXT    NOT NULL DEFAULT '';
ALTER TABLE request_logs ADD COLUMN status       INTEGER NOT NULL DEFAULT 0;
ALTER TABLE request_logs ADD COLUMN duration_ms  INTEGER NOT NULL DEFAULT 0;
ALTER TABLE request_logs ADD COLUMN cached       INTEGER NOT NULL DEFAULT 0;
ALTER TABLE request_logs ADD COLUMN bypassed     INTEGER NOT NULL DEFAULT 0;
ALTER TABLE request_logs ADD COLUMN client_ip    TEXT    NOT NULL DEFAULT '';
ALTER TABLE request_logs ADD COLUMN bytes        INTEGER NOT NULL DEFAULT 0;
ALTER TABLE request_logs ADD COLUMN error        TEXT    NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_request_logs_ts     ON request_logs(created_at);
CREATE INDEX IF NOT EXISTS idx_request_logs_status ON request_logs(status);

-- ============================================================
-- registry_upstreams: 上游 Registry 配置
-- ============================================================
CREATE TABLE IF NOT EXISTS registry_upstreams (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    name          TEXT    NOT NULL UNIQUE,             -- 'dockerhub' / 'ghcr' / 'steam' / ...
    upstream_url  TEXT    NOT NULL,                    -- 'https://registry-1.docker.io'
    mirror_path   TEXT    NOT NULL DEFAULT '/v2',      -- 客户端访问路径前缀
    enabled       INTEGER NOT NULL DEFAULT 1,           -- 0/1
    created_at    INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_registry_upstreams_enabled ON registry_upstreams(enabled);

-- 启动种子：默认 Docker Hub。
-- 注意：name 在表上是 UNIQUE，多机部署或重置时这条会被忽略（INSERT OR IGNORE）。
INSERT OR IGNORE INTO registry_upstreams (name, upstream_url, mirror_path, enabled, created_at)
VALUES ('dockerhub', 'https://registry-1.docker.io', '/v2', 1, strftime('%s', 'now'));
