-- Owner: internal/config. Published business configuration is separate from
-- deployment observations and ordinary local app settings. Values are limited
-- to the registered non-secret runtime key set by Config application code.

CREATE TABLE config_runtime_releases (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    state TEXT NOT NULL CHECK (state IN ('draft','validated','validation_failed','published','superseded')),
    base_revision BIGINT NOT NULL DEFAULT 0 CHECK (base_revision >= 0),
    rollback_of_release_id BIGINT REFERENCES config_runtime_releases(id) ON DELETE RESTRICT,
    checksum BYTEA NOT NULL CHECK (octet_length(checksum) = 32),
    validation_errors JSONB NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(validation_errors) = 'array'),
    created_by TEXT NOT NULL CHECK (length(created_by) BETWEEN 1 AND 200),
    created_at TIMESTAMPTZ NOT NULL,
    validated_at TIMESTAMPTZ,
    published_by TEXT CHECK (published_by IS NULL OR length(published_by) BETWEEN 1 AND 200),
    published_at TIMESTAMPTZ,
    CONSTRAINT config_runtime_releases_time_shape CHECK (
        (state IN ('draft','validated','validation_failed') AND published_at IS NULL AND published_by IS NULL)
        OR (state IN ('published','superseded') AND published_at IS NOT NULL AND published_by IS NOT NULL)
    )
);

CREATE TABLE config_runtime_release_values (
    release_id BIGINT NOT NULL REFERENCES config_runtime_releases(id) ON DELETE RESTRICT,
    setting_key TEXT NOT NULL CHECK (setting_key IN ('automation.operations.max_recipients_per_run')),
    value JSONB NOT NULL,
    PRIMARY KEY(release_id, setting_key)
);

CREATE TABLE config_runtime_active_release (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    release_id BIGINT REFERENCES config_runtime_releases(id) ON DELETE RESTRICT,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
INSERT INTO config_runtime_active_release(singleton, release_id) VALUES(TRUE, NULL);

CREATE TABLE config_runtime_release_command_receipts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    action TEXT NOT NULL CHECK (action IN ('runtime_release.create','runtime_release.validate','runtime_release.publish','runtime_release.rollback')),
    actor TEXT NOT NULL CHECK (length(actor) BETWEEN 1 AND 200),
    idempotency_key TEXT NOT NULL CHECK (length(idempotency_key) BETWEEN 8 AND 200),
    payload_digest BYTEA NOT NULL CHECK (octet_length(payload_digest) = 32),
    release_id BIGINT REFERENCES config_runtime_releases(id) ON DELETE RESTRICT,
    state TEXT NOT NULL CHECK (state IN ('reserved','completed')),
    created_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    UNIQUE(action, actor, idempotency_key),
    CONSTRAINT config_runtime_release_command_receipts_completion CHECK (
        (state='reserved' AND release_id IS NULL AND completed_at IS NULL)
        OR (state='completed' AND release_id IS NOT NULL AND completed_at IS NOT NULL)
    )
);

CREATE TABLE config_runtime_release_audits (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    release_id BIGINT NOT NULL REFERENCES config_runtime_releases(id) ON DELETE RESTRICT,
    action TEXT NOT NULL CHECK (action IN ('created','validated','validation_failed','published','superseded','rolled_back')),
    actor TEXT NOT NULL CHECK (length(actor) BETWEEN 1 AND 200),
    request_id TEXT NOT NULL CHECK (length(request_id) BETWEEN 8 AND 200),
    created_at TIMESTAMPTZ NOT NULL,
    UNIQUE(release_id, action, request_id)
);

CREATE TABLE config_runtime_usage (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    revision BIGINT NOT NULL CHECK (revision >= 0),
    source TEXT NOT NULL CHECK (source IN ('environment_default','published')),
    consumer TEXT NOT NULL CHECK (consumer = 'automation.operations.max_recipients_per_run'),
    role TEXT NOT NULL CHECK (role IN ('api','worker','effects-worker')),
    operation TEXT NOT NULL CHECK (operation IN ('preview','confirm','execution')),
    subject_kind TEXT NOT NULL CHECK (subject_kind IN ('automation_preview','automation_run')),
    subject_id BIGINT NOT NULL CHECK (subject_id > 0),
    used_at TIMESTAMPTZ NOT NULL,
    UNIQUE(revision, source, consumer, role, operation, subject_kind, subject_id)
);

