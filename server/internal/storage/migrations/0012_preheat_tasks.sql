-- 0012_preheat_tasks.sql — 通用预热任务（PRD §9.2.3 / §9.3.3 / §9.5.5）
--
-- kind: 'docker' | 'steam' | 'resource'
-- targets: JSON 数组（每项是单条镜像名 / Steam AppID / 资源 URL）
-- status: 'pending' | 'running' | 'done' | 'error' | 'canceled'
-- progress_total / progress_done: 任务进度计数
-- cron_expression: 非空 = 周期任务（暂不实现解析，留接口给后续 robfig/cron）
-- next_run_at / last_run_at / last_duration_ms: 调度统计

CREATE TABLE IF NOT EXISTS preheat_tasks (
  id               INTEGER PRIMARY KEY AUTOINCREMENT,
  name             TEXT    NOT NULL,
  kind             TEXT    NOT NULL,                    -- 'docker' | 'steam' | 'resource'
  targets_json     TEXT    NOT NULL DEFAULT '[]',       -- JSON 字符串数组
  status           TEXT    NOT NULL DEFAULT 'pending',  -- 'pending' | 'running' | 'done' | 'error' | 'canceled'
  progress_total   INTEGER NOT NULL DEFAULT 0,
  progress_done    INTEGER NOT NULL DEFAULT 0,
  progress_bytes   INTEGER NOT NULL DEFAULT 0,           -- 已下载字节累加
  error_message    TEXT    NOT NULL DEFAULT '',
  cron_expression  TEXT    NOT NULL DEFAULT '',          -- 空 = 一次性；非空 = 周期
  enabled          INTEGER NOT NULL DEFAULT 1,
  next_run_at      INTEGER NOT NULL DEFAULT 0,
  last_run_at      INTEGER NOT NULL DEFAULT 0,
  last_duration_ms INTEGER NOT NULL DEFAULT 0,
  retry_count      INTEGER NOT NULL DEFAULT 0,           -- 累计失败重试次数
  max_retries      INTEGER NOT NULL DEFAULT 2,
  created_at       INTEGER NOT NULL,
  updated_at       INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_preheat_tasks_status ON preheat_tasks(status);
CREATE INDEX IF NOT EXISTS idx_preheat_tasks_enabled ON preheat_tasks(enabled);

-- preheat_items: 任务下每条 target 的执行状态（独立行，便于失败重试单条）
CREATE TABLE IF NOT EXISTS preheat_items (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  task_id       INTEGER NOT NULL,
  target        TEXT    NOT NULL,                       -- 单条镜像 / AppID
  status        TEXT    NOT NULL DEFAULT 'pending',    -- 'pending' | 'running' | 'done' | 'error' | 'skipped'
  error_message TEXT    NOT NULL DEFAULT '',
  bytes_added   INTEGER NOT NULL DEFAULT 0,
  started_at    INTEGER NOT NULL DEFAULT 0,
  finished_at   INTEGER NOT NULL DEFAULT 0,
  FOREIGN KEY (task_id) REFERENCES preheat_tasks(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_preheat_items_task_id ON preheat_items(task_id);
CREATE INDEX IF NOT EXISTS idx_preheat_items_status ON preheat_items(status);
