-- Owner: internal/configmigration/target. This is a one-time, local
-- configuration-definition migration ledger. It deliberately stores no
-- customer/OneID facts, messages, execution history, provider state, or
-- secrets. Source snapshots are encrypted outside PostgreSQL; only their
-- SHA-256 digests and safe source locators are retained here.

-- Empty means "not configured" and is the normal value for every paused
-- imported plan. Keep real non-empty webhook references unique while allowing
-- multiple plans to remain safely unconfigured and editable.
ALTER TABLE group_ops_plan_webhook_descriptors
    DROP CONSTRAINT group_ops_plan_webhook_descriptors_reference_key;
CREATE UNIQUE INDEX group_ops_plan_webhook_descriptors_nonempty_reference_unique
    ON group_ops_plan_webhook_descriptors(reference)
    WHERE reference <> '';

ALTER TABLE group_ops_plans
    ADD COLUMN legacy_import_definition JSONB NOT NULL DEFAULT '{}'::jsonb
    CHECK (jsonb_typeof(legacy_import_definition) = 'object');
ALTER TABLE group_ops_plan_nodes
    ADD COLUMN legacy_import_definition JSONB NOT NULL DEFAULT '{}'::jsonb
    CHECK (jsonb_typeof(legacy_import_definition) = 'object');

CREATE TABLE config_definition_import_batches (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    source_system TEXT NOT NULL CHECK (length(source_system) BETWEEN 1 AND 160),
    batch_key TEXT NOT NULL CHECK (batch_key ~ '^[A-Za-z0-9._:-]{1,200}$'),
    snapshot_digest BYTEA NOT NULL CHECK (octet_length(snapshot_digest) = 32),
    actor_admin_user_id BIGINT NOT NULL REFERENCES admin_users(id) ON DELETE RESTRICT,
    status TEXT NOT NULL CHECK (status IN ('applying', 'applied', 'verified')),
    manifest JSONB NOT NULL CHECK (jsonb_typeof(manifest) = 'object'),
    imported_counts JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(imported_counts) = 'object'),
    verified_at TIMESTAMPTZ,
    applied_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT config_definition_import_batches_source_key_unique UNIQUE (source_system, batch_key),
    CONSTRAINT config_definition_import_batches_snapshot_unique UNIQUE (source_system, snapshot_digest),
    CONSTRAINT config_definition_import_batches_status_times CHECK (
        (status = 'applying' AND applied_at IS NULL AND verified_at IS NULL)
        OR (status = 'applied' AND applied_at IS NOT NULL AND verified_at IS NULL)
        OR (status = 'verified' AND applied_at IS NOT NULL AND verified_at IS NOT NULL)
    )
);

-- Product owns this migration-only safe definition projection. The current
-- Product API has no duration field, so retaining the positive duration here
-- prevents the service-period business definition from being silently lost.
-- It contains no entitlement, customer, membership state, or execution data.
CREATE TABLE product_imported_service_period_definitions (
    product_id BIGINT PRIMARY KEY REFERENCES products(id) ON DELETE RESTRICT,
    duration_days INTEGER NOT NULL CHECK (duration_days > 0)
);

CREATE TABLE config_definition_import_source_maps (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    batch_id BIGINT NOT NULL REFERENCES config_definition_import_batches(id) ON DELETE RESTRICT,
    source_system TEXT NOT NULL CHECK (length(source_system) BETWEEN 1 AND 160),
    domain TEXT NOT NULL CHECK (domain IN ('product', 'coupon', 'groupops', 'automation')),
    source_kind TEXT NOT NULL CHECK (length(source_kind) BETWEEN 1 AND 120),
    source_key TEXT NOT NULL CHECK (length(source_key) BETWEEN 1 AND 240),
    source_digest BYTEA NOT NULL CHECK (octet_length(source_digest) = 32),
    source_actor_labels JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (
        jsonb_typeof(source_actor_labels) = 'object'
        AND source_actor_labels - ARRAY['created_by','updated_by'] = '{}'::jsonb
        AND COALESCE(char_length(source_actor_labels->>'created_by'), 0) <= 160
        AND COALESCE(char_length(source_actor_labels->>'updated_by'), 0) <= 160
    ),
    target_table TEXT NOT NULL CHECK (target_table IN ('products', 'coupon_rules', 'group_ops_plans', 'automation_agents')),
    target_id BIGINT NOT NULL CHECK (target_id > 0),
    imported_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT config_definition_import_source_maps_source_unique UNIQUE (source_system, domain, source_kind, source_key),
    CONSTRAINT config_definition_import_source_maps_batch_source_unique UNIQUE (batch_id, domain, source_kind, source_key)
);

