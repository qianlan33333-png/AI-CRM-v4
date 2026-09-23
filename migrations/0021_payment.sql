-- Owners: internal/payment and a constraint extension owned by
-- internal/externaleffects. Payment/Refund effects reuse EER; no second queue.
ALTER TABLE external_effects DROP CONSTRAINT external_effects_owner_check;
ALTER TABLE external_effects ADD CONSTRAINT external_effects_owner_check CHECK (owner IN ('outbound','payment'));
ALTER TABLE external_effects DROP CONSTRAINT external_effects_kind_check;
ALTER TABLE external_effects ADD CONSTRAINT external_effects_kind_check CHECK (kind IN (
  'outbound_message','outbound_media','wecom_tag_catalog','group_message',
  'wechat_pay_prepay_v1','wechat_pay_refund_v1','wechat_shop_refund_v1'
));
ALTER TABLE external_effects ADD CONSTRAINT external_effects_owner_kind_shape CHECK (
  (owner='outbound' AND kind IN ('outbound_message','outbound_media','wecom_tag_catalog','group_message')) OR
  (owner='payment' AND kind IN ('wechat_pay_prepay_v1','wechat_pay_refund_v1','wechat_shop_refund_v1'))
);

CREATE TABLE payments (
  id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  order_id BIGINT NOT NULL UNIQUE REFERENCES orders(id) ON DELETE RESTRICT,
  provider TEXT NOT NULL CHECK (provider IN ('wechat_pay','wechat_shop')),
  merchant_order_no TEXT NOT NULL CHECK (length(merchant_order_no) BETWEEN 1 AND 200),
  payer_identity_id BIGINT NOT NULL CHECK (payer_identity_id>0),
  payer_customer_id BIGINT NOT NULL CHECK (payer_customer_id>0),
  beneficiary_customer_id BIGINT NOT NULL CHECK (beneficiary_customer_id>0),
  amount_minor BIGINT NOT NULL CHECK (amount_minor>0), currency TEXT NOT NULL CHECK (currency='CNY'),
  status TEXT NOT NULL CHECK (status IN ('awaiting_prepay','awaiting_payment','paid','failed','cancelled')),
  external_effect_id BIGINT UNIQUE REFERENCES external_effects(id) ON DELETE RESTRICT,
  provider_transaction_digest TEXT CHECK (provider_transaction_digest IS NULL OR provider_transaction_digest ~ '^sha256:[0-9a-f]{64}$'),
  version BIGINT NOT NULL CHECK(version>0), created_at TIMESTAMPTZ NOT NULL, updated_at TIMESTAMPTZ NOT NULL,
  CHECK(updated_at>=created_at), UNIQUE(provider,merchant_order_no)
);
CREATE INDEX payments_customer_idx ON payments(beneficiary_customer_id,created_at DESC,id DESC);

CREATE TABLE payment_refunds (
  id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  payment_id BIGINT NOT NULL REFERENCES payments(id) ON DELETE RESTRICT,
  provider TEXT NOT NULL CHECK(provider IN ('wechat_pay','wechat_shop')),
  refund_no TEXT NOT NULL UNIQUE CHECK(length(refund_no) BETWEEN 1 AND 200),
  amount_minor BIGINT NOT NULL CHECK(amount_minor>0), reason TEXT NOT NULL CHECK(length(reason) BETWEEN 1 AND 500),
  status TEXT NOT NULL CHECK(status IN ('requested','effect_accepted','outcome_unknown','completed','final_failed')),
  external_effect_id BIGINT UNIQUE REFERENCES external_effects(id) ON DELETE RESTRICT,
  provider_refund_digest TEXT CHECK(provider_refund_digest IS NULL OR provider_refund_digest ~ '^sha256:[0-9a-f]{64}$'),
  version BIGINT NOT NULL CHECK(version>0), created_at TIMESTAMPTZ NOT NULL, updated_at TIMESTAMPTZ NOT NULL,
  UNIQUE(payment_id,refund_no), CHECK(updated_at>=created_at)
);

