-- Owners: platform, identity, internal/wecom and internal/customer.
-- This forward-only migration introduces the durable records for the customer
-- activation/list slice. Provider credentials and imported clear-text files
-- are deliberately not persisted here.

-- Owner: internal/platform. River remains the sole internal durable job
-- kernel. This table owns versioned projection events only; application logs
-- must never print payload_json.
CREATE TABLE outbox_events (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    aggregate_type TEXT NOT NULL CHECK (length(aggregate_type) BETWEEN 1 AND 80),
    aggregate_id TEXT NOT NULL CHECK (length(aggregate_id) BETWEEN 1 AND 200),
    event_type TEXT NOT NULL CHECK (length(event_type) BETWEEN 1 AND 160),
    event_version SMALLINT NOT NULL CHECK (event_version > 0),
    idempotency_key TEXT NOT NULL UNIQUE CHECK (length(idempotency_key) BETWEEN 8 AND 240),
    payload_json JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(payload_json) = 'object'),
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    available_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    claimed_by TEXT,
    claim_expires_at TIMESTAMPTZ,
    processed_at TIMESTAMPTZ,
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    last_error_code TEXT,
    CONSTRAINT outbox_events_claim_pair CHECK ((claimed_by IS NULL) = (claim_expires_at IS NULL))
);
CREATE INDEX outbox_events_pending_idx ON outbox_events(available_at, id)
    WHERE processed_at IS NULL;

-- Owner: internal/wecom.
CREATE TABLE wecom_customer_sync_runs (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    run_key TEXT NOT NULL UNIQUE CHECK (length(run_key) BETWEEN 8 AND 200),
    trigger_type TEXT NOT NULL CHECK (trigger_type IN ('initial', 'daily', 'manual')),
    status TEXT NOT NULL CHECK (status IN (
        'queued', 'listing_staff', 'fetching_profiles', 'ingesting', 'reconciling',
        'succeeded', 'failed_retryable', 'failed_terminal'
    )),
    resume_status TEXT CHECK (resume_status IN ('listing_staff', 'fetching_profiles', 'ingesting', 'reconciling')),
    corp_scope TEXT NOT NULL CHECK (left(corp_scope, 11) = 'wecom-corp:' AND length(corp_scope) > 11),
    staff_ids JSONB NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(staff_ids) = 'array'),
    staff_index INTEGER NOT NULL DEFAULT 0 CHECK (staff_index >= 0),
    provider_cursor TEXT NOT NULL DEFAULT '',
    discovered_count BIGINT NOT NULL DEFAULT 0 CHECK (discovered_count >= 0),
    activated_count BIGINT NOT NULL DEFAULT 0 CHECK (activated_count >= 0),
    already_linked_count BIGINT NOT NULL DEFAULT 0 CHECK (already_linked_count >= 0),
    conflict_count BIGINT NOT NULL DEFAULT 0 CHECK (conflict_count >= 0),
    terminal_failed_count BIGINT NOT NULL DEFAULT 0 CHECK (terminal_failed_count >= 0),
    projected_count BIGINT NOT NULL DEFAULT 0 CHECK (projected_count >= 0),
    stale_count BIGINT NOT NULL DEFAULT 0 CHECK (stale_count >= 0),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    last_error_code TEXT,
    requested_by BIGINT,
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT wecom_customer_sync_runs_completion CHECK (
        (status = 'succeeded' AND completed_at IS NOT NULL)
        OR (status <> 'succeeded' AND completed_at IS NULL)
    ),
    CONSTRAINT wecom_customer_sync_runs_resume CHECK (
        (status = 'failed_retryable' AND resume_status IS NOT NULL)
        OR (status <> 'failed_retryable' AND resume_status IS NULL)
    )
);
CREATE UNIQUE INDEX wecom_customer_sync_runs_one_active_idx ON wecom_customer_sync_runs((true))
    WHERE status IN ('queued', 'listing_staff', 'fetching_profiles', 'ingesting', 'reconciling', 'failed_retryable');
CREATE INDEX wecom_customer_sync_runs_recent_idx ON wecom_customer_sync_runs(created_at DESC, id DESC);

CREATE TABLE wecom_customer_sync_items (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    run_id BIGINT NOT NULL REFERENCES wecom_customer_sync_runs(id),
    corp_scope TEXT NOT NULL,
    external_userid TEXT NOT NULL CHECK (external_userid <> '' AND external_userid !~ '[[:cntrl:]]'),
    external_userid_digest BYTEA NOT NULL CHECK (octet_length(external_userid_digest) = 32),
    staff_id_digest BYTEA NOT NULL CHECK (octet_length(staff_id_digest) = 32),
    payload_digest BYTEA NOT NULL CHECK (octet_length(payload_digest) = 32),
    outcome TEXT NOT NULL CHECK (outcome IN ('activated', 'already_linked', 'conflict', 'terminal_failed')),
    customer_id BIGINT REFERENCES customers(id),
    identity_id BIGINT REFERENCES customer_identities(id),
    error_code TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT wecom_customer_sync_items_run_external_unique UNIQUE(run_id, corp_scope, external_userid)
);
CREATE INDEX wecom_customer_sync_items_run_outcome_idx ON wecom_customer_sync_items(run_id, outcome);

