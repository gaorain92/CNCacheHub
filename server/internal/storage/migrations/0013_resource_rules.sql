-- 0013_resource_rules.sql — 资源加速中心（PRD §9.4 / §9.5.5）
--
-- 设计：白名单 URL 缓存（不开放代理）
--   - resource_rules: 一条 rule 配一个 upstream（github / playwright / huggingface / terraform / 自定义）
--   - resource_cache_entries: 命中的缓存条目
-- 客户端 URL 改前缀到 CNCacheHub: /r/<rule_name>/<path>
--   → CNCacheHub 拼成 <upstream_url>/<path>，fetch + 落盘缓存

CREATE TABLE IF NOT EXISTS resource_rules (
  id                 INTEGER PRIMARY KEY AUTOINCREMENT,
  name               TEXT    NOT NULL UNIQUE,             -- 形如 'github-release' / 'hf-qwen' / 'playwright-browsers'
  kind               TEXT    NOT NULL,                     -- 'github' | 'playwright' | 'huggingface' | 'terraform' | 'custom'
  upstream_url       TEXT    NOT NULL,                     -- 上游地址（无尾斜杠）
  path_pattern       TEXT    NOT NULL DEFAULT '*',         -- glob 匹配 path（P2#1）；默认匹配所有
  default_ttl_seconds INTEGER NOT NULL DEFAULT 86400,      -- 24h 默认 TTL
  enabled            INTEGER NOT NULL DEFAULT 1,
  description        TEXT    NOT NULL DEFAULT '',
  created_at         INTEGER NOT NULL,
  updated_at         INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_resource_rules_enabled ON resource_rules(enabled);

-- 缓存条目（resource 用法）
CREATE TABLE IF NOT EXISTS resource_cache_entries (
  id              INTEGER PRIMARY KEY AUTOINCREMENT,
  rule_id         INTEGER NOT NULL,
  path            TEXT    NOT NULL,                  -- 形如 'microsoft/vscode/releases/download/1.85.0/...'
  size_bytes      INTEGER NOT NULL DEFAULT 0,
  hit_count       INTEGER NOT NULL DEFAULT 0,
  last_access_at  INTEGER NOT NULL DEFAULT 0,
  expires_at      INTEGER NOT NULL DEFAULT 0,        -- unix 秒；0 = 永不过期
  content_type    TEXT    NOT NULL DEFAULT '',
  storage_path    TEXT    NOT NULL DEFAULT '',        -- 磁盘路径（cache.Store 落盘点）
  created_at      INTEGER NOT NULL,
  updated_at      INTEGER NOT NULL,
  FOREIGN KEY (rule_id) REFERENCES resource_rules(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_resource_cache_rule_id ON resource_cache_entries(rule_id);
CREATE INDEX IF NOT EXISTS idx_resource_cache_path ON resource_cache_entries(rule_id, path);
CREATE UNIQUE INDEX IF NOT EXISTS uniq_resource_cache_rule_path ON resource_cache_entries(rule_id, path);

-- 4 个内置规则（覆盖 PRD §9.4.2-9.4.5）
INSERT OR IGNORE INTO resource_rules (name, kind, upstream_url, default_ttl_seconds, description, created_at, updated_at)
VALUES
  ('github-release',   'github',      'https://github.com',                604800, 'GitHub release 附件 / raw / codeload', strftime('%s','now'), strftime('%s','now')),
  ('huggingface',      'huggingface', 'https://huggingface.co',            604800, 'HF 模型 / datasets / tokenizer',       strftime('%s','now'), strftime('%s','now')),
  ('playwright',       'playwright',  'https://playwright.azureedge.net',  604800, 'Playwright 浏览器二进制',              strftime('%s','now'), strftime('%s','now')),
  ('terraform',        'terraform',   'https://releases.hashicorp.com',    604800, 'Terraform / Vault / 其他 HashiCorp',   strftime('%s','now'), strftime('%s','now'));
