-- Owner: internal/payment. Distribution sees only Payment's stable port and
-- opaque instruction/receiver references; it never accesses these tables.
-- Every accepted receiver, split or unfreeze reuses the existing EER runtime.

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
  'wechat_pay_prepay_v1','wechat_pay_refund_v1','wechat_shop_refund_v1',
  'wechat_pay_profit_sharing_receiver_v1','wechat_pay_profit_sharing_order_v1','wechat_pay_profit_sharing_unfreeze_v1'
));
ALTER TABLE external_effects ADD CONSTRAINT external_effects_owner_kind_shape CHECK (
  (owner='outbound' AND kind IN (
    'outbound_message','automation_message','outbound_media','wecom_tag_catalog','wecom_tag_catalog_mutation','group_message',
    'channel_acquisition_asset','channel_welcome_message','channel_entry_tag',
    'channel_acquisition_link_mutation','sidebar_jssdk_send','survey_completion',
    'customer_owner_handoff','customer_tag_command','commerce_product_push'
  )) OR
  (owner='payment' AND kind IN (
    'wechat_pay_prepay_v1','wechat_pay_refund_v1','wechat_shop_refund_v1',
    'wechat_pay_profit_sharing_receiver_v1','wechat_pay_profit_sharing_order_v1','wechat_pay_profit_sharing_unfreeze_v1'
  )) OR
  (owner='automation' AND kind='ai_agent_generate')
);

-- Frozen at native checkout before prepay. Historical paid orders remain
-- false, so they cannot acquire a split instruction retrospectively.
ALTER TABLE payments ADD COLUMN profit_sharing_marked BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE payments ADD COLUMN provider_transaction_reference TEXT
  CHECK(provider_transaction_reference IS NULL OR (length(provider_transaction_reference) BETWEEN 1 AND 200 AND btrim(provider_transaction_reference)=provider_transaction_reference));

CREATE TABLE payment_profit_sharing_receivers (
  id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  customer_id BIGINT NOT NULL CHECK(customer_id > 0),
  identity_id BIGINT NOT NULL CHECK(identity_id > 0),
  app_id TEXT NOT NULL CHECK(length(btrim(app_id)) BETWEEN 1 AND 160),
  app_scope TEXT NOT NULL CHECK(length(btrim(app_scope)) BETWEEN 1 AND 240),
  channel TEXT NOT NULL CHECK(channel IN ('mini_program','h5_official_account')),
  account_digest TEXT NOT NULL CHECK(account_digest ~ '^sha256:[0-9a-f]{64}$'),
  state TEXT NOT NULL CHECK(state IN ('accepted','ready','outcome_unknown','final_failed')),
  external_effect_id BIGINT UNIQUE REFERENCES external_effects(id) ON DELETE RESTRICT,
  version BIGINT NOT NULL CHECK(version > 0),
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  UNIQUE(customer_id, app_id),
  CHECK(updated_at >= created_at)
);
CREATE UNIQUE INDEX payment_profit_sharing_receivers_identity_scope_unique
  ON payment_profit_sharing_receivers(identity_id, app_scope);

CREATE TABLE payment_profit_sharing_instructions (
  id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  settlement_ref TEXT NOT NULL UNIQUE CHECK(length(btrim(settlement_ref)) BETWEEN 8 AND 200),
  payment_id BIGINT NOT NULL REFERENCES payments(id) ON DELETE RESTRICT,
  receiver_id BIGINT NOT NULL REFERENCES payment_profit_sharing_receivers(id) ON DELETE RESTRICT,
  provider_order_no TEXT NOT NULL UNIQUE CHECK(provider_order_no ~ '^v3ps_[A-Z2-7]{20,40}$'),
  idempotency_key_digest TEXT NOT NULL UNIQUE CHECK(idempotency_key_digest ~ '^sha256:[0-9a-f]{64}$'),
  source_ref_digest TEXT NOT NULL CHECK(source_ref_digest ~ '^sha256:[0-9a-f]{64}$'),
  payload_digest TEXT NOT NULL CHECK(payload_digest ~ '^sha256:[0-9a-f]{64}$'),
  policy_version_hash TEXT NOT NULL CHECK(policy_version_hash ~ '^sha256:[0-9a-f]{64}$'),
  amount_minor BIGINT NOT NULL CHECK(amount_minor > 0),
  currency TEXT NOT NULL CHECK(currency='CNY'),
  state TEXT NOT NULL CHECK(state IN ('accepted','settling','outcome_unknown','paid','cancelled','exception')),
  external_effect_id BIGINT UNIQUE REFERENCES external_effects(id) ON DELETE RESTRICT,
  deadline_at TIMESTAMPTZ NOT NULL,
  receiver_confirmed_success BOOLEAN NOT NULL DEFAULT false,
  outcome_known BOOLEAN NOT NULL DEFAULT false,
  version BIGINT NOT NULL CHECK(version > 0),
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  CHECK(updated_at >= created_at),
  CHECK((state='paid') = (receiver_confirmed_success AND outcome_known)),
  CHECK(state NOT IN ('paid','cancelled') OR outcome_known)
);
CREATE UNIQUE INDEX payment_profit_sharing_open_receiver_once
  ON payment_profit_sharing_instructions(payment_id, receiver_id)
  WHERE state IN ('accepted','settling','outcome_unknown');