CREATE INDEX config_definition_import_source_maps_batch_idx
    ON config_definition_import_source_maps(batch_id, domain, target_table, target_id);

-- Historical import provenance must remain reproducible. Batches advance
-- only through the explicit target coordinator; mappings are append-only.
CREATE FUNCTION config_definition_import_source_maps_reject_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'config definition import source maps are append-only';
END;
$$;


CREATE TRIGGER config_definition_import_source_maps_append_only
    BEFORE UPDATE OR DELETE OR TRUNCATE ON config_definition_import_source_maps
    FOR EACH STATEMENT EXECUTE FUNCTION config_definition_import_source_maps_reject_mutation();

CREATE TABLE config_definition_import_quarantines (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    batch_id BIGINT NOT NULL REFERENCES config_definition_import_batches(id) ON DELETE RESTRICT,
    domain TEXT NOT NULL CHECK (domain IN ('product', 'coupon', 'groupops', 'automation')),
    source_kind TEXT NOT NULL CHECK (length(source_kind) BETWEEN 1 AND 120),
    source_key_digest BYTEA NOT NULL CHECK (octet_length(source_key_digest) = 32),
    source_digest BYTEA NOT NULL CHECK (octet_length(source_digest) = 32),
    reason_code TEXT NOT NULL CHECK (reason_code ~ '^[a-z0-9_]{1,80}$'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT config_definition_import_quarantines_unique UNIQUE (batch_id, domain, source_kind, source_key_digest, reason_code)
);

CREATE FUNCTION config_definition_import_quarantines_reject_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'config definition import quarantines are append-only';
END;
$$;

CREATE TRIGGER config_definition_import_quarantines_append_only
    BEFORE UPDATE OR DELETE OR TRUNCATE ON config_definition_import_quarantines
    FOR EACH STATEMENT EXECUTE FUNCTION config_definition_import_quarantines_reject_mutation();


CREATE FUNCTION config_definition_import_batches_reject_unsafe_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP <> 'UPDATE' THEN
        RAISE EXCEPTION 'configuration definition import batches are append-only';
    END IF;
    IF OLD.source_system <> NEW.source_system OR OLD.batch_key <> NEW.batch_key
       OR OLD.snapshot_digest <> NEW.snapshot_digest OR OLD.actor_admin_user_id <> NEW.actor_admin_user_id
       OR OLD.manifest <> NEW.manifest
       OR NOT ((OLD.status = 'applying' AND NEW.status = 'applied' AND NEW.applied_at IS NOT NULL AND NEW.verified_at IS NULL)
               OR (OLD.status = 'applied' AND NEW.status = 'verified' AND NEW.applied_at IS NOT NULL AND NEW.verified_at IS NOT NULL AND OLD.imported_counts = NEW.imported_counts)) THEN
        RAISE EXCEPTION 'configuration definition import batch mutation is not allowed';
    END IF;
    RETURN NEW;
END;
$$;


CREATE TRIGGER config_definition_import_batches_guarded_update
    BEFORE UPDATE ON config_definition_import_batches
    FOR EACH ROW EXECUTE FUNCTION config_definition_import_batches_reject_unsafe_mutation();

CREATE FUNCTION config_definition_import_batches_reject_delete() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'configuration definition import batches are append-only';
END;
$$;

CREATE TRIGGER config_definition_import_batches_reject_delete
    BEFORE DELETE OR TRUNCATE ON config_definition_import_batches
    FOR EACH STATEMENT EXECUTE FUNCTION config_definition_import_batches_reject_delete();