CREATE TABLE wecom_external_contact_profiles (
    customer_id BIGINT PRIMARY KEY REFERENCES customers(id),
    corp_scope TEXT NOT NULL,
    external_identity_id BIGINT NOT NULL REFERENCES customer_identities(id),
    display_name TEXT NOT NULL DEFAULT '',
    avatar_url TEXT NOT NULL DEFAULT '',
    gender SMALLINT NOT NULL DEFAULT 0 CHECK (gender BETWEEN 0 AND 2),
    contact_type SMALLINT NOT NULL DEFAULT 0 CHECK (contact_type BETWEEN 0 AND 3),
    corp_name TEXT NOT NULL DEFAULT '',
    activation_status TEXT NOT NULL DEFAULT 'active' CHECK (activation_status IN ('active', 'conflict', 'stale')),
    profile_digest BYTEA NOT NULL CHECK (octet_length(profile_digest) = 32),
    last_seen_run_id BIGINT NOT NULL REFERENCES wecom_customer_sync_runs(id),
    fetched_at TIMESTAMPTZ NOT NULL,
    stale_at TIMESTAMPTZ,
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT wecom_external_contact_profiles_stale CHECK (
        (activation_status = 'stale' AND stale_at IS NOT NULL)
        OR (activation_status <> 'stale' AND stale_at IS NULL)
    )
);
CREATE INDEX wecom_external_contact_profiles_seen_idx ON wecom_external_contact_profiles(last_seen_run_id, customer_id);

-- Owner: identity. A run is an immutable import ledger. Receipt digests make
-- every source row reconcilable without retaining the source phone in logs.
CREATE TABLE identity_phone_import_runs (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    run_key TEXT NOT NULL UNIQUE CHECK (length(run_key) BETWEEN 8 AND 200),
    schema_version TEXT NOT NULL CHECK (schema_version <> ''),
    source_manifest_digest BYTEA NOT NULL CHECK (octet_length(source_manifest_digest) = 32),
    status TEXT NOT NULL CHECK (status IN ('inspected', 'dry_run', 'applying', 'applied', 'reconciled', 'rolled_back', 'failed')),
    input_count BIGINT NOT NULL DEFAULT 0 CHECK (input_count >= 0),
    attached_count BIGINT NOT NULL DEFAULT 0 CHECK (attached_count >= 0),
    already_linked_count BIGINT NOT NULL DEFAULT 0 CHECK (already_linked_count >= 0),
    conflict_count BIGINT NOT NULL DEFAULT 0 CHECK (conflict_count >= 0),
    unresolved_count BIGINT NOT NULL DEFAULT 0 CHECK (unresolved_count >= 0),
    invalid_count BIGINT NOT NULL DEFAULT 0 CHECK (invalid_count >= 0),
    duplicate_input_count BIGINT NOT NULL DEFAULT 0 CHECK (duplicate_input_count >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    completed_at TIMESTAMPTZ,
    rolled_back_at TIMESTAMPTZ
);

CREATE TABLE identity_phone_import_receipts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    run_id BIGINT NOT NULL REFERENCES identity_phone_import_runs(id),
    source_row_id TEXT NOT NULL CHECK (source_row_id <> ''),
    source_row_digest BYTEA NOT NULL CHECK (octet_length(source_row_digest) = 32),
    outcome TEXT NOT NULL CHECK (outcome IN (
        'attached', 'already_linked', 'conflict', 'unresolved_external_identity', 'invalid', 'duplicate_input', 'replayed'
    )),
    customer_id BIGINT REFERENCES customers(id),
    identity_id BIGINT REFERENCES customer_identities(id),
    error_code TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT identity_phone_import_receipts_row_unique UNIQUE(run_id, source_row_id)
);
CREATE INDEX identity_phone_import_receipts_run_outcome_idx ON identity_phone_import_receipts(run_id, outcome);

-- Owner: internal/customer. This is a disposable read model, not an identity
-- source of truth. Raw external IDs and clear-text phones are forbidden.
CREATE TABLE customer_directory_projection (
    customer_id BIGINT PRIMARY KEY,
    customer_status TEXT NOT NULL CHECK (customer_status IN ('active', 'merged', 'closed')),
    display_name TEXT NOT NULL DEFAULT '',
    avatar_url TEXT NOT NULL DEFAULT '',
    gender SMALLINT NOT NULL DEFAULT 0 CHECK (gender BETWEEN 0 AND 2),
    contact_type SMALLINT NOT NULL DEFAULT 0 CHECK (contact_type BETWEEN 0 AND 3),
    corp_name TEXT NOT NULL DEFAULT '',
    oneid_label TEXT NOT NULL DEFAULT '',
    phone_masked TEXT NOT NULL DEFAULT '',
    phone_assurance TEXT CHECK (phone_assurance IN ('verified', 'declared')),
    activation_status TEXT NOT NULL DEFAULT 'active' CHECK (activation_status IN ('active', 'conflict', 'stale')),
    source TEXT NOT NULL DEFAULT 'wecom_directory_sync',
    source_version BIGINT NOT NULL DEFAULT 1 CHECK (source_version > 0),
    last_synced_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX customer_directory_projection_page_idx ON customer_directory_projection(updated_at DESC, customer_id DESC);
CREATE INDEX customer_directory_projection_activation_idx ON customer_directory_projection(activation_status, updated_at DESC, customer_id DESC);
