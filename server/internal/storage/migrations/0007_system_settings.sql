-- 0007_system_settings.sql
-- 系统设置（key-value 表）。
--
-- 字段：key 主键、value 用 TEXT（json 序列化或纯文本，按 key 约定）。
-- 首次启动时 main.go 会把 env 里的默认值同步进来；
-- UI 改值写表，重启时仍按表里值生效（env 降级为兜底）。

CREATE TABLE IF NOT EXISTS system_settings (
    key        TEXT    PRIMARY KEY,
    value      TEXT    NOT NULL,
    updated_at INTEGER NOT NULL,
    updated_by INTEGER NOT NULL DEFAULT 0
);

-- 种子：首次启动会覆写；这里给个安全的空默认。
INSERT OR IGNORE INTO system_settings (key, value, updated_at, updated_by)
VALUES
    ('small_vps_opt',     'false', strftime('%s', 'now'), 0),
    ('reserve_space_gb',  '5',     strftime('%s', 'now'), 0),
    ('max_object_size_mb','1024',  strftime('%s', 'now'), 0),
    ('cache_total_gb',    '20',    strftime('%s', 'now'), 0),
    ('cleanup_trigger_pct','80',   strftime('%s', 'now'), 0),
    ('cleanup_target_pct','60',    strftime('%s', 'now'), 0);
