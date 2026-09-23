-- Owner: internal/channel. Standalone customer-acquisition-link compatibility
-- writes are durable Channel intents and are executed only by Outbound/EER.

ALTER TABLE external_effects DROP CONSTRAINT IF EXISTS external_effects_owner_kind_shape;
ALTER TABLE external_effects DROP CONSTRAINT IF EXISTS external_effects_kind_check;
ALTER TABLE external_effects ADD CONSTRAINT external_effects_kind_check CHECK (kind IN (
  'outbound_message','outbound_media','wecom_tag_catalog','group_message','channel_acquisition_asset','channel_welcome_message','channel_entry_tag','channel_acquisition_link_mutation',
  'wechat_pay_prepay_v1','wechat_pay_refund_v1','wechat_shop_refund_v1'
));
ALTER TABLE external_effects ADD CONSTRAINT external_effects_owner_kind_shape CHECK (
  (owner='outbound' AND kind IN ('outbound_message','outbound_media','wecom_tag_catalog','group_message','channel_acquisition_asset','channel_welcome_message','channel_entry_tag','channel_acquisition_link_mutation')) OR
  (owner='payment' AND kind IN ('wechat_pay_prepay_v1','wechat_pay_refund_v1','wechat_shop_refund_v1'))
);

CREATE TABLE channel_acquisition_link_receipts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    actor_admin_user_id BIGINT NOT NULL REFERENCES admin_users(id) ON DELETE RESTRICT,
    operation_key_digest BYTEA NOT NULL CHECK(octet_length(operation_key_digest)=32),
    request_digest BYTEA NOT NULL CHECK(octet_length(request_digest)=32),
    operation TEXT NOT NULL CHECK(operation IN ('create','update','delete')),
    requested_link_id TEXT NOT NULL DEFAULT '' CHECK(requested_link_id=btrim(requested_link_id) AND char_length(requested_link_id)<=1024),
    link_name TEXT NOT NULL DEFAULT '' CHECK(link_name=btrim(link_name) AND char_length(link_name)<=120),
    user_ids TEXT[] NOT NULL DEFAULT '{}',
    department_ids BIGINT[] NOT NULL DEFAULT '{}',
    skip_verify BOOLEAN NOT NULL DEFAULT FALSE,
    source_ref_digest TEXT NOT NULL UNIQUE CHECK(source_ref_digest ~ '^sha256:[0-9a-f]{64}$'),
    effect_ref TEXT NOT NULL UNIQUE CHECK(effect_ref ~ '^eer_[1-9][0-9]*$'),
    accept_receipt_ref TEXT NOT NULL CHECK(accept_receipt_ref ~ '^eerop_[1-9][0-9]*$'),
    queue_receipt_ref TEXT NOT NULL CHECK(queue_receipt_ref ~ '^eerop_[1-9][0-9]*$'),
    state TEXT NOT NULL CHECK(state IN ('accepted','attempted','executed','outcome_unknown','final_failed','reconciled')),
    result_link_id TEXT NOT NULL DEFAULT '' CHECK(result_link_id=btrim(result_link_id) AND char_length(result_link_id)<=1024),
    result_url TEXT NOT NULL DEFAULT '' CHECK(char_length(result_url)<=10000),
    outcome_digest TEXT NOT NULL DEFAULT '' CHECK(outcome_digest='' OR outcome_digest ~ '^sha256:[0-9a-f]{64}$'),
    business_endpoint_dispatched BOOLEAN NOT NULL DEFAULT FALSE,
    real_external_call_executed BOOLEAN NOT NULL DEFAULT FALSE,
    resolution TEXT CHECK(resolution IS NULL OR resolution IN ('provider_applied','provider_not_applied')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE(actor_admin_user_id,operation_key_digest),
    CHECK((operation='create' AND requested_link_id='') OR (operation IN ('update','delete') AND requested_link_id<>'')),
    CHECK(cardinality(user_ids)<=500 AND cardinality(department_ids)<=500),
    CHECK((operation='delete' AND link_name='' AND cardinality(user_ids)=0 AND cardinality(department_ids)=0) OR operation<>'delete')
);
CREATE INDEX channel_acquisition_link_receipts_link_idx ON channel_acquisition_link_receipts(COALESCE(NULLIF(result_link_id,''),requested_link_id),id DESC);

CREATE TABLE channel_acquisition_link_reconciliations (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    receipt_id BIGINT NOT NULL REFERENCES channel_acquisition_link_receipts(id) ON DELETE RESTRICT,
    actor_admin_user_id BIGINT NOT NULL REFERENCES admin_users(id) ON DELETE RESTRICT,
    operation_key_digest BYTEA NOT NULL CHECK(octet_length(operation_key_digest)=32),
    request_digest BYTEA NOT NULL CHECK(octet_length(request_digest)=32),
    resolution TEXT NOT NULL CHECK(resolution IN ('provider_applied','provider_not_applied')),
    evidence_digest TEXT NOT NULL CHECK(evidence_digest ~ '^sha256:[0-9a-f]{64}$'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE(actor_admin_user_id,operation_key_digest)
);

CREATE FUNCTION channel_link_receipt_guard() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
  IF TG_OP IN ('DELETE','TRUNCATE') OR NEW.actor_admin_user_id IS DISTINCT FROM OLD.actor_admin_user_id OR NEW.operation_key_digest IS DISTINCT FROM OLD.operation_key_digest OR NEW.request_digest IS DISTINCT FROM OLD.request_digest OR NEW.operation IS DISTINCT FROM OLD.operation OR NEW.requested_link_id IS DISTINCT FROM OLD.requested_link_id OR NEW.link_name IS DISTINCT FROM OLD.link_name OR NEW.user_ids IS DISTINCT FROM OLD.user_ids OR NEW.department_ids IS DISTINCT FROM OLD.department_ids OR NEW.skip_verify IS DISTINCT FROM OLD.skip_verify OR NEW.source_ref_digest IS DISTINCT FROM OLD.source_ref_digest OR NEW.effect_ref IS DISTINCT FROM OLD.effect_ref OR NEW.accept_receipt_ref IS DISTINCT FROM OLD.accept_receipt_ref OR NEW.queue_receipt_ref IS DISTINCT FROM OLD.queue_receipt_ref OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN RAISE EXCEPTION 'channel acquisition link receipt identity is immutable'; END IF;
  RETURN NEW;
END; $$;
CREATE TRIGGER channel_acquisition_link_receipts_guard BEFORE UPDATE OR DELETE ON channel_acquisition_link_receipts FOR EACH ROW EXECUTE FUNCTION channel_link_receipt_guard();
CREATE TRIGGER channel_acquisition_link_receipts_no_truncate BEFORE TRUNCATE ON channel_acquisition_link_receipts FOR EACH STATEMENT EXECUTE FUNCTION channel_link_receipt_guard();
CREATE TRIGGER channel_acquisition_link_reconciliations_immutable BEFORE UPDATE OR DELETE ON channel_acquisition_link_reconciliations FOR EACH ROW EXECUTE FUNCTION channel_history_immutable_guard();
