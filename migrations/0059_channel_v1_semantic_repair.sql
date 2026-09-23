-- Owner: internal/channel and cmd/migrate-channel-history.
-- V1 rows remain provenance-only. Legacy assets are never executable and
-- cannot create an External Effect or a provider write.

ALTER TABLE channel_history_source_maps DROP CONSTRAINT channel_history_source_maps_target_kind_check;
ALTER TABLE channel_history_source_maps ADD CONSTRAINT channel_history_source_maps_target_kind_check
CHECK (target_kind IN ('','channel','contact','assignee','effect','config','legacy_asset','material','tag'));

CREATE TABLE channel_semantic_repair_runs (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    import_run_id BIGINT NOT NULL REFERENCES channel_history_import_runs(id) ON DELETE RESTRICT,
    state TEXT NOT NULL CHECK(state IN ('validating','blocked','repaired','activated','failed')),
    source_config_count BIGINT NOT NULL DEFAULT 0,
    repaired_config_count BIGINT NOT NULL DEFAULT 0,
    conflict_count BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    completed_at TIMESTAMPTZ,
    UNIQUE(import_run_id)
);

CREATE TABLE channel_semantic_repair_conflicts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    repair_run_id BIGINT NOT NULL REFERENCES channel_semantic_repair_runs(id) ON DELETE RESTRICT,
    channel_id BIGINT NOT NULL REFERENCES channels(id) ON DELETE RESTRICT,
    field_name TEXT NOT NULL CHECK(field_name=btrim(field_name) AND char_length(field_name) BETWEEN 1 AND 100),
    source_digest BYTEA NOT NULL CHECK(octet_length(source_digest)=32),
    current_digest BYTEA NOT NULL CHECK(octet_length(current_digest)=32),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE(repair_run_id,channel_id,field_name)
);

CREATE TABLE channel_semantic_repaired_configs (
    repair_run_id BIGINT NOT NULL REFERENCES channel_semantic_repair_runs(id) ON DELETE RESTRICT,
    channel_id BIGINT NOT NULL REFERENCES channels(id) ON DELETE RESTRICT,
    config_version BIGINT NOT NULL,
    desired_status TEXT NOT NULL CHECK(desired_status IN ('active','inactive','archived')),
    blockers JSONB NOT NULL DEFAULT '[]'::jsonb CHECK(jsonb_typeof(blockers)='array'),
    activated_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY(repair_run_id,channel_id),
    FOREIGN KEY(channel_id,config_version) REFERENCES channel_config_versions(channel_id,config_version) ON DELETE RESTRICT
);

CREATE TABLE channel_legacy_acquisition_assets (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    import_run_id BIGINT NOT NULL REFERENCES channel_history_import_runs(id) ON DELETE RESTRICT,
    source_asset_id BIGINT NOT NULL CHECK(source_asset_id > 0),
    channel_id BIGINT NOT NULL REFERENCES channels(id) ON DELETE RESTRICT,
    config_version BIGINT NOT NULL,
    asset_version BIGINT NOT NULL CHECK(asset_version > 0),
    kind TEXT NOT NULL CHECK(kind IN ('contact_way_qrcode','customer_acquisition_link')),
    provider_asset_ref TEXT NOT NULL DEFAULT '' CHECK(provider_asset_ref=btrim(provider_asset_ref) AND char_length(provider_asset_ref)<=500),
    result_url TEXT NOT NULL DEFAULT '' CHECK(char_length(result_url)<=10000),
    source_status TEXT NOT NULL CHECK(source_status=btrim(source_status) AND char_length(source_status) BETWEEN 1 AND 100),
    verification_status TEXT NOT NULL DEFAULT 'legacy_unverified' CHECK(verification_status IN ('legacy_unverified','legacy_verified_active','legacy_stale')),
    source_digest BYTEA NOT NULL CHECK(octet_length(source_digest)=32),
    provider_readback_digest TEXT NOT NULL DEFAULT '' CHECK(provider_readback_digest='' OR provider_readback_digest ~ '^sha256:[0-9a-f]{64}$'),
    verified_at TIMESTAMPTZ,
    retired_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE(import_run_id,source_asset_id),
    UNIQUE(channel_id,kind,asset_version),
    FOREIGN KEY(channel_id,config_version) REFERENCES channel_config_versions(channel_id,config_version) ON DELETE RESTRICT,
    CHECK((verification_status='legacy_verified_active' AND provider_asset_ref<>'' AND result_url<>'' AND verified_at IS NOT NULL AND retired_at IS NULL) OR verification_status<>'legacy_verified_active')
);
CREATE INDEX channel_legacy_assets_read_idx ON channel_legacy_acquisition_assets(channel_id,kind,verification_status,asset_version DESC);

