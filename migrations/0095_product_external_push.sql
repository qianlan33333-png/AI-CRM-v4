-- Owners: internal/order, internal/product, internal/outbound and
-- internal/externaleffects.  This is the forward-only bridge from a native
-- first-paid Order fact to one controlled commerce webhook effect.  Raw
-- identity values and signing material never enter EER or these audit rows.

-- 0010 predates configuration revisions. Existing rows are release-compatible
-- at revision 1; Product increments this value on each later write.
ALTER TABLE product_external_push_configurations
    ADD COLUMN IF NOT EXISTS version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0);


-- Product owns the non-sensitive business fields from the frozen commerce
-- configuration. The protected runtime target remains responsible only for
-- endpoint, signing material and identity selectors.
ALTER TABLE product_external_push_configurations
    ADD COLUMN IF NOT EXISTS push_type TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS day BIGINT NULL,
    ADD COLUMN IF NOT EXISTS frequency BIGINT NULL,
    ADD COLUMN IF NOT EXISTS expires_at_ts BIGINT NULL,
    ADD COLUMN IF NOT EXISTS remark TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS custom_params JSONB NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE product_external_push_configurations
    ADD CONSTRAINT product_external_push_business_shape CHECK (
      push_type = btrim(push_type) AND char_length(push_type) <= 200
      AND remark = btrim(remark) AND char_length(remark) <= 2000
      AND (day IS NULL OR day >= 0)
      AND (frequency IS NULL OR frequency >= 0)
      AND (expires_at_ts IS NULL OR expires_at_ts >= 0)
      AND jsonb_typeof(custom_params) = 'object'
      AND pg_column_size(custom_params) <= 32768
    );

-- Old 0010 intentionally admitted only one local placeholder test per
-- configuration digest. A real explicit test operation is keyed by its
-- Product receipt instead, so an administrator can make a second deliberate
-- test without configuration churn. Replaying the same receipt remains one
-- operation.
ALTER TABLE product_external_push_tests
    DROP CONSTRAINT IF EXISTS product_external_push_tests_configuration_unique;
CREATE UNIQUE INDEX IF NOT EXISTS product_external_push_tests_receipt_unique
    ON product_external_push_tests(receipt_id);

-- The original check accepted order.paid but not the versioned durable event.
ALTER TABLE order_outbox
    DROP CONSTRAINT IF EXISTS order_outbox_event_type_check;
ALTER TABLE order_outbox
    ADD CONSTRAINT order_outbox_event_type_check
    CHECK (event_type ~ '^order[.][a-z_]+([.]v[0-9]+)?$');

CREATE TABLE order_paid_events (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    order_id BIGINT NOT NULL REFERENCES orders(id) ON DELETE RESTRICT,
    order_version BIGINT NOT NULL CHECK (order_version > 1),
    source_digest BYTEA NOT NULL CHECK (octet_length(source_digest)=32),
    occurred_at TIMESTAMPTZ NOT NULL,
    UNIQUE(order_id),
    UNIQUE(order_id, order_version)
);
CREATE INDEX order_paid_events_occurred_idx ON order_paid_events(occurred_at, id);

-- Preserve the complete existing EER union from 0093 before adding the sole
-- commerce-push kind. Do not narrow customer owner-handoff/tag commands or
-- prior payment/outbound kinds while this independent migration is applied.
ALTER TABLE external_effects DROP CONSTRAINT IF EXISTS external_effects_owner_kind_shape;
ALTER TABLE external_effects DROP CONSTRAINT IF EXISTS external_effects_kind_check;
ALTER TABLE external_effects ADD CONSTRAINT external_effects_kind_check CHECK (kind IN (
  'outbound_message','automation_message','outbound_media','wecom_tag_catalog','group_message',
  'channel_acquisition_asset','channel_welcome_message','channel_entry_tag',
  'channel_acquisition_link_mutation','sidebar_jssdk_send','survey_completion',
  'customer_owner_handoff','customer_tag_command','commerce_product_push',
  'wechat_pay_prepay_v1','wechat_pay_refund_v1','wechat_shop_refund_v1'
));
ALTER TABLE external_effects ADD CONSTRAINT external_effects_owner_kind_shape CHECK (
  (owner='outbound' AND kind IN (
    'outbound_message','automation_message','outbound_media','wecom_tag_catalog','group_message',
    'channel_acquisition_asset','channel_welcome_message','channel_entry_tag',
    'channel_acquisition_link_mutation','sidebar_jssdk_send','survey_completion',
    'customer_owner_handoff','customer_tag_command','commerce_product_push'
  )) OR
  (owner='payment' AND kind IN ('wechat_pay_prepay_v1','wechat_pay_refund_v1','wechat_shop_refund_v1'))
);

