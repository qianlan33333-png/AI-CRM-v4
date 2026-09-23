-- Owner: Automation. Dynamic text is generated before the existing AI
-- Assistant approval path, so each customer generation has its own immutable
-- effect binding and never reuses an outbound send recipient/effect.
--
-- Extend, rather than replace, the complete current EER registry from 0106.
-- Automation only accepts the provider-neutral generation effect below; all
-- customer delivery remains owned by Outbound after human review.
ALTER TABLE external_effects DROP CONSTRAINT IF EXISTS external_effects_owner_check;
ALTER TABLE external_effects DROP CONSTRAINT IF EXISTS external_effects_owner_kind_shape;
ALTER TABLE external_effects DROP CONSTRAINT IF EXISTS external_effects_kind_check;
ALTER TABLE external_effects ADD CONSTRAINT external_effects_owner_check
  CHECK (owner IN ('outbound','payment','automation'));
ALTER TABLE external_effects ADD CONSTRAINT external_effects_kind_check CHECK (kind IN (
  'outbound_message','automation_message','outbound_media','wecom_tag_catalog','wecom_tag_catalog_mutation','group_message',
  'channel_acquisition_asset','channel_welcome_message','channel_entry_tag',
  'channel_acquisition_link_mutation','sidebar_jssdk_send','survey_completion',
  'customer_owner_handoff','customer_tag_command','commerce_product_push','ai_agent_generate',
  'wechat_pay_prepay_v1','wechat_pay_refund_v1','wechat_shop_refund_v1'
));
ALTER TABLE external_effects ADD CONSTRAINT external_effects_owner_kind_shape CHECK (
  (owner='outbound' AND kind IN (
    'outbound_message','automation_message','outbound_media','wecom_tag_catalog','wecom_tag_catalog_mutation','group_message',
    'channel_acquisition_asset','channel_welcome_message','channel_entry_tag',
    'channel_acquisition_link_mutation','sidebar_jssdk_send','survey_completion',
    'customer_owner_handoff','customer_tag_command','commerce_product_push'
  )) OR
  (owner='payment' AND kind IN ('wechat_pay_prepay_v1','wechat_pay_refund_v1','wechat_shop_refund_v1')) OR
  (owner='automation' AND kind='ai_agent_generate')
);

CREATE TABLE automation_generation_items (
  id BIGSERIAL PRIMARY KEY,
  run_id BIGINT NOT NULL REFERENCES automation_runs(id) ON DELETE RESTRICT,
  customer_id BIGINT NOT NULL CHECK(customer_id > 0),
  sender_staff_id BIGINT NOT NULL CHECK(sender_staff_id > 0),
  agent_id BIGINT NOT NULL CHECK(agent_id > 0),
  agent_published_version BIGINT NOT NULL CHECK(agent_published_version > 0),
  agent_code TEXT NOT NULL CHECK(length(btrim(agent_code)) BETWEEN 1 AND 120),
  role_prompt TEXT NOT NULL CHECK(length(role_prompt) BETWEEN 1 AND 16000),
  task_prompt TEXT NOT NULL CHECK(length(task_prompt) BETWEEN 1 AND 16000),
  context_snapshot JSONB NOT NULL CHECK(jsonb_typeof(context_snapshot) = 'object'),
  model_policy JSONB NOT NULL CHECK(jsonb_typeof(model_policy) = 'object'),
  source_digest BYTEA NOT NULL CHECK(octet_length(source_digest) = 32),
  target_digest BYTEA NOT NULL CHECK(octet_length(target_digest) = 32),
  payload_digest BYTEA NOT NULL CHECK(octet_length(payload_digest) = 32),
  policy_digest BYTEA NOT NULL CHECK(octet_length(policy_digest) = 32),
  receipt_key_digest BYTEA NOT NULL UNIQUE CHECK(octet_length(receipt_key_digest) = 32),
  effect_id TEXT UNIQUE CHECK(effect_id IS NULL OR effect_id ~ '^eer_[1-9][0-9]*$'),
  state TEXT NOT NULL CHECK(state IN ('accepted','queued','executed','retryable_failed','final_failed','outcome_unknown','cancelled')),
  generated_text TEXT CHECK(generated_text IS NULL OR length(generated_text) BETWEEN 1 AND 8000),
  result_digest BYTEA CHECK(result_digest IS NULL OR octet_length(result_digest) = 32),
  failure_code TEXT CHECK(failure_code IS NULL OR failure_code ~ '^[a-z0-9_]{1,80}$'),
  attempt_count INTEGER NOT NULL DEFAULT 0 CHECK(attempt_count >= 0),
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  completed_at TIMESTAMPTZ,
  UNIQUE(run_id, customer_id),
  CHECK((state = 'executed') = (generated_text IS NOT NULL AND completed_at IS NOT NULL)),
  CHECK(state NOT IN ('retryable_failed','final_failed','outcome_unknown','cancelled') OR completed_at IS NOT NULL)
);
CREATE INDEX automation_generation_items_run_page_idx ON automation_generation_items(run_id, id);
CREATE INDEX automation_generation_items_effect_idx ON automation_generation_items(effect_id) WHERE effect_id IS NOT NULL;

CREATE TABLE automation_generation_audit_events (
  id BIGSERIAL PRIMARY KEY,
  generation_item_id BIGINT NOT NULL REFERENCES automation_generation_items(id) ON DELETE RESTRICT,
  operation TEXT NOT NULL CHECK(length(btrim(operation)) BETWEEN 1 AND 80),
  payload_digest BYTEA NOT NULL CHECK(octet_length(payload_digest) = 32),
  occurred_at TIMESTAMPTZ NOT NULL
);
CREATE TRIGGER automation_generation_audit_append_only
  BEFORE UPDATE OR DELETE OR TRUNCATE ON automation_generation_audit_events
  FOR EACH STATEMENT EXECUTE FUNCTION automation_append_only();
