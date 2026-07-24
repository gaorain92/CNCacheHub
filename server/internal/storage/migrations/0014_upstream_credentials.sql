-- 0014_upstream_credentials.sql — PRD §9.7.3 上游凭据管理
--
-- 加 3 列：username（明文，不算 secret，只是登录名）/ password_enc（加密）/ token_enc（加密）
-- 默认 NULL = 没设置该凭据。
-- 加密：AES-256-GCM，nonce(12) + ciphertext + tag(16)，key 来自 data_dir/.master_key
-- 启动时如果 master key 文件不存在就生成一个 (chmod 0600)。

ALTER TABLE registry_upstreams ADD COLUMN username    TEXT    NOT NULL DEFAULT '';
ALTER TABLE registry_upstreams ADD COLUMN password_enc BLOB;
ALTER TABLE registry_upstreams ADD COLUMN token_enc    BLOB;