-- Outbound owns the durable intention, encrypted exact body and completion
-- projection. source_reference + target_slot is the logical send identity;
-- target configuration revisions/digests therefore cannot mint a second paid
-- delivery after the first acceptance.
CREATE TABLE outbound_commerce_push_intents (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    source_kind TEXT NOT NULL CHECK (source_kind IN ('order_paid','synthetic_test','history_paid')),
    source_reference TEXT NOT NULL CHECK (source_reference = btrim(source_reference) AND char_length(source_reference) BETWEEN 1 AND 200 AND source_reference !~ '[[:cntrl:]]'),
    order_paid_event_id BIGINT NULL REFERENCES order_paid_events(id) ON DELETE RESTRICT,
    product_id BIGINT NOT NULL CHECK (product_id > 0),
    product_kind TEXT NOT NULL CHECK (product_kind IN ('wechat_pay','service_period')),
    target_reference TEXT NOT NULL CHECK (target_reference = btrim(target_reference) AND char_length(target_reference) BETWEEN 1 AND 128 AND target_reference !~ '[[:cntrl:]]'),
    target_slot TEXT NOT NULL CHECK (target_slot = btrim(target_slot) AND char_length(target_slot) BETWEEN 1 AND 128 AND target_slot !~ '[[:cntrl:]]'),
    -- A Product with no saved configuration is a valid paid-order input. Its
    -- disabled intent freezes revision 0 rather than making settlement depend
    -- on an administrator first creating an external-push row.
    product_configuration_revision BIGINT NOT NULL CHECK (product_configuration_revision >= 0),
    source_digest BYTEA NOT NULL CHECK (octet_length(source_digest)=32),
    target_digest BYTEA NOT NULL CHECK (octet_length(target_digest)=32),
    payload_digest BYTEA NOT NULL CHECK (octet_length(payload_digest)=32),
    policy_digest BYTEA NOT NULL CHECK (octet_length(policy_digest)=32),
    receipt_key_digest BYTEA NOT NULL UNIQUE CHECK (octet_length(receipt_key_digest)=32),
    intent_digest BYTEA NOT NULL CHECK (octet_length(intent_digest)=32),
    envelope_fingerprint TEXT NULL UNIQUE CHECK (envelope_fingerprint IS NULL OR envelope_fingerprint ~ '^sha256:[0-9a-f]{64}$'),
    payload_ciphertext BYTEA NULL,
    payload_key_version SMALLINT NULL CHECK (payload_key_version IN (1)),
    effect_id TEXT NULL UNIQUE CHECK (effect_id IS NULL OR effect_id ~ '^eer_[1-9][0-9]*$'),
    queue_receipt_id TEXT NULL CHECK (queue_receipt_id IS NULL OR queue_receipt_id ~ '^eerop_[1-9][0-9]*$'),
    state TEXT NOT NULL CHECK (state IN ('planned_disabled','planned_config_expired','planned_target_unavailable','planned_identity_unavailable','planned_payload_protection_unavailable','accepted','queued','attempted','provider_accepted','final_failed','outcome_unknown','reconciled')),
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    provider_call_attempted BOOLEAN NOT NULL DEFAULT FALSE,
    provider_real_call_executed BOOLEAN NOT NULL DEFAULT FALSE,
    provider_result_received BOOLEAN NULL,
    -- P06 safe response projection: never persist a Provider body or dynamic
    -- error. The fixed code plus status only distinguish a response from an
    -- unknown transport outcome on the legacy order-detail page.
    provider_response_status INTEGER NULL CHECK (provider_response_status BETWEEN 100 AND 599),
    provider_result_code TEXT NULL CHECK (provider_result_code IN ('provider_accepted','provider_rejected','response_unknown')),
    CONSTRAINT outbound_commerce_push_response_shape CHECK (
      (provider_response_status IS NULL AND provider_result_code IS NULL)
      OR (provider_response_status IS NOT NULL AND provider_result_code IS NOT NULL)
    ),
    receipt_digest BYTEA NULL CHECK (receipt_digest IS NULL OR octet_length(receipt_digest)=32),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    UNIQUE(source_reference, target_slot),
    UNIQUE(order_paid_event_id, target_slot),
    CONSTRAINT outbound_commerce_push_source_shape CHECK (
      (source_kind='order_paid' AND order_paid_event_id IS NOT NULL)
      OR (source_kind IN ('synthetic_test','history_paid') AND order_paid_event_id IS NULL)
    ),
    CONSTRAINT outbound_commerce_push_payload_shape CHECK (
      (state IN ('planned_disabled','planned_config_expired','planned_target_unavailable','planned_identity_unavailable','planned_payload_protection_unavailable') AND payload_ciphertext IS NULL AND payload_key_version IS NULL AND effect_id IS NULL AND queue_receipt_id IS NULL AND envelope_fingerprint IS NULL)
      OR (state NOT IN ('planned_disabled','planned_config_expired','planned_target_unavailable','planned_identity_unavailable','planned_payload_protection_unavailable') AND payload_ciphertext IS NOT NULL AND payload_key_version IS NOT NULL AND effect_id IS NOT NULL AND queue_receipt_id IS NOT NULL AND envelope_fingerprint IS NOT NULL)
    ),
    CONSTRAINT outbound_commerce_push_call_shape CHECK (NOT provider_real_call_executed OR provider_call_attempted)
);
CREATE INDEX outbound_commerce_push_effect_idx ON outbound_commerce_push_intents(effect_id);
CREATE INDEX outbound_commerce_push_timeline_idx ON outbound_commerce_push_intents(product_id, created_at DESC, id DESC);

