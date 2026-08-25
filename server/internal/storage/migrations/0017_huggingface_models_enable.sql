-- 0017_huggingface_models_enable.sql — 默认启用 huggingface-models 规则
--
-- 0016 把它设成 enabled=0（要 user 显式启用 + 配 token 才能下 gated 模型），
-- 但新装的 HF mirror（/hf/...）依赖该 rule 转发到 /r/—— enabled=0 时
-- mirror 直接返 403，UX 差。
--
-- 改为默认 enabled=1：公开模型（Qwen2.5、Mistral 等）直接能用；
-- gated 模型（Llama / Gemma）只需在 Settings 配 huggingface_token 即可。
-- 已有 enabled=0 的 install 会被本 migration 改成 1（让"忘了启用"的人也能用）；
-- 故意禁用的用户可以重新 disable。

UPDATE resource_rules
SET enabled = 1, updated_at = strftime('%s', 'now')
WHERE name = 'huggingface-models' AND enabled = 0;
