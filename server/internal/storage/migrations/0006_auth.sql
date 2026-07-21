-- 0006_auth.sql
-- 用户、登录会话、审计日志（PRD §9.7.1 控制台安全）。
--
-- 设计：
--   * users：唯一 username；password_hash 用 bcrypt；is_admin 标记管理员；
--     must_change_password 首次初始化后强制改密（占位字段，phase 2 启用）；
--   * sessions：token 字符串主键（32+ 字节随机），关联 user_id，expires_at 自动过期；
--   * audit_logs：基础审计（登录/登出/改密/配置变更），保留 90 天由 cron 清理（未来 phase）。
--
-- 字段命名沿用复数表名 + snake_case columns（与之前 migrations 一致）。

CREATE TABLE IF NOT EXISTS users (
    id                    INTEGER PRIMARY KEY AUTOINCREMENT,
    username              TEXT    NOT NULL UNIQUE,
    password_hash         TEXT    NOT NULL,
    is_admin              INTEGER NOT NULL DEFAULT 0,
    must_change_password  INTEGER NOT NULL DEFAULT 0,
    created_at            INTEGER NOT NULL,
    last_login_at         INTEGER NOT NULL DEFAULT 0,
    disabled              INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS sessions (
    token       TEXT    PRIMARY KEY,                       -- 32 字节 base64url 随机串
    user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at  INTEGER NOT NULL,
    expires_at  INTEGER NOT NULL,                          -- unix 秒
    last_seen_at INTEGER NOT NULL DEFAULT 0,
    ip          TEXT    NOT NULL DEFAULT '',
    user_agent  TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_sessions_expires ON sessions(expires_at);

CREATE TABLE IF NOT EXISTS audit_logs (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id    INTEGER,                                     -- nullable for anonymous events
    action     TEXT    NOT NULL,                            -- 'login'/'logout'/'change_password'/'init'/'cache_delete'/...
    resource   TEXT    NOT NULL DEFAULT '',                 -- e.g. 'user:1' / 'cache_entry:8'
    ip         TEXT    NOT NULL DEFAULT '',
    user_agent TEXT    NOT NULL DEFAULT '',
    status     TEXT    NOT NULL DEFAULT 'ok',              -- 'ok' / 'fail'
    details    TEXT    NOT NULL DEFAULT '',                 -- json blob (best-effort)
    created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_audit_logs_user ON audit_logs(user_id);
CREATE INDEX IF NOT EXISTS idx_audit_logs_created ON audit_logs(created_at);