CREATE TABLE outbound_commerce_push_audit_events (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    intent_id BIGINT NOT NULL REFERENCES outbound_commerce_push_intents(id) ON DELETE RESTRICT,
    operation TEXT NOT NULL CHECK (operation IN ('planned','accepted','completed','reconciled')),
    payload_digest BYTEA NOT NULL CHECK (octet_length(payload_digest)=32),
    occurred_at TIMESTAMPTZ NOT NULL
);
CREATE TABLE outbound_commerce_push_outbox (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    event_type TEXT NOT NULL CHECK (event_type IN ('outbound.commerce_push.planned.v1','outbound.commerce_push.queued.v1','outbound.commerce_push.completed.v1')),
    intent_id BIGINT NOT NULL REFERENCES outbound_commerce_push_intents(id) ON DELETE RESTRICT,
    payload JSONB NOT NULL CHECK (jsonb_typeof(payload)='object'),
    idempotency_digest BYTEA NOT NULL CHECK (octet_length(idempotency_digest)=32),
    occurred_at TIMESTAMPTZ NOT NULL,
    UNIQUE(event_type, idempotency_digest)
);
CREATE OR REPLACE FUNCTION outbound_commerce_push_append_only() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'outbound commerce push evidence is append-only'; END;
$$;
CREATE TRIGGER outbound_commerce_push_audit_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON outbound_commerce_push_audit_events FOR EACH STATEMENT EXECUTE FUNCTION outbound_commerce_push_append_only();
CREATE TRIGGER outbound_commerce_push_outbox_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON outbound_commerce_push_outbox FOR EACH STATEMENT EXECUTE FUNCTION outbound_commerce_push_append_only();

-- The old external-push configuration, delivery, and transaction-paid outbox
-- rows are historical evidence only. Outbound owns this ledger because it is
-- the sole owner of current commerce delivery intents. Importing a frozen V2
-- record never creates an intent, an External Effect, an Order event, or a
-- River job.
CREATE TABLE outbound_commerce_push_history_batches (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    source_system TEXT NOT NULL CHECK (source_system = btrim(source_system) AND char_length(source_system) BETWEEN 1 AND 160),
    source_revision TEXT NOT NULL CHECK (source_revision ~ '^[a-f0-9]{40}$'),
    manifest_digest BYTEA NOT NULL CHECK (octet_length(manifest_digest)=32),
    snapshot_at TIMESTAMPTZ NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('applied','reconciled')),
    input_count INTEGER NOT NULL CHECK (input_count >= 0),
    imported_count INTEGER NOT NULL CHECK (imported_count >= 0),
    pending_count INTEGER NOT NULL CHECK (pending_count >= 0),
    excluded_count INTEGER NOT NULL CHECK (excluded_count >= 0),
    applied_at TIMESTAMPTZ NOT NULL,
    reconciled_at TIMESTAMPTZ NULL,
    -- A source revision is a Git/code revision, not a snapshot identity. A
    -- later protected snapshot of the same donor revision is allowed; its
    -- manifest is the replay receipt.
    UNIQUE(source_system, manifest_digest),
    CONSTRAINT outbound_commerce_push_history_batch_conservation CHECK (input_count=imported_count+pending_count+excluded_count)
);