-- Automation owns frozen recipient ceilings only for runs created after this
-- release. Existing previews/runs have no trustworthy historical Config
-- revision, so they remain explicitly unobserved and recover through their
-- already-persisted target_count boundary instead of being rewritten as 1.
ALTER TABLE automation_run_previews
    ADD COLUMN runtime_config_observed BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN runtime_config_revision BIGINT,
    ADD COLUMN max_recipients_per_run INTEGER,
    ADD CONSTRAINT automation_run_previews_runtime_config_shape CHECK (
        (runtime_config_observed AND runtime_config_revision IS NOT NULL AND max_recipients_per_run IS NOT NULL AND runtime_config_revision >= 0 AND max_recipients_per_run BETWEEN 1 AND 5000)
        OR (NOT runtime_config_observed AND runtime_config_revision IS NULL AND max_recipients_per_run IS NULL)
    );
ALTER TABLE automation_runs
    ADD COLUMN runtime_config_observed BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN runtime_config_revision BIGINT,
    ADD COLUMN max_recipients_per_run INTEGER,
    ADD CONSTRAINT automation_runs_runtime_config_shape CHECK (
        (runtime_config_observed AND runtime_config_revision IS NOT NULL AND max_recipients_per_run IS NOT NULL AND runtime_config_revision >= 0 AND max_recipients_per_run BETWEEN 1 AND 5000)
        OR (NOT runtime_config_observed AND runtime_config_revision IS NULL AND max_recipients_per_run IS NULL)
    );

ALTER TABLE config_outbox DROP CONSTRAINT config_outbox_event_type_check;
ALTER TABLE config_outbox ADD CONSTRAINT config_outbox_event_type_check CHECK (event_type IN ('setting.updated','runtime_release.published','runtime_release.rolled_back'));

CREATE FUNCTION config_runtime_release_values_reject_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'config_runtime_release_values are immutable'; END;
$$;
CREATE TRIGGER config_runtime_release_values_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON config_runtime_release_values
FOR EACH STATEMENT EXECUTE FUNCTION config_runtime_release_values_reject_mutation();

CREATE FUNCTION config_runtime_release_audits_reject_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'config_runtime_release_audits are append-only'; END;
$$;
CREATE TRIGGER config_runtime_release_audits_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON config_runtime_release_audits
FOR EACH STATEMENT EXECUTE FUNCTION config_runtime_release_audits_reject_mutation();

CREATE FUNCTION config_runtime_usage_reject_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'config_runtime_usage is append-only'; END;
$$;
CREATE TRIGGER config_runtime_usage_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON config_runtime_usage
FOR EACH STATEMENT EXECUTE FUNCTION config_runtime_usage_reject_mutation();

CREATE FUNCTION config_runtime_release_receipts_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.action <> NEW.action OR OLD.actor <> NEW.actor OR OLD.idempotency_key <> NEW.idempotency_key OR OLD.payload_digest <> NEW.payload_digest OR OLD.created_at <> NEW.created_at THEN
    RAISE EXCEPTION 'config_runtime_release receipt identity is immutable';
  END IF;
  IF OLD.state <> 'reserved' OR NEW.state <> 'completed' OR NEW.release_id IS NULL OR NEW.completed_at IS NULL THEN
    RAISE EXCEPTION 'invalid config_runtime_release receipt transition';
  END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER config_runtime_release_receipts_guard BEFORE UPDATE ON config_runtime_release_command_receipts
FOR EACH ROW EXECUTE FUNCTION config_runtime_release_receipts_guard();

