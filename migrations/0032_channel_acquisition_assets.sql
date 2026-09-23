-- Owner: internal/channel. EER owns execution; Channel owns the frozen
-- business reference and provider artifact. No external customer identity is
-- stored here. Forward-only; assets are retired, never hard deleted.

ALTER TABLE external_effects DROP CONSTRAINT IF EXISTS external_effects_owner_kind_shape;
ALTER TABLE external_effects DROP CONSTRAINT IF EXISTS external_effects_kind_check;
ALTER TABLE external_effects ADD CONSTRAINT external_effects_kind_check CHECK (kind IN (
  'outbound_message','outbound_media','wecom_tag_catalog','group_message','channel_acquisition_asset',
  'wechat_pay_prepay_v1','wechat_pay_refund_v1','wechat_shop_refund_v1'
));
ALTER TABLE external_effects ADD CONSTRAINT external_effects_owner_kind_shape CHECK (
  (owner='outbound' AND kind IN ('outbound_message','outbound_media','wecom_tag_catalog','group_message','channel_acquisition_asset')) OR
  (owner='payment' AND kind IN ('wechat_pay_prepay_v1','wechat_pay_refund_v1','wechat_shop_refund_v1'))
);

CREATE TABLE channel_acquisition_assets (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    channel_id BIGINT NOT NULL REFERENCES channels(id) ON DELETE RESTRICT,
    config_version BIGINT NOT NULL,
    asset_version BIGINT NOT NULL CHECK (asset_version > 0),
    kind TEXT NOT NULL CHECK (kind IN ('contact_way_qrcode','customer_acquisition_link')),
    operation TEXT NOT NULL DEFAULT 'create' CHECK (operation IN ('create','update','delete')),
    supersedes_asset_id BIGINT REFERENCES channel_acquisition_assets(id) ON DELETE RESTRICT,
    target_provider_asset_ref TEXT NOT NULL DEFAULT '' CHECK (target_provider_asset_ref=btrim(target_provider_asset_ref) AND char_length(target_provider_asset_ref)<=500),
    source_ref_digest TEXT NOT NULL UNIQUE CHECK (source_ref_digest ~ '^sha256:[0-9a-f]{64}$'),
    operation_key_digest BYTEA NOT NULL CHECK (octet_length(operation_key_digest)=32),
    request_digest BYTEA NOT NULL CHECK (octet_length(request_digest)=32),
    effect_ref TEXT NOT NULL UNIQUE CHECK (effect_ref ~ '^eer_[1-9][0-9]*$'),
    accept_receipt_ref TEXT NOT NULL CHECK (accept_receipt_ref ~ '^eerop_[1-9][0-9]*$'),
    queue_receipt_ref TEXT NOT NULL CHECK (queue_receipt_ref ~ '^eerop_[1-9][0-9]*$'),
    state TEXT NOT NULL CHECK (state IN ('accepted','queued','attempted','executed','outcome_unknown','final_failed','reconciled')),
    provider_asset_ref TEXT NOT NULL DEFAULT '' CHECK (provider_asset_ref=btrim(provider_asset_ref) AND char_length(provider_asset_ref)<=500),
    result_url TEXT NOT NULL DEFAULT '' CHECK (char_length(result_url)<=10000),
    result_digest TEXT NOT NULL DEFAULT '' CHECK (result_digest='' OR result_digest ~ '^sha256:[0-9a-f]{64}$'),
    retired_at TIMESTAMPTZ,
    created_by BIGINT NOT NULL REFERENCES admin_users(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE(channel_id, kind, asset_version),
    UNIQUE(created_by, operation_key_digest),
    FOREIGN KEY(channel_id, config_version) REFERENCES channel_config_versions(channel_id, config_version) ON DELETE RESTRICT,
    CHECK ((operation='create' AND supersedes_asset_id IS NULL AND target_provider_asset_ref='') OR (operation IN ('update','delete') AND supersedes_asset_id IS NOT NULL AND target_provider_asset_ref<>'')),
    CHECK ((state IN ('executed','reconciled') AND operation<>'delete' AND provider_asset_ref<>'' AND result_url<>'') OR (state IN ('executed','reconciled') AND operation='delete' AND provider_asset_ref='' AND result_url='') OR state NOT IN ('executed','reconciled'))
);
CREATE INDEX channel_acquisition_assets_channel_idx ON channel_acquisition_assets(channel_id,id DESC);

CREATE TABLE channel_asset_reconciliation_receipts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    asset_id BIGINT NOT NULL REFERENCES channel_acquisition_assets(id) ON DELETE RESTRICT,
    actor_admin_user_id BIGINT NOT NULL REFERENCES admin_users(id) ON DELETE RESTRICT,
    operation_key_digest BYTEA NOT NULL CHECK(octet_length(operation_key_digest)=32),
    resolution TEXT NOT NULL CHECK(resolution IN ('provider_applied','provider_not_applied')),
    evidence_digest TEXT NOT NULL CHECK(evidence_digest ~ '^sha256:[0-9a-f]{64}$'),
    prior_state TEXT NOT NULL CHECK(prior_state='outcome_unknown'),
    resulting_state TEXT NOT NULL CHECK(resulting_state IN ('reconciled','final_failed')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE(actor_admin_user_id,operation_key_digest)
);

CREATE FUNCTION channel_asset_guard() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
  IF TG_OP IN ('DELETE','TRUNCATE') THEN RAISE EXCEPTION 'channel assets are durable facts'; END IF;
  IF NEW.channel_id IS DISTINCT FROM OLD.channel_id OR NEW.config_version IS DISTINCT FROM OLD.config_version OR NEW.asset_version IS DISTINCT FROM OLD.asset_version OR NEW.kind IS DISTINCT FROM OLD.kind OR NEW.operation IS DISTINCT FROM OLD.operation OR NEW.supersedes_asset_id IS DISTINCT FROM OLD.supersedes_asset_id OR NEW.target_provider_asset_ref IS DISTINCT FROM OLD.target_provider_asset_ref OR NEW.source_ref_digest IS DISTINCT FROM OLD.source_ref_digest OR NEW.effect_ref IS DISTINCT FROM OLD.effect_ref OR NEW.created_by IS DISTINCT FROM OLD.created_by OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN RAISE EXCEPTION 'channel asset identity is immutable'; END IF;
  RETURN NEW;
END; $$;
CREATE TRIGGER channel_acquisition_assets_guard BEFORE UPDATE OR DELETE ON channel_acquisition_assets FOR EACH ROW EXECUTE FUNCTION channel_asset_guard();
CREATE TRIGGER channel_acquisition_assets_no_truncate BEFORE TRUNCATE ON channel_acquisition_assets FOR EACH STATEMENT EXECUTE FUNCTION channel_asset_guard();
CREATE TRIGGER channel_asset_reconciliations_immutable BEFORE UPDATE OR DELETE ON channel_asset_reconciliation_receipts FOR EACH ROW EXECUTE FUNCTION channel_history_immutable_guard();
