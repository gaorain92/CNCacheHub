-- 0008_bypass_reason.sql
-- request_logs 加 bypass_reason 字段（PRD §9.6.4 要求 BYPASS_SIZE_LIMIT 标记）。
--
-- bypassed 字段是 0/1 二值；bypass_reason 存具体原因：
--   '' / 'size_limit' / 'disk_low'
-- 老行 bypass_reason 默认为 ''。

ALTER TABLE request_logs ADD COLUMN bypass_reason TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_request_logs_bypass_reason ON request_logs(bypass_reason) WHERE bypass_reason != '';
