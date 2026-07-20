-- 0003_cache_bypassed.sql
-- 补 cache_entries.bypassed / bypass_reason 字段（0002 漏了）。
-- 原因：proxy.fetchAndCache 知道 sw.Bypassed()，需要落库才能在 UI / 仪表盘区分命中/旁路。

ALTER TABLE cache_entries ADD COLUMN bypassed INTEGER NOT NULL DEFAULT 0;
ALTER TABLE cache_entries ADD COLUMN bypass_reason TEXT NOT NULL DEFAULT '';

-- 加索引方便过滤（按时间排之外，按旁路状态过滤也常见）。
CREATE INDEX IF NOT EXISTS idx_cache_entries_bypassed ON cache_entries(bypassed);
