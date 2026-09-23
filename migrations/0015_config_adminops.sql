-- Owner: internal/config. This section holds only non-secret local
-- configuration facts. Secret-bearing runtime environment values never enter
-- these tables.
CREATE TABLE config_settings (
    setting_key TEXT PRIMARY KEY CHECK (setting_key IN (
		'wecom.corp_id','wecom.agent_id','outbound.rate_per_second','outbound.max_attempts',
		'adminops.app_settings.enabled','adminops.push_capabilities.enabled',
		'adminops.releases.enabled','adminops.diagnostics.enabled'
    )),
    value JSONB NOT NULL,
    updated_by TEXT NOT NULL CHECK (length(updated_by) BETWEEN 1 AND 200),
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE config_audits (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    request_id TEXT NOT NULL UNIQUE CHECK (length(request_id) BETWEEN 1 AND 200),
    setting_key TEXT NOT NULL REFERENCES config_settings(setting_key) DEFERRABLE INITIALLY DEFERRED,
    old_value JSONB,
    new_value JSONB NOT NULL,
    updated_by TEXT NOT NULL CHECK (length(updated_by) BETWEEN 1 AND 200),
    updated_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX config_audits_recent_idx ON config_audits(updated_at DESC, id DESC);

-- A local transactional outbox.  These facts are not provider intents and are
-- deliberately not consumed by outbound.
CREATE TABLE config_outbox (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    event_type TEXT NOT NULL CHECK (event_type = 'setting.updated'),
    idempotency_key TEXT NOT NULL UNIQUE CHECK (length(idempotency_key) BETWEEN 1 AND 240),
    payload JSONB NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
    occurred_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    published_at TIMESTAMPTZ
);

-- The HTTP idempotency key names one whole settings form submission, not one
-- row inside it.  Keeping this receipt in the same transaction prevents a
-- replay with a different set of setting keys from silently adding rows.
CREATE TABLE config_command_receipts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    action TEXT NOT NULL CHECK (action IN ('app_settings.save','setup_wizard.save','category_settings.save')),
    actor TEXT NOT NULL CHECK (length(actor) BETWEEN 1 AND 200),
    request_id TEXT NOT NULL CHECK (length(request_id) BETWEEN 1 AND 200),
    payload_digest BYTEA NOT NULL CHECK (octet_length(payload_digest) = 32),
    state TEXT NOT NULL CHECK (state IN ('reserved','completed')),
    created_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    UNIQUE (action, actor, request_id)
);

-- Owner: internal/adminops. Read-only release and diagnostic projections are
-- intentionally separate from configuration writes. They are filled by
-- deployment/runtime observation, never by the admin UI in this PR.
CREATE TABLE adminops_release_projections (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    release_sha TEXT NOT NULL CHECK (length(release_sha) BETWEEN 1 AND 200),
    status TEXT NOT NULL CHECK (status IN ('observed','active','superseded','failed')),
    observed_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    details JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(details) = 'object')
);
CREATE INDEX adminops_release_projections_recent_idx ON adminops_release_projections(observed_at DESC, id DESC);

CREATE TABLE adminops_diagnostic_snapshots (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    diagnostic_key TEXT NOT NULL CHECK (length(diagnostic_key) BETWEEN 1 AND 120),
    status TEXT NOT NULL CHECK (status IN ('ok','warning','error','unknown')),
    observed_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    details JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(details) = 'object')
);
CREATE INDEX adminops_diagnostic_snapshots_recent_idx ON adminops_diagnostic_snapshots(observed_at DESC, id DESC);

CREATE FUNCTION config_audits_reject_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'config_audits is append-only'; END;
$$;
CREATE TRIGGER config_audits_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON config_audits
FOR EACH STATEMENT EXECUTE FUNCTION config_audits_reject_mutation();
CREATE FUNCTION config_outbox_reject_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'config_outbox is append-only'; END;
$$;
CREATE TRIGGER config_outbox_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON config_outbox
FOR EACH STATEMENT EXECUTE FUNCTION config_outbox_reject_mutation();