CREATE TABLE channel_legacy_material_maps (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    import_run_id BIGINT NOT NULL REFERENCES channel_history_import_runs(id) ON DELETE RESTRICT,
    source_table TEXT NOT NULL CHECK(source_table IN ('image_library','miniprogram_library','attachment_library','group_invite_library')),
    source_id BIGINT NOT NULL CHECK(source_id > 0),
    media_kind TEXT NOT NULL CHECK(media_kind IN ('image','miniprogram','attachment','group_invite')),
    media_id BIGINT CHECK(media_id IS NULL OR media_id > 0),
    source_digest BYTEA NOT NULL CHECK(octet_length(source_digest)=32),
    state TEXT NOT NULL CHECK(state IN ('mapped','unresolved','invalid')),
    reason_code TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE(import_run_id,source_table,source_id)
);

CREATE TABLE channel_legacy_tag_maps (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    import_run_id BIGINT NOT NULL REFERENCES channel_history_import_runs(id) ON DELETE RESTRICT,
    provider_tag_id_digest BYTEA NOT NULL CHECK(octet_length(provider_tag_id_digest)=32),
    tag_id BIGINT,
    name_snapshot TEXT NOT NULL DEFAULT '' CHECK(char_length(name_snapshot)<=200),
    group_name_snapshot TEXT NOT NULL DEFAULT '' CHECK(char_length(group_name_snapshot)<=200),
    state TEXT NOT NULL CHECK(state IN ('mapped','unresolved','deleted')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE(import_run_id,provider_tag_id_digest)
);

CREATE TABLE channel_history_contact_reconciliations (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    history_contact_id BIGINT NOT NULL REFERENCES channel_history_contacts(id) ON DELETE RESTRICT,
    prior_customer_id BIGINT,
    customer_id BIGINT NOT NULL REFERENCES customers(id) ON DELETE RESTRICT,
    evidence_digest BYTEA NOT NULL CHECK(octet_length(evidence_digest)=32),
    reconciled_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE(history_contact_id,customer_id)
);

CREATE FUNCTION channel_semantic_fact_guard() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
  IF TG_OP IN ('DELETE','TRUNCATE') THEN RAISE EXCEPTION 'channel semantic facts are durable'; END IF;
  RETURN NEW;
END; $$;
CREATE TRIGGER channel_legacy_assets_no_delete BEFORE DELETE ON channel_legacy_acquisition_assets FOR EACH ROW EXECUTE FUNCTION channel_semantic_fact_guard();
CREATE TRIGGER channel_legacy_assets_no_truncate BEFORE TRUNCATE ON channel_legacy_acquisition_assets FOR EACH STATEMENT EXECUTE FUNCTION channel_semantic_fact_guard();
CREATE TRIGGER channel_history_contact_reconciliations_immutable BEFORE UPDATE OR DELETE ON channel_history_contact_reconciliations FOR EACH ROW EXECUTE FUNCTION channel_history_immutable_guard();
CREATE TRIGGER channel_history_contact_reconciliations_no_truncate BEFORE TRUNCATE ON channel_history_contact_reconciliations FOR EACH STATEMENT EXECUTE FUNCTION channel_history_immutable_guard();