CREATE TABLE payment_profit_sharing_reserves (
  instruction_id BIGINT PRIMARY KEY REFERENCES payment_profit_sharing_instructions(id) ON DELETE RESTRICT,
  payment_id BIGINT NOT NULL REFERENCES payments(id) ON DELETE RESTRICT,
  amount_minor BIGINT NOT NULL CHECK(amount_minor > 0),
  state TEXT NOT NULL CHECK(state IN ('reserved','released')),
  released_reason TEXT CHECK(released_reason IS NULL OR length(btrim(released_reason)) BETWEEN 1 AND 500),
  created_at TIMESTAMPTZ NOT NULL,
  released_at TIMESTAMPTZ,
  CHECK((state='released') = (released_at IS NOT NULL))
);
CREATE INDEX payment_profit_sharing_reserves_payment_open_idx
  ON payment_profit_sharing_reserves(payment_id) WHERE state='reserved';

CREATE TABLE payment_profit_sharing_unfreezes (
  id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  payment_id BIGINT NOT NULL UNIQUE REFERENCES payments(id) ON DELETE RESTRICT,
  provider_order_no TEXT NOT NULL UNIQUE CHECK(provider_order_no ~ '^v3psu_[A-Z2-7]{20,40}$'),
  reason TEXT NOT NULL CHECK(length(btrim(reason)) BETWEEN 1 AND 500),
  idempotency_key_digest TEXT NOT NULL UNIQUE CHECK(idempotency_key_digest ~ '^sha256:[0-9a-f]{64}$'),
  source_ref_digest TEXT NOT NULL CHECK(source_ref_digest ~ '^sha256:[0-9a-f]{64}$'),
  payload_digest TEXT NOT NULL CHECK(payload_digest ~ '^sha256:[0-9a-f]{64}$'),
  policy_version_hash TEXT NOT NULL CHECK(policy_version_hash ~ '^sha256:[0-9a-f]{64}$'),
  state TEXT NOT NULL CHECK(state IN ('accepted','outcome_unknown','succeeded','exception')),
  external_effect_id BIGINT UNIQUE REFERENCES external_effects(id) ON DELETE RESTRICT,
  outcome_known BOOLEAN NOT NULL DEFAULT false,
  version BIGINT NOT NULL CHECK(version > 0),
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  CHECK(updated_at >= created_at),
  CHECK((state='succeeded') = outcome_known)
);

CREATE TABLE payment_profit_sharing_provider_intents (
  id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  receiver_id BIGINT REFERENCES payment_profit_sharing_receivers(id) ON DELETE RESTRICT,
  instruction_id BIGINT REFERENCES payment_profit_sharing_instructions(id) ON DELETE RESTRICT,
	unfreeze_id BIGINT REFERENCES payment_profit_sharing_unfreezes(id) ON DELETE RESTRICT,
  effect_kind TEXT NOT NULL CHECK(effect_kind IN ('wechat_pay_profit_sharing_receiver_v1','wechat_pay_profit_sharing_order_v1','wechat_pay_profit_sharing_unfreeze_v1')),
  source_ref_digest TEXT NOT NULL CHECK(source_ref_digest ~ '^sha256:[0-9a-f]{64}$'),
  target_ref_digest TEXT NOT NULL CHECK(target_ref_digest ~ '^sha256:[0-9a-f]{64}$'),
  payload_digest TEXT NOT NULL CHECK(payload_digest ~ '^sha256:[0-9a-f]{64}$'),
  policy_version_hash TEXT NOT NULL CHECK(policy_version_hash ~ '^sha256:[0-9a-f]{64}$'),
  request_snapshot JSONB NOT NULL CHECK(jsonb_typeof(request_snapshot)='object'),
  created_at TIMESTAMPTZ NOT NULL,
  CHECK((receiver_id IS NOT NULL)::int + (instruction_id IS NOT NULL)::int + (unfreeze_id IS NOT NULL)::int = 1),
  UNIQUE(effect_kind, source_ref_digest)
);

CREATE TABLE payment_profit_sharing_audit_events (
  id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  aggregate_kind TEXT NOT NULL CHECK(aggregate_kind IN ('receiver','instruction')),
  aggregate_id BIGINT NOT NULL CHECK(aggregate_id > 0),
  event_type TEXT NOT NULL CHECK(event_type ~ '^payment[.]profit_sharing[.][a-z_]+$'),
  actor_scope TEXT NOT NULL CHECK(length(btrim(actor_scope)) BETWEEN 1 AND 240),
  payload JSONB NOT NULL CHECK(jsonb_typeof(payload)='object'),
  occurred_at TIMESTAMPTZ NOT NULL
);
CREATE FUNCTION payment_profit_sharing_facts_immutable() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'payment profit sharing facts are append only'; END $$;
CREATE TRIGGER payment_profit_sharing_audit_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON payment_profit_sharing_audit_events FOR EACH STATEMENT EXECUTE FUNCTION payment_profit_sharing_facts_immutable();