CREATE FUNCTION config_runtime_releases_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.base_revision <> NEW.base_revision OR OLD.rollback_of_release_id IS DISTINCT FROM NEW.rollback_of_release_id OR OLD.checksum <> NEW.checksum OR OLD.created_by <> NEW.created_by OR OLD.created_at <> NEW.created_at THEN
    RAISE EXCEPTION 'config_runtime_release content is immutable';
  END IF;
  IF OLD.state = 'draft' AND NEW.state IN ('validated','validation_failed') AND NEW.published_at IS NULL AND NEW.published_by IS NULL THEN
    RETURN NEW;
  END IF;
  IF OLD.state IN ('validated','validation_failed') AND NEW.state IN ('validated','validation_failed') AND NEW.published_at IS NULL AND NEW.published_by IS NULL THEN
    RETURN NEW;
  END IF;
  IF OLD.state = 'validated' AND NEW.state = 'published' AND NEW.published_at IS NOT NULL AND NEW.published_by IS NOT NULL THEN
    RETURN NEW;
  END IF;
  IF OLD.state = 'published' AND NEW.state = 'superseded' THEN
    RETURN NEW;
  END IF;
  RAISE EXCEPTION 'invalid config_runtime_release transition';
END;
$$;
CREATE TRIGGER config_runtime_releases_guard BEFORE UPDATE ON config_runtime_releases
FOR EACH ROW EXECUTE FUNCTION config_runtime_releases_guard();

-- Offline legacy release history is deliberately separate from live runtime
-- releases. A historic V2 row never changes config_runtime_active_release and
-- has no runtime, outbox, or Provider effect.
CREATE TABLE config_runtime_release_history_batches (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    source_system TEXT NOT NULL CHECK(length(source_system) BETWEEN 1 AND 160),
    source_revision TEXT NOT NULL CHECK(source_revision ~ '^[a-f0-9]{40}$'),
    manifest_digest BYTEA NOT NULL CHECK(octet_length(manifest_digest)=32),
    snapshot_at TIMESTAMPTZ NOT NULL,
    status TEXT NOT NULL CHECK(status IN ('applied','reconciled')),
    input_count INTEGER NOT NULL CHECK(input_count>=0),
    pending_count INTEGER NOT NULL CHECK(pending_count>=0),
    excluded_count INTEGER NOT NULL CHECK(excluded_count>=0),
    applied_at TIMESTAMPTZ NOT NULL,
    reconciled_at TIMESTAMPTZ,
    CHECK(input_count=pending_count+excluded_count),
    UNIQUE(source_system,source_revision)
);
CREATE TABLE config_runtime_release_history_rows (
    batch_id BIGINT NOT NULL REFERENCES config_runtime_release_history_batches(id) ON DELETE RESTRICT,
    source_release_id BIGINT NOT NULL CHECK(source_release_id>0),
    source_release_key TEXT NOT NULL CHECK(length(source_release_key) BETWEEN 1 AND 200),
    source_profile_id TEXT NOT NULL CHECK(length(source_profile_id) BETWEEN 1 AND 200),
    source_checksum TEXT NOT NULL CHECK(length(source_checksum) BETWEEN 0 AND 200),
    source_digest BYTEA NOT NULL CHECK(octet_length(source_digest)=32),
    source_state TEXT NOT NULL CHECK(length(source_state) BETWEEN 1 AND 80),
    source_created_by TEXT NOT NULL CHECK(length(source_created_by) BETWEEN 0 AND 200),
    source_published_by TEXT NOT NULL CHECK(length(source_published_by) BETWEEN 0 AND 200),
    source_created_at TIMESTAMPTZ NOT NULL,
    source_validated_at TIMESTAMPTZ,
    source_published_at TIMESTAMPTZ,
    source_based_on_release_id BIGINT,
    source_rollback_of_release_id BIGINT,
    outcome TEXT NOT NULL CHECK(outcome='excluded'),
    reason_code TEXT NOT NULL CHECK(reason_code='no_v3_runtime_equivalence'),
    read_only BOOLEAN NOT NULL DEFAULT TRUE CHECK(read_only),
    PRIMARY KEY(batch_id,source_release_id)
);
CREATE INDEX config_runtime_release_history_rows_source_idx ON config_runtime_release_history_rows(source_release_id);
