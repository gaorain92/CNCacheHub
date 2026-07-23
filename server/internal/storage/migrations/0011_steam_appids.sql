-- 0011_steam_appids.sql — SteamCMD AppID 元数据 + 预热记录（PRD §9.3.3）
--
-- app_id：Steam 应用 ID（integer，CS2=730、Palworld=2394010 等）
-- login_type：anonymous / account（PRD §9.3.3）
-- install_dir：示例安装目录（仅展示用，实际以用户机器为准）
-- preheat_*：最近一次预热的状态/时间/时长/消息
-- cache_bytes_estimate：用户填的缓存占用估算（手动或从 cleanup task 同步）
-- hit_count / miss_count：跑 SteamCMD 下载时的命中/未命中计数（用户可手动 reset）
-- enabled：是否在「预热任务」里被纳入
-- created_at / updated_at：维护时间

CREATE TABLE IF NOT EXISTS steam_appids (
  id                      INTEGER PRIMARY KEY AUTOINCREMENT,
  app_id                  INTEGER NOT NULL UNIQUE,
  name                    TEXT    NOT NULL,
  login_type              TEXT    NOT NULL DEFAULT 'anonymous',  -- 'anonymous' | 'account'
  install_dir             TEXT    NOT NULL DEFAULT '',
  enabled                 INTEGER NOT NULL DEFAULT 1,
  last_preheat_at         INTEGER NOT NULL DEFAULT 0,           -- unix 秒；0 = 从未
  last_preheat_status     TEXT    NOT NULL DEFAULT '',          -- 'ok' | 'error' | 'running'
  last_preheat_message    TEXT    NOT NULL DEFAULT '',
  last_preheat_duration_ms INTEGER NOT NULL DEFAULT 0,
  cache_bytes_estimate    INTEGER NOT NULL DEFAULT 0,
  hit_count               INTEGER NOT NULL DEFAULT 0,
  miss_count              INTEGER NOT NULL DEFAULT 0,
  created_at              INTEGER NOT NULL,
  updated_at              INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_steam_appids_enabled ON steam_appids(enabled);
CREATE INDEX IF NOT EXISTS idx_steam_appids_app_id ON steam_appids(app_id);

-- 7 个常见服务端（PRD §9.3.3 默认模板）
-- Steam login_type 默认 anonymous（公开仓库）
INSERT OR IGNORE INTO steam_appids
  (app_id, name, login_type, install_dir, enabled, created_at, updated_at)
VALUES
  (730,    'CS2 Dedicated Server',                       'anonymous', '/data/steamapps/cs2',           1, strftime('%s','now'), strftime('%s','now')),
  (2394010,'Palworld Dedicated Server',                  'anonymous', '/data/steamapps/palworld',       1, strftime('%s','now'), strftime('%s','now')),
  (896660, 'Valheim Dedicated Server',                   'anonymous', '/data/steamapps/valheim',        1, strftime('%s','now'), strftime('%s','now')),
  (258550, 'Rust Dedicated Server',                      'anonymous', '/data/steamapps/rust',           1, strftime('%s','now'), strftime('%s','now')),
  (2430930,'ARK: Survival Ascended Dedicated Server',    'anonymous', '/data/steamapps/ark',            1, strftime('%s','now'), strftime('%s','now')),
  (380870, 'Project Zomboid Dedicated Server',           'anonymous', '/data/steamapps/pz',             1, strftime('%s','now'), strftime('%s','now')),
  (105600,'Terraria Dedicated Server',                   'anonymous', '/data/steamapps/terraria',       1, strftime('%s','now'), strftime('%s','now'));