-- Each V2 row has one global immutable history record. Snapshot batches only
-- reference it, so overlapping extracts cannot silently duplicate a source
-- row; a changed digest for that same source row fails closed.
CREATE TABLE outbound_commerce_push_history_rows (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    source_system TEXT NOT NULL CHECK (source_system = btrim(source_system) AND char_length(source_system) BETWEEN 1 AND 160),
    source_kind TEXT NOT NULL CHECK (source_kind IN ('config','delivery','domain_event_outbox')),
    source_id BIGINT NOT NULL CHECK (source_id > 0),
    source_digest BYTEA NOT NULL CHECK (octet_length(source_digest)=32),
    source_config_id BIGINT NULL CHECK (source_config_id IS NULL OR source_config_id > 0),
    source_delivery_id TEXT NOT NULL DEFAULT '' CHECK (char_length(source_delivery_id) <= 200),
    source_event_type TEXT NOT NULL DEFAULT '' CHECK (char_length(source_event_type) <= 160),
    source_target_type TEXT NOT NULL DEFAULT '' CHECK (char_length(source_target_type) <= 120),
    source_target_id TEXT NOT NULL DEFAULT '' CHECK (char_length(source_target_id) <= 240),
    -- These are V2 source coordinates, never a V3 orders.id. An Order-owned
    -- read Port must match its own source_system/source_key before a history
    -- delivery becomes visible on a V3 order page.
    source_order_kind TEXT NOT NULL DEFAULT '' CHECK (char_length(source_order_kind) <= 80),
    source_order_scope TEXT NOT NULL DEFAULT '' CHECK (char_length(source_order_scope) <= 160),
    source_order_key TEXT NOT NULL DEFAULT '' CHECK (char_length(source_order_key) <= 240),
    source_order_id BIGINT NULL CHECK (source_order_id IS NULL OR source_order_id >= 0),
    source_product_id BIGINT NULL CHECK (source_product_id IS NULL OR source_product_id >= 0),
    source_state TEXT NOT NULL CHECK (char_length(source_state) <= 80),
    source_attempt_count INTEGER NOT NULL CHECK (source_attempt_count >= 0),
    source_effect_job_id BIGINT NULL CHECK (source_effect_job_id IS NULL OR source_effect_job_id > 0),
    source_effect_state TEXT NULL CHECK (source_effect_state IS NULL OR char_length(source_effect_state) <= 80),
    source_response_status INTEGER NULL CHECK (source_response_status BETWEEN 100 AND 599),
    -- This is the legacy admin-facing diagnostic field, bounded but not logged.
    -- Raw request/response bodies stay only in the sealed source snapshot.
    source_error_message TEXT NOT NULL DEFAULT '' CHECK (char_length(source_error_message) <= 2000),
    source_response_body_protected BOOLEAN NOT NULL DEFAULT FALSE,
    source_created_at TIMESTAMPTZ NOT NULL,
    source_updated_at TIMESTAMPTZ NOT NULL,
    target_product_id BIGINT NULL CHECK (target_product_id IS NULL OR target_product_id > 0),
    outcome TEXT NOT NULL CHECK (outcome IN ('imported','pending','excluded')),
    reason_code TEXT NOT NULL CHECK (reason_code ~ '^[a-z0-9_]{1,80}$'),
    read_only BOOLEAN NOT NULL DEFAULT TRUE CHECK (read_only=TRUE),
    UNIQUE(source_system, source_kind, source_id)
);
CREATE INDEX outbound_commerce_push_history_rows_product_idx
    ON outbound_commerce_push_history_rows(target_product_id, source_created_at DESC, id DESC);
CREATE INDEX outbound_commerce_push_history_rows_order_source_idx
    ON outbound_commerce_push_history_rows(source_order_kind, source_order_scope, source_order_key, source_created_at DESC, id DESC)
    WHERE source_kind='delivery';

CREATE TABLE outbound_commerce_push_history_batch_rows (
    batch_id BIGINT NOT NULL REFERENCES outbound_commerce_push_history_batches(id) ON DELETE RESTRICT,
    source_row_id BIGINT NOT NULL REFERENCES outbound_commerce_push_history_rows(id) ON DELETE RESTRICT,
    source_digest BYTEA NOT NULL CHECK (octet_length(source_digest)=32),
    PRIMARY KEY(batch_id, source_row_id)
);
CREATE INDEX outbound_commerce_push_history_batch_rows_source_idx
    ON outbound_commerce_push_history_batch_rows(source_row_id, batch_id);
