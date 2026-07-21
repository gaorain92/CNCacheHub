-- 0004_cleanup_tasks.sql
-- 扩展 cleanup_tasks 表（0001 已建骨架，id + created_at）。
-- 加列 + 索引；逐条 ALTER，重复跑（重复加列）会失败 — 但我们 schema_migrations
-- 跟踪保证每个 migration 只跑一次。
--
-- 注：SQLite 不支持 `ALTER TABLE ... ADD COLUMN IF NOT EXISTS`，所以靠 schema_migrations 幂等。

ALTER TABLE cleanup_tasks ADD COLUMN task_name         TEXT    NOT NULL DEFAULT '';
ALTER TABLE cleanup_tasks ADD COLUMN strategy          TEXT    NOT NULL DEFAULT '';
ALTER TABLE cleanup_tasks ADD COLUMN threshold_seconds INTEGER NOT NULL DEFAULT 604800;
ALTER TABLE cleanup_tasks ADD COLUMN threshold_bytes   INTEGER NOT NULL DEFAULT 21474836480;
ALTER TABLE cleanup_tasks ADD COLUMN enabled           INTEGER NOT NULL DEFAULT 1;
ALTER TABLE cleanup_tasks ADD COLUMN cron_interval_sec INTEGER NOT NULL DEFAULT 3600;
ALTER TABLE cleanup_tasks ADD COLUMN last_run_at       INTEGER NOT NULL DEFAULT 0;
ALTER TABLE cleanup_tasks ADD COLUMN last_status       TEXT    NOT NULL DEFAULT '';
ALTER TABLE cleanup_tasks ADD COLUMN last_freed_bytes  INTEGER NOT NULL DEFAULT 0;
ALTER TABLE cleanup_tasks ADD COLUMN last_freed_count  INTEGER NOT NULL DEFAULT 0;

-- 旧 task_name 会有重复空字符串，加 UNIQUE INDEX 会失败；用 NOT NULL 已有空字符串兜底。
-- 如果未来要严格 UNIQUE，改 schema 加 UNIQUE 约束并 migrate。
CREATE INDEX IF NOT EXISTS idx_cleanup_tasks_enabled ON cleanup_tasks(enabled);
