-- 0016_huggingface_models.sql — HuggingFace 模型下载规则
--
-- 跟现有 'huggingface' rule（datasets / tokenizer / raw）区分：
-- 'huggingface_models' 专门给模型权重（GB 级别）下载
--   - 透传 Range 头支持断点续传
--   - 如果 system_settings.huggingface_token 已设置，注入 Authorization: Bearer
--   - 默认 disabled（需要 user 主动启用 + 配 token）
--
-- 客户端用法:
--   curl -L "http://cncachehub/r/huggingface-models/<owner>/<repo>/resolve/<rev>/<filename>"
--
-- 例子:
--   curl -L "http://cncachehub/r/huggingface-models/bert-base-uncased/resolve/main/config.json"

INSERT OR IGNORE INTO resource_rules
  (name, kind, upstream_url, path_pattern, default_ttl_seconds, enabled, description, created_at, updated_at)
VALUES
  ('huggingface-models',
   'huggingface_models',
   'https://huggingface.co',
   '*',
   604800,             -- 7 天（模型版本可能不频繁改）
   0,                  -- 默认关闭：需要 user 显式启用 + 在设置页配 hf_token
   'HuggingFace 模型权重（GB 级别）— 走 LFS CDN + Range 断点续传；可选 token 注入（gated 模型）',
   strftime('%s', 'now'),
   strftime('%s', 'now'));
