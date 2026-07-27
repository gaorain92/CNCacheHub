-- 0015_log_retention.sql
-- 日志保留天数系统设置 + path 索引加速筛选

-- 新系统设置：log_retention_days，默认 30
INSERT OR IGNORE INTO system_settings (key, value, updated_at, updated_by)
VALUES ('log_retention_days', '30', strftime('%s','now'), 0);

-- path 索引加速 LIKE '%keyword%'（虽然 SQLite 不完全能用，但 = 精确匹配能走索引）
CREATE INDEX IF NOT EXISTS idx_request_logs_client_ip ON request_logs(client_ip);
CREATE INDEX IF NOT EXISTS idx_request_logs_method    ON request_logs(method);
