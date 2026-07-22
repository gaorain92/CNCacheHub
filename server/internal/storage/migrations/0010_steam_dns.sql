-- 0010_steam_dns.sql — SteamCMD DNS 启动器配置表（PRD §9.3）
--
-- 单行配置（id=1 始终存在），记录 DNS server 运行时参数。
-- domain_rules 用 JSON 数组存白名单，支持 *.example.com 通配符。
-- updated_at 触发器自动维护。

CREATE TABLE IF NOT EXISTS dns_config (
  id           INTEGER PRIMARY KEY CHECK (id = 1),
  enabled      INTEGER NOT NULL DEFAULT 0,
  listen_addr  TEXT    NOT NULL DEFAULT '0.0.0.0:5353',
  upstream     TEXT    NOT NULL DEFAULT '1.1.1.1:53',
  answer_ip    TEXT    NOT NULL DEFAULT '127.0.0.1',
  domain_rules TEXT    NOT NULL DEFAULT '[]',  -- JSON 数组
  created_at   INTEGER NOT NULL,
  updated_at   INTEGER NOT NULL
);

-- 默认规则（LANCache 风格）
-- 通过 INSERT OR IGNORE 落库，应用启动时若表为空自动 seed

INSERT OR IGNORE INTO dns_config (id, enabled, listen_addr, upstream, answer_ip, domain_rules, created_at, updated_at)
VALUES (
  1,
  0,
  '0.0.0.0:5353',
  '1.1.1.1:53',
  '127.0.0.1',
  '["*.steamcontent.com","*.steampipe.steamcontent.com","*.steamserver.net","*.steamstatic.com","content*.steampowered.com","client-download.steampowered.com","*.cs.steampowered.com","*.cm.steampowered.com"]',
  strftime('%s','now'),
  strftime('%s','now')
);
