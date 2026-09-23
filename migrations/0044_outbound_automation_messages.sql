-- Owner: Outbound. External provider identifiers and message bodies are never persisted here.
ALTER TABLE external_effects DROP CONSTRAINT IF EXISTS external_effects_owner_kind_shape;
ALTER TABLE external_effects DROP CONSTRAINT IF EXISTS external_effects_kind_check;
ALTER TABLE external_effects ADD CONSTRAINT external_effects_kind_check CHECK (kind IN (
  'outbound_message','automation_message','outbound_media','wecom_tag_catalog','group_message','channel_acquisition_asset','channel_welcome_message','channel_entry_tag','channel_acquisition_link_mutation',
  'wechat_pay_prepay_v1','wechat_pay_refund_v1','wechat_shop_refund_v1'
));
ALTER TABLE external_effects ADD CONSTRAINT external_effects_owner_kind_shape CHECK (
  (owner='outbound' AND kind IN ('outbound_message','automation_message','outbound_media','wecom_tag_catalog','group_message','channel_acquisition_asset','channel_welcome_message','channel_entry_tag','channel_acquisition_link_mutation')) OR
  (owner='payment' AND kind IN ('wechat_pay_prepay_v1','wechat_pay_refund_v1','wechat_shop_refund_v1'))
);

CREATE TABLE outbound_message_intents (
  id BIGSERIAL PRIMARY KEY,
  source_kind TEXT NOT NULL CHECK(source_kind IN ('automation_run','automation_enrollment')),
  source_id BIGINT NOT NULL CHECK(source_id>0),
  run_recipient_id BIGINT NOT NULL CHECK(run_recipient_id>0),
  customer_id BIGINT NOT NULL CHECK(customer_id>0),
  sender_staff_id BIGINT NOT NULL CHECK(sender_staff_id>0),
  agent_id BIGINT NOT NULL CHECK(agent_id>0),
  agent_published_version BIGINT NOT NULL CHECK(agent_published_version>0),
  content_reference TEXT NOT NULL CHECK(length(content_reference) BETWEEN 1 AND 200),
  source_digest BYTEA NOT NULL CHECK(octet_length(source_digest)=32),
  target_digest BYTEA NOT NULL CHECK(octet_length(target_digest)=32),
  payload_digest BYTEA NOT NULL CHECK(octet_length(payload_digest)=32),
  policy_digest BYTEA NOT NULL CHECK(octet_length(policy_digest)=32),
  receipt_key_digest BYTEA NOT NULL CHECK(octet_length(receipt_key_digest)=32),
  intent_digest BYTEA NOT NULL CHECK(octet_length(intent_digest)=32),
  envelope_fingerprint TEXT NOT NULL UNIQUE CHECK(length(envelope_fingerprint)=71),
  effect_id TEXT UNIQUE CHECK(effect_id IS NULL OR effect_id ~ '^eer_[1-9][0-9]*$'),
  queue_receipt_id TEXT CHECK(queue_receipt_id IS NULL OR length(queue_receipt_id)<=200),
  state TEXT NOT NULL CHECK(state IN ('accepted','queued','attempted','provider_accepted','delivery_proven','retryable_failed','final_failed','outcome_unknown','reconciled','cancelled')),
  attempt_count INTEGER NOT NULL DEFAULT 0 CHECK(attempt_count>=0),
  receipt_digest BYTEA CHECK(receipt_digest IS NULL OR octet_length(receipt_digest)=32),
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  UNIQUE(source_kind,source_id,run_recipient_id),
  UNIQUE(receipt_key_digest)
);
CREATE INDEX outbound_message_intents_effect_idx ON outbound_message_intents(effect_id);
CREATE TABLE outbound_message_audit_events (
  id BIGSERIAL PRIMARY KEY, message_intent_id BIGINT NOT NULL REFERENCES outbound_message_intents(id) ON DELETE RESTRICT,
  operation TEXT NOT NULL, payload_digest BYTEA NOT NULL CHECK(octet_length(payload_digest)=32), occurred_at TIMESTAMPTZ NOT NULL
);
CREATE TABLE outbound_message_outbox (
  id BIGSERIAL PRIMARY KEY, event_type TEXT NOT NULL, message_intent_id BIGINT NOT NULL REFERENCES outbound_message_intents(id) ON DELETE RESTRICT,
  payload JSONB NOT NULL CHECK(jsonb_typeof(payload)='object'), idempotency_digest BYTEA NOT NULL CHECK(octet_length(idempotency_digest)=32), occurred_at TIMESTAMPTZ NOT NULL,
  UNIQUE(event_type,idempotency_digest)
);
CREATE OR REPLACE FUNCTION outbound_message_append_only() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'outbound message evidence is append-only'; END $$;
CREATE TRIGGER outbound_message_audit_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON outbound_message_audit_events FOR EACH STATEMENT EXECUTE FUNCTION outbound_message_append_only();
CREATE TRIGGER outbound_message_outbox_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON outbound_message_outbox FOR EACH STATEMENT EXECUTE FUNCTION outbound_message_append_only();
