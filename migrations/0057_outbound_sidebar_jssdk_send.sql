-- Owners: internal/externaleffects and internal/outbound.
ALTER TABLE external_effects DROP CONSTRAINT IF EXISTS external_effects_owner_kind_shape;
ALTER TABLE external_effects DROP CONSTRAINT IF EXISTS external_effects_kind_check;
ALTER TABLE external_effects ADD CONSTRAINT external_effects_kind_check CHECK (kind IN (
  'outbound_message','automation_message','outbound_media','wecom_tag_catalog','group_message','channel_acquisition_asset','channel_welcome_message','channel_entry_tag','channel_acquisition_link_mutation','sidebar_jssdk_send',
  'wechat_pay_prepay_v1','wechat_pay_refund_v1','wechat_shop_refund_v1'
));
ALTER TABLE external_effects ADD CONSTRAINT external_effects_owner_kind_shape CHECK (
  (owner='outbound' AND kind IN ('outbound_message','automation_message','outbound_media','wecom_tag_catalog','group_message','channel_acquisition_asset','channel_welcome_message','channel_entry_tag','channel_acquisition_link_mutation','sidebar_jssdk_send')) OR
  (owner='payment' AND kind IN ('wechat_pay_prepay_v1','wechat_pay_refund_v1','wechat_shop_refund_v1'))
);

CREATE TABLE outbound_sidebar_send_intents (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    customer_id BIGINT NOT NULL REFERENCES customers(id) ON DELETE RESTRICT,
    employee_digest BYTEA NOT NULL CHECK (octet_length(employee_digest)=32),
    resource_kind TEXT NOT NULL CHECK (resource_kind IN ('product','material','radar_link')),
    resource_id TEXT NOT NULL CHECK (char_length(resource_id) BETWEEN 1 AND 100),
    content_digest BYTEA NOT NULL CHECK (octet_length(content_digest)=32),
    payload JSONB NOT NULL CHECK (jsonb_typeof(payload)='object'),
    receipt_key_digest BYTEA NOT NULL UNIQUE CHECK (octet_length(receipt_key_digest)=32),
    intent_digest BYTEA NOT NULL CHECK (octet_length(intent_digest)=32),
    effect_id TEXT UNIQUE,
    queue_receipt_id TEXT,
    state TEXT NOT NULL CHECK (state IN ('accepted','queued','client_executed','outcome_unknown','final_failed')),
    delivery_state TEXT NOT NULL DEFAULT 'unknown' CHECK (delivery_state IN ('unknown','reconciled')),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE outbound_sidebar_send_grants (
    intent_id BIGINT PRIMARY KEY REFERENCES outbound_sidebar_send_intents(id) ON DELETE RESTRICT,
    token_digest BYTEA NOT NULL UNIQUE CHECK (octet_length(token_digest)=32),
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    outcome TEXT CHECK (outcome IN ('client_executed','outcome_unknown','final_failed')),
    evidence_digest BYTEA,
    CHECK ((consumed_at IS NULL AND outcome IS NULL AND evidence_digest IS NULL) OR (consumed_at IS NOT NULL AND outcome IS NOT NULL AND octet_length(evidence_digest)=32))
);

CREATE TABLE outbound_sidebar_send_audit_events (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    intent_id BIGINT NOT NULL REFERENCES outbound_sidebar_send_intents(id) ON DELETE RESTRICT,
	operation TEXT NOT NULL CHECK (operation IN ('accept','client_complete','expire')),
    payload_digest BYTEA NOT NULL CHECK (octet_length(payload_digest)=32),
    occurred_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE outbound_sidebar_send_outbox (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
	event_type TEXT NOT NULL CHECK (event_type IN ('outbound.sidebar_send.queued.v1','outbound.sidebar_send.client_completed.v1','outbound.sidebar_send.expired.v1')),
    intent_id BIGINT NOT NULL REFERENCES outbound_sidebar_send_intents(id) ON DELETE RESTRICT,
    payload JSONB NOT NULL CHECK (jsonb_typeof(payload)='object'),
    idempotency_digest BYTEA NOT NULL CHECK (octet_length(idempotency_digest)=32),
    occurred_at TIMESTAMPTZ NOT NULL,
    UNIQUE(event_type,idempotency_digest)
);