CREATE TABLE payment_operation_receipts (
  id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  operation TEXT NOT NULL CHECK(operation IN ('create','refund','callback','reconcile','history_import')),
  actor_scope TEXT NOT NULL CHECK(length(actor_scope) BETWEEN 1 AND 200),
  key_digest BYTEA NOT NULL CHECK(octet_length(key_digest)=32), payload_digest BYTEA NOT NULL CHECK(octet_length(payload_digest)=32),
  result_kind TEXT NOT NULL CHECK(result_kind IN ('payment','refund','callback','reconcile','history')),
  result_id BIGINT NOT NULL CHECK(result_id>0), created_at TIMESTAMPTZ NOT NULL,
  UNIQUE(operation,actor_scope,key_digest)
);
CREATE TABLE payment_provider_intents (
  id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  payment_id BIGINT REFERENCES payments(id) ON DELETE RESTRICT,
  refund_id BIGINT REFERENCES payment_refunds(id) ON DELETE RESTRICT,
  effect_kind TEXT NOT NULL CHECK(effect_kind IN ('wechat_pay_prepay_v1','wechat_pay_refund_v1','wechat_shop_refund_v1')),
  source_ref_digest TEXT NOT NULL CHECK(source_ref_digest ~ '^sha256:[0-9a-f]{64}$'),
  target_ref_digest TEXT NOT NULL CHECK(target_ref_digest ~ '^sha256:[0-9a-f]{64}$'),
  payload_digest TEXT NOT NULL CHECK(payload_digest ~ '^sha256:[0-9a-f]{64}$'),
  policy_version_hash TEXT NOT NULL CHECK(policy_version_hash ~ '^sha256:[0-9a-f]{64}$'),
  request_snapshot JSONB NOT NULL CHECK(jsonb_typeof(request_snapshot)='object'), created_at TIMESTAMPTZ NOT NULL,
  CHECK((payment_id IS NOT NULL)::int+(refund_id IS NOT NULL)::int=1), UNIQUE(effect_kind,source_ref_digest)
);
CREATE TABLE payment_callback_receipts (
  id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY, provider TEXT NOT NULL CHECK(provider IN ('wechat_pay','wechat_shop')),
  event_digest BYTEA NOT NULL CHECK(octet_length(event_digest)=32), body_digest BYTEA NOT NULL CHECK(octet_length(body_digest)=32),
  signature_verified BOOLEAN NOT NULL CHECK(signature_verified), outcome TEXT NOT NULL CHECK(outcome IN ('settled','replayed','conflict','query_required')),
  payment_id BIGINT REFERENCES payments(id) ON DELETE RESTRICT, refund_id BIGINT REFERENCES payment_refunds(id) ON DELETE RESTRICT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(), UNIQUE(provider,event_digest)
);
CREATE TABLE payment_reconciliations (
  id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY, payment_id BIGINT REFERENCES payments(id) ON DELETE RESTRICT,
  refund_id BIGINT REFERENCES payment_refunds(id) ON DELETE RESTRICT, evidence_digest BYTEA NOT NULL CHECK(octet_length(evidence_digest)=32),
  outcome TEXT NOT NULL CHECK(outcome IN ('paid','refunded','not_found','pending','final_failed')),
  created_at TIMESTAMPTZ NOT NULL, CHECK((payment_id IS NOT NULL)::int+(refund_id IS NOT NULL)::int=1)
);
CREATE TABLE payment_sessions (
  id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY, token_digest BYTEA NOT NULL UNIQUE CHECK(octet_length(token_digest)=32),
  payer_identity_id BIGINT NOT NULL CHECK(payer_identity_id>0), payer_customer_id BIGINT NOT NULL CHECK(payer_customer_id>0),
  beneficiary_customer_id BIGINT NOT NULL CHECK(beneficiary_customer_id>0), app_scope_digest BYTEA NOT NULL CHECK(octet_length(app_scope_digest)=32),
  expires_at TIMESTAMPTZ NOT NULL, consumed_at TIMESTAMPTZ, created_at TIMESTAMPTZ NOT NULL, CHECK(expires_at>created_at)
);
CREATE TABLE payment_handoffs (
  id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  payment_id BIGINT NOT NULL UNIQUE REFERENCES payments(id) ON DELETE RESTRICT,
  effect_id BIGINT NOT NULL UNIQUE REFERENCES external_effects(id) ON DELETE RESTRICT,
  payload JSONB NOT NULL CHECK(jsonb_typeof(payload)='object'),
  payload_digest TEXT NOT NULL CHECK(payload_digest ~ '^sha256:[0-9a-f]{64}$'),
  expires_at TIMESTAMPTZ NOT NULL, created_at TIMESTAMPTZ NOT NULL,
  CHECK(expires_at>created_at)
);
CREATE TABLE payment_shop_materials (
  id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY, order_id BIGINT NOT NULL REFERENCES orders(id) ON DELETE RESTRICT,
  provider_order_digest TEXT NOT NULL CHECK(provider_order_digest ~ '^sha256:[0-9a-f]{64}$'), snapshot JSONB NOT NULL CHECK(jsonb_typeof(snapshot)='object'),
  snapshot_digest BYTEA NOT NULL CHECK(octet_length(snapshot_digest)=32), fetched_at TIMESTAMPTZ NOT NULL, UNIQUE(order_id,provider_order_digest)
);
CREATE TABLE payment_audit_events (id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,event_type TEXT NOT NULL CHECK(event_type ~ '^payment[.][a-z_]+$'),aggregate_id BIGINT NOT NULL CHECK(aggregate_id>0),actor_scope TEXT NOT NULL,payload JSONB NOT NULL CHECK(jsonb_typeof(payload)='object'),occurred_at TIMESTAMPTZ NOT NULL);
CREATE TABLE payment_outbox (id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,event_type TEXT NOT NULL CHECK(event_type ~ '^payment[.][a-z_]+$'),idempotency_key TEXT NOT NULL UNIQUE,aggregate_id BIGINT NOT NULL,payload JSONB NOT NULL CHECK(jsonb_typeof(payload)='object'),occurred_at TIMESTAMPTZ NOT NULL,published_at TIMESTAMPTZ);

CREATE TRIGGER payment_receipts_no_delete BEFORE UPDATE OR DELETE OR TRUNCATE ON payment_operation_receipts FOR EACH STATEMENT EXECUTE FUNCTION order_immutable_facts_reject_mutation();
CREATE TRIGGER payment_callbacks_no_delete BEFORE UPDATE OR DELETE OR TRUNCATE ON payment_callback_receipts FOR EACH STATEMENT EXECUTE FUNCTION order_immutable_facts_reject_mutation();
CREATE TRIGGER payment_audit_no_delete BEFORE UPDATE OR DELETE OR TRUNCATE ON payment_audit_events FOR EACH STATEMENT EXECUTE FUNCTION order_immutable_facts_reject_mutation();
