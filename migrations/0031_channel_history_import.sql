-- Owner: internal/channel and cmd/migrate-channel-history.
-- Retention: source snapshot lineage, per-row outcomes, historical channel
-- contacts, staff snapshots and provider facts are append-only audit evidence.
-- Forward-only: rollback is a guarded data operation scoped to one import run;
-- the schema itself is not dropped from a deployed environment.

CREATE TABLE channel_history_import_runs (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    snapshot_id TEXT NOT NULL UNIQUE CHECK (snapshot_id = btrim(snapshot_id) AND char_length(snapshot_id) BETWEEN 8 AND 200),
    source_host_digest BYTEA NOT NULL CHECK (octet_length(source_host_digest) = 32),
    snapshot_timestamp TIMESTAMPTZ NOT NULL,
    manifest_digest BYTEA NOT NULL CHECK (octet_length(manifest_digest) = 32),
    state TEXT NOT NULL CHECK (state IN ('validated','importing','completed','reconciled','rolled_back','failed')),
    imported_count BIGINT NOT NULL DEFAULT 0 CHECK (imported_count >= 0),
    unresolved_count BIGINT NOT NULL DEFAULT 0 CHECK (unresolved_count >= 0),
    quarantined_count BIGINT NOT NULL DEFAULT 0 CHECK (quarantined_count >= 0),
    invalid_count BIGINT NOT NULL DEFAULT 0 CHECK (invalid_count >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    completed_at TIMESTAMPTZ,
    CONSTRAINT channel_history_import_runs_completion CHECK ((state IN ('completed','reconciled','rolled_back') AND completed_at IS NOT NULL) OR (state NOT IN ('completed','reconciled','rolled_back')))
);

CREATE TABLE channel_history_source_maps (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    import_run_id BIGINT NOT NULL REFERENCES channel_history_import_runs(id) ON DELETE RESTRICT,
    source_table TEXT NOT NULL CHECK (source_table = btrim(source_table) AND char_length(source_table) BETWEEN 1 AND 200),
    source_pk TEXT NOT NULL CHECK (source_pk = btrim(source_pk) AND char_length(source_pk) BETWEEN 1 AND 500),
    source_digest BYTEA NOT NULL CHECK (octet_length(source_digest) = 32),
    outcome TEXT NOT NULL CHECK (outcome IN ('imported','already_imported','unresolved','quarantined','invalid')),
    target_kind TEXT NOT NULL DEFAULT '' CHECK (target_kind IN ('','channel','contact','assignee','effect')),
    target_id BIGINT CHECK (target_id IS NULL OR target_id > 0),
    reason_code TEXT NOT NULL DEFAULT '' CHECK (reason_code = btrim(reason_code) AND char_length(reason_code) <= 200),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (import_run_id, source_table, source_pk),
    CONSTRAINT channel_history_source_maps_target CHECK ((outcome IN ('imported','already_imported') AND target_kind <> '' AND target_id IS NOT NULL) OR (outcome NOT IN ('imported','already_imported') AND target_id IS NULL))
);

CREATE TABLE channel_history_contacts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    import_run_id BIGINT NOT NULL REFERENCES channel_history_import_runs(id) ON DELETE RESTRICT,
    channel_id BIGINT NOT NULL REFERENCES channels(id) ON DELETE RESTRICT,
    source_contact_id BIGINT NOT NULL CHECK (source_contact_id > 0),
    customer_id BIGINT REFERENCES customers(id) ON DELETE RESTRICT,
    owner_reference TEXT NOT NULL DEFAULT '' CHECK (owner_reference = btrim(owner_reference) AND char_length(owner_reference) <= 200),
    first_entered_at TIMESTAMPTZ NOT NULL,
    last_entered_at TIMESTAMPTZ NOT NULL,
    enter_count BIGINT NOT NULL CHECK (enter_count > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (import_run_id, channel_id, source_contact_id),
    CHECK (last_entered_at >= first_entered_at AND updated_at >= created_at)
);
CREATE INDEX channel_history_contacts_page_idx ON channel_history_contacts (channel_id, id);

CREATE TABLE channel_history_assignees (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    import_run_id BIGINT NOT NULL REFERENCES channel_history_import_runs(id) ON DELETE RESTRICT,
    channel_id BIGINT NOT NULL REFERENCES channels(id) ON DELETE RESTRICT,
    source_assignee_id BIGINT NOT NULL CHECK (source_assignee_id > 0),
    staff_reference TEXT NOT NULL DEFAULT '' CHECK (staff_reference = btrim(staff_reference) AND char_length(staff_reference) <= 200),
    display_name_snapshot TEXT NOT NULL DEFAULT '' CHECK (display_name_snapshot = btrim(display_name_snapshot) AND char_length(display_name_snapshot) <= 200),
    priority INTEGER NOT NULL CHECK (priority >= 0),
    ratio_percent INTEGER CHECK (ratio_percent BETWEEN 0 AND 100),
    max_scans_24h INTEGER CHECK (max_scans_24h >= 0),
    status TEXT NOT NULL CHECK (status = btrim(status) AND char_length(status) BETWEEN 1 AND 100),
    source_created_at TIMESTAMP(6) WITHOUT TIME ZONE NOT NULL,
    source_updated_at TIMESTAMP(6) WITHOUT TIME ZONE NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (import_run_id, channel_id, source_assignee_id)
);
CREATE INDEX channel_history_assignees_channel_idx ON channel_history_assignees (channel_id, id);

CREATE TABLE channel_history_effects (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    import_run_id BIGINT NOT NULL REFERENCES channel_history_import_runs(id) ON DELETE RESTRICT,
    channel_id BIGINT REFERENCES channels(id) ON DELETE RESTRICT,
    source_effect_id TEXT NOT NULL CHECK (source_effect_id = btrim(source_effect_id) AND char_length(source_effect_id) BETWEEN 1 AND 500),
    effect_kind TEXT NOT NULL CHECK (effect_kind = btrim(effect_kind) AND char_length(effect_kind) BETWEEN 1 AND 100),
    provider_state TEXT NOT NULL CHECK (provider_state = btrim(provider_state) AND char_length(provider_state) BETWEEN 1 AND 100),
    occurred_at TIMESTAMPTZ NOT NULL,
    fact_digest BYTEA NOT NULL CHECK (octet_length(fact_digest) = 32),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (import_run_id, source_effect_id)
);

CREATE FUNCTION channel_history_immutable_guard() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog AS $$
BEGIN
	IF TG_OP = 'DELETE' AND current_setting('aicrm.channel_history_rollback', true) = 'on' THEN
		RETURN OLD;
	END IF;
    RAISE EXCEPTION 'channel history facts are immutable; use guarded import rollback';
END;
$$;

CREATE TRIGGER channel_history_contacts_immutable BEFORE UPDATE OR DELETE ON channel_history_contacts FOR EACH ROW EXECUTE FUNCTION channel_history_immutable_guard();
CREATE TRIGGER channel_history_contacts_no_truncate BEFORE TRUNCATE ON channel_history_contacts FOR EACH STATEMENT EXECUTE FUNCTION channel_history_immutable_guard();
CREATE TRIGGER channel_history_assignees_immutable BEFORE UPDATE OR DELETE ON channel_history_assignees FOR EACH ROW EXECUTE FUNCTION channel_history_immutable_guard();
CREATE TRIGGER channel_history_assignees_no_truncate BEFORE TRUNCATE ON channel_history_assignees FOR EACH STATEMENT EXECUTE FUNCTION channel_history_immutable_guard();
CREATE TRIGGER channel_history_effects_immutable BEFORE UPDATE OR DELETE ON channel_history_effects FOR EACH ROW EXECUTE FUNCTION channel_history_immutable_guard();
CREATE TRIGGER channel_history_effects_no_truncate BEFORE TRUNCATE ON channel_history_effects FOR EACH STATEMENT EXECUTE FUNCTION channel_history_immutable_guard();
CREATE TRIGGER channel_history_source_maps_immutable BEFORE UPDATE OR DELETE ON channel_history_source_maps FOR EACH ROW EXECUTE FUNCTION channel_history_immutable_guard();
CREATE TRIGGER channel_history_source_maps_no_truncate BEFORE TRUNCATE ON channel_history_source_maps FOR EACH STATEMENT EXECUTE FUNCTION channel_history_immutable_guard();
