-- 0001_init.sql
-- 初始 schema：建立 schema_migrations 表 + 给后续模块留 schema 占位。
--
-- 命名约定：
--   * 表名复数下划线（cache_entries / request_logs / cleanup_tasks）
--   * 主键统一 `id INTEGER PRIMARY KEY AUTOINCREMENT`
--   * 时间戳统一 INTEGER（Unix 秒）
--   * 外键统一 `xxx_id INTEGER REFERENCES xxx(id) ON DELETE ...`
--
-- Phase 1+ 会通过 ALTER TABLE / 新增 migrations 进一步完善字段；本文件只搭骨架。

-- 迁移记录表：每条成功应用过的 migration 一行。
CREATE TABLE IF NOT EXISTS schema_migrations (
    filename   TEXT PRIMARY KEY,
    applied_at INTEGER NOT NULL
);

-- 缓存条目：phase 1 用于 Docker / SteamCMD / 资源加速中心统一记录已落盘对象。
-- 字段在 phase 1 会用 ALTER 扩展（size_bytes / source / upstream_url / content_type / last_access_at / ...）。
CREATE TABLE IF NOT EXISTS cache_entries (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    created_at INTEGER NOT NULL
);

-- 请求日志：phase 1 用于缓存命中率、上游可用性、下载速度等可观测数据。
-- 字段在 phase 1 会用 ALTER 扩展（method / path / status / duration_ms / client_ip / user_agent / cache_hit / ...）。
CREATE TABLE IF NOT EXISTS request_logs (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    created_at INTEGER NOT NULL
);

-- 清理任务：phase 2 用于定时清理过期 / 超大对象。
-- 字段在 phase 2 会用 ALTER 扩展（name / cron / enabled / last_run_at / last_status / ...）。
CREATE TABLE IF NOT EXISTS cleanup_tasks (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    created_at INTEGER NOT NULL
);
