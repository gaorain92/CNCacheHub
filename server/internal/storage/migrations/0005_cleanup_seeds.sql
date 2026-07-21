-- 0005_cleanup_seeds.sql
-- 启动种子：默认 LRU 7 天 + capacity 20GB。
INSERT OR IGNORE INTO cleanup_tasks (task_name, strategy, threshold_seconds, threshold_bytes, enabled, cron_interval_sec, created_at)
VALUES
    ('default-lru', 'lru', 604800, 0, 1, 3600, strftime('%s', 'now')),
    ('capacity-cap', 'capacity', 0, 21474836480, 1, 3600, strftime('%s', 'now'));
