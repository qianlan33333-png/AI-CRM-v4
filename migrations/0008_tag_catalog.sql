-- Owner: internal/tag.  This catalog is not a customer-tag assignment model.
-- It has no customer ID, external identity, provider credential, or PII.
CREATE TABLE tag_groups (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    group_name TEXT NOT NULL CHECK (length(group_name) BETWEEN 1 AND 200),
    sort_order INTEGER NOT NULL CHECK (sort_order >= 0),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    archived_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE UNIQUE INDEX tag_groups_active_sort_unique ON tag_groups(sort_order) WHERE archived_at IS NULL;
CREATE TABLE tag_catalog_tags (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    group_id BIGINT NOT NULL REFERENCES tag_groups(id),
    tag_name TEXT NOT NULL CHECK (length(tag_name) BETWEEN 1 AND 200),
    sort_order INTEGER NOT NULL CHECK (sort_order >= 0),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    archived_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE UNIQUE INDEX tag_catalog_tags_active_sort_unique ON tag_catalog_tags(group_id,sort_order) WHERE archived_at IS NULL;
CREATE TABLE tag_references (
    resource_kind TEXT NOT NULL CHECK (resource_kind IN ('group','tag')),
    resource_id BIGINT NOT NULL,
    reference_digest TEXT NOT NULL CHECK (reference_digest ~ '^sha256:[0-9a-f]{64}$'),
    owner TEXT NOT NULL CHECK (length(owner) BETWEEN 1 AND 120),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY(resource_kind,resource_id,reference_digest)
);
CREATE TABLE tag_operation_receipts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    operation TEXT NOT NULL CHECK (length(operation) BETWEEN 1 AND 80),
    actor_admin_user_id BIGINT NOT NULL CHECK (actor_admin_user_id > 0),
    idempotency_key_digest BYTEA NOT NULL CHECK (octet_length(idempotency_key_digest)=32),
    payload_digest BYTEA NOT NULL CHECK (octet_length(payload_digest)=32),
    state TEXT NOT NULL CHECK (state IN ('in_progress','completed')),
    result_ids BIGINT[] NOT NULL DEFAULT '{}',
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE(operation,actor_admin_user_id,idempotency_key_digest)
);
CREATE TABLE tag_audit_events (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    event_type TEXT NOT NULL CHECK (event_type ~ '^tag[.][a-z0-9_.]+$'),
    actor_admin_user_id BIGINT NOT NULL CHECK (actor_admin_user_id > 0),
    payload JSONB NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL
);
CREATE TABLE tag_outbox (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    event_type TEXT NOT NULL CHECK (event_type ~ '^tag[.][a-z0-9_.]+$'),
    aggregate_kind TEXT NOT NULL,
    aggregate_id BIGINT NOT NULL,
    payload JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    published_at TIMESTAMPTZ
);
CREATE TABLE tag_sync_receipts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    actor_admin_user_id BIGINT NOT NULL CHECK (actor_admin_user_id > 0),
    idempotency_key_digest BYTEA NOT NULL CHECK (octet_length(idempotency_key_digest)=32),
    trace_id TEXT NOT NULL DEFAULT '' CHECK (length(trace_id)<=200),
    sync_kind TEXT NOT NULL CHECK (sync_kind IN ('manual','due')),
    state TEXT NOT NULL CHECK (state IN ('reserved','queued')),
    event_id BIGINT NOT NULL DEFAULT 0,
    queue_job_id BIGINT NOT NULL DEFAULT 0,
	 effect_id BIGINT NOT NULL DEFAULT 0,
	 effect_ref TEXT NOT NULL DEFAULT '',
	 effect_state TEXT NOT NULL DEFAULT '',
	 accept_receipt_id TEXT NOT NULL DEFAULT '',
	 queue_receipt_id TEXT NOT NULL DEFAULT '',
    accepted_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE(actor_admin_user_id,idempotency_key_digest)
);
CREATE TABLE tag_provider_observations (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    effect_id BIGINT NOT NULL CHECK(effect_id>0), generation BIGINT NOT NULL CHECK(generation>0),
    artifact_digest TEXT NOT NULL CHECK(artifact_digest ~ '^sha256:[0-9a-f]{64}$'),
    snapshot JSONB NOT NULL, observed_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE(effect_id,generation),
    FOREIGN KEY(effect_id,generation) REFERENCES external_effect_generations(effect_id,generation) ON DELETE RESTRICT
);
CREATE FUNCTION tag_provider_observations_reject_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'tag_provider_observations is append-only';
END;
$$;
CREATE TRIGGER tag_provider_observations_append_only
BEFORE UPDATE OR DELETE OR TRUNCATE ON tag_provider_observations
FOR EACH STATEMENT EXECUTE FUNCTION tag_provider_observations_reject_mutation();
