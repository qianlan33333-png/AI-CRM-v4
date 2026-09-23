-- Owner: internal/groupops.
-- Forward-only correction for the sealed V1 history projection. Historical
-- source staff and chat references are not current Access identifiers: text
-- facts stay as source references and no synthetic bigint is manufactured.

ALTER TABLE group_ops_v1_history_plans
    ALTER COLUMN created_by DROP NOT NULL,
    ALTER COLUMN updated_by DROP NOT NULL,
    ADD COLUMN source_created_by_reference TEXT,
    ADD COLUMN source_updated_by_reference TEXT,
    ADD COLUMN source_owner_reference TEXT,
    ADD CONSTRAINT group_ops_v1_history_plans_source_references CHECK (
        COALESCE(char_length(source_created_by_reference), 0) <= 128
        AND COALESCE(char_length(source_updated_by_reference), 0) <= 128
        AND COALESCE(char_length(source_owner_reference), 0) <= 128
    );

ALTER TABLE group_ops_v1_history_plans
    DROP CONSTRAINT group_ops_v1_history_plans_source_code_check,
    ADD CONSTRAINT group_ops_v1_history_plans_source_code_check CHECK (
        btrim(source_code) = source_code AND char_length(source_code) <= 128
    );

ALTER TABLE group_ops_v1_history_directory
    DROP CONSTRAINT group_ops_v1_history_directory_check,
    ADD COLUMN source_owner_reference TEXT,
    ADD CONSTRAINT group_ops_v1_history_directory_source_owner_reference CHECK (
        COALESCE(char_length(source_owner_reference), 0) <= 128
    ),
    ADD CONSTRAINT group_ops_v1_history_directory_check CHECK (
        (source_kind = 'group_chats' AND member_count IS NOT NULL
            AND internal_member_count IS NULL AND external_member_count IS NULL AND owner_name IS NULL)
        OR (source_kind = 'wecom_group_chat_snapshots' AND member_count IS NULL
            AND internal_member_count IS NOT NULL AND external_member_count IS NOT NULL)
    );

ALTER TABLE group_ops_v1_history_groups
    ADD COLUMN source_owner_reference TEXT,
    ADD CONSTRAINT group_ops_v1_history_groups_source_owner_reference CHECK (
        COALESCE(char_length(source_owner_reference), 0) <= 128
    );

CREATE TABLE group_ops_v1_history_import_batches (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    source_system TEXT NOT NULL CHECK (char_length(source_system) BETWEEN 1 AND 160),
    source_revision TEXT NOT NULL CHECK (source_revision ~ '^[0-9a-f]{40}$'),
    snapshot_digest BYTEA NOT NULL CHECK (octet_length(snapshot_digest) = 32),
    manifest JSONB NOT NULL CHECK (jsonb_typeof(manifest) = 'object'),
    status TEXT NOT NULL CHECK (status IN ('applying','applied','verified')),
    imported_counts JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(imported_counts) = 'object'),
    applied_at TIMESTAMPTZ,
    verified_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (source_system, source_revision),
    UNIQUE (source_system, snapshot_digest),
    CHECK (
        (status = 'applying' AND applied_at IS NULL AND verified_at IS NULL)
        OR (status = 'applied' AND applied_at IS NOT NULL AND verified_at IS NULL)
        OR (status = 'verified' AND applied_at IS NOT NULL AND verified_at IS NOT NULL)
    )
);

CREATE TABLE group_ops_v1_history_import_rows (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    batch_id BIGINT NOT NULL REFERENCES group_ops_v1_history_import_batches(id) ON DELETE RESTRICT,
    source_kind TEXT NOT NULL CHECK (source_kind IN ('plans','directory_group_chats','directory_snapshots','groups','nodes')),
    source_key TEXT NOT NULL CHECK (char_length(source_key) BETWEEN 1 AND 240),
    source_digest BYTEA NOT NULL CHECK (octet_length(source_digest) = 32),
    outcome TEXT NOT NULL CHECK (outcome IN ('imported','quarantined')),
    target_table TEXT CHECK (target_table IN ('group_ops_v1_history_plans','group_ops_v1_history_directory','group_ops_v1_history_groups','group_ops_v1_history_nodes')),
    target_id BIGINT,
    target_digest BYTEA,
    reason_code TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CHECK (
        (outcome = 'imported' AND target_table IS NOT NULL AND target_id IS NOT NULL AND target_digest IS NOT NULL AND octet_length(target_digest) = 32 AND reason_code IS NULL)
        OR (outcome = 'quarantined' AND target_table IS NULL AND target_id IS NULL AND target_digest IS NULL AND reason_code ~ '^[a-z0-9_]{1,80}$')
    ),
    UNIQUE (batch_id, source_kind, source_key)
);
CREATE INDEX group_ops_v1_history_import_rows_batch_idx ON group_ops_v1_history_import_rows(batch_id, source_kind, outcome, id);

CREATE FUNCTION group_ops_v1_history_import_rows_reject_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'Group Ops history import rows are append-only';
END;
$$;
CREATE TRIGGER group_ops_v1_history_import_rows_append_only
    BEFORE UPDATE OR DELETE OR TRUNCATE ON group_ops_v1_history_import_rows
    FOR EACH STATEMENT EXECUTE FUNCTION group_ops_v1_history_import_rows_reject_mutation();

CREATE FUNCTION group_ops_v1_history_import_batches_guarded_update() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.source_system <> NEW.source_system OR OLD.source_revision <> NEW.source_revision
       OR OLD.snapshot_digest <> NEW.snapshot_digest OR OLD.manifest <> NEW.manifest
       OR NOT ((OLD.status = 'applying' AND NEW.status = 'applied' AND NEW.applied_at IS NOT NULL AND NEW.verified_at IS NULL)
               OR (OLD.status = 'applied' AND NEW.status = 'verified' AND NEW.applied_at IS NOT NULL AND NEW.verified_at IS NOT NULL AND OLD.imported_counts = NEW.imported_counts)) THEN
        RAISE EXCEPTION 'Group Ops history import batch mutation is not allowed';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER group_ops_v1_history_import_batches_guarded_update
    BEFORE UPDATE ON group_ops_v1_history_import_batches
    FOR EACH ROW EXECUTE FUNCTION group_ops_v1_history_import_batches_guarded_update();

CREATE FUNCTION group_ops_v1_history_import_batches_reject_delete() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'Group Ops history import batches are append-only';
END;
$$;
CREATE TRIGGER group_ops_v1_history_import_batches_append_only
    BEFORE DELETE OR TRUNCATE ON group_ops_v1_history_import_batches
    FOR EACH STATEMENT EXECUTE FUNCTION group_ops_v1_history_import_batches_reject_delete();

-- V1 nodes legitimately used a blank trigger-time label. Keep it distinct from
-- NULL and from a malformed padded label without rewriting the 0017 history.
ALTER TABLE group_ops_v1_history_nodes
    DROP CONSTRAINT IF EXISTS group_ops_v1_history_nodes_trigger_time_check;
ALTER TABLE group_ops_v1_history_nodes
    ADD CONSTRAINT group_ops_v1_history_nodes_trigger_time_check
    CHECK (btrim(trigger_time) = trigger_time);
