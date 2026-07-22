-- 0009_registries_seed.sql
-- 多 Registry 代理的 seed（PRD §9.2.2）。
--
-- 设计：每个 upstream 有 mirror_path 字段（之前 0002 已建表 + 索引）。
-- 客户端按 path 访问：
--   /v2/<repo>/manifests/<ref>            → dockerhub（向后兼容）
--   /v2/dockerhub/<repo>/manifests/<ref>  → dockerhub（显式）
--   /v2/ghcr/<repo>/manifests/<ref>       → ghcr
--   /v2/quay/<repo>/manifests/<ref>       → quay
--   /v2/k8s/<repo>/manifests/<ref>        → registry.k8s.io
--
-- 注意：dockerhub 的 mirror_path 留空（默认 /v2/*）向后兼容；
-- 其他三个的 mirror_path 加 `/<name>` 前缀。

INSERT OR IGNORE INTO registry_upstreams (name, upstream_url, mirror_path, enabled, created_at)
VALUES
    ('ghcr', 'https://ghcr.io',                '/v2/ghcr',  1, strftime('%s', 'now')),
    ('quay', 'https://quay.io',                '/v2/quay',  1, strftime('%s', 'now')),
    ('k8s',  'https://registry.k8s.io',        '/v2/k8s',   1, strftime('%s', 'now'));

-- dockerhub 的 mirror_path 留空（让 proxy 知道这是默认 upstream）
UPDATE registry_upstreams
   SET mirror_path = ''
 WHERE name = 'dockerhub' AND mirror_path = '/v2';
