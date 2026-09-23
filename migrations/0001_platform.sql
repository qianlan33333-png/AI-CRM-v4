-- Owner: internal/platform
-- Strategy: forward-only baseline; a failed migration transaction leaves no partial schema.

CREATE TABLE audit_events (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    idempotency_key text NOT NULL UNIQUE,
    action text NOT NULL,
    actor_type text NOT NULL,
    actor_id text,
    resource_type text NOT NULL,
    resource_id text,
    payload jsonb NOT NULL DEFAULT '{}'::jsonb,
    occurred_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT audit_events_idempotency_key_nonempty CHECK (length(idempotency_key) BETWEEN 8 AND 200),
    CONSTRAINT audit_events_action_nonempty CHECK (length(action) BETWEEN 1 AND 120),
    CONSTRAINT audit_events_actor_type_nonempty CHECK (length(actor_type) BETWEEN 1 AND 80),
    CONSTRAINT audit_events_resource_type_nonempty CHECK (length(resource_type) BETWEEN 1 AND 80)
);

CREATE INDEX audit_events_resource_timeline_idx
    ON audit_events (resource_type, resource_id, occurred_at DESC, id DESC);

CREATE TABLE idempotency_receipts (
    idempotency_key text PRIMARY KEY,
    payload_hash bytea NOT NULL,
    status text NOT NULL DEFAULT 'accepted',
    response jsonb,
    attempt_count integer NOT NULL DEFAULT 0,
    max_attempts integer NOT NULL DEFAULT 8,
    next_attempt_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    lease_owner text,
    lease_expires_at timestamptz,
    last_error_code text,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT idempotency_receipts_key_nonempty CHECK (length(idempotency_key) BETWEEN 8 AND 200),
    CONSTRAINT idempotency_receipts_payload_hash_sha256 CHECK (octet_length(payload_hash) = 32),
    CONSTRAINT idempotency_receipts_status_valid CHECK (
        status IN ('accepted', 'queued', 'attempted', 'executed', 'outcome_unknown', 'reconciled', 'failed')
    ),
    CONSTRAINT idempotency_receipts_attempts_valid CHECK (
        attempt_count >= 0 AND max_attempts > 0 AND attempt_count <= max_attempts
    ),
    CONSTRAINT idempotency_receipts_lease_pair CHECK (
        (lease_owner IS NULL) = (lease_expires_at IS NULL)
    )
);

CREATE INDEX idempotency_receipts_claim_idx
    ON idempotency_receipts (next_attempt_at, created_at)
    WHERE status IN ('accepted', 'queued', 'attempted') AND attempt_count < max_attempts;

CREATE TABLE webhook_inbox (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    provider text NOT NULL,
    idempotency_key text NOT NULL,
    payload_hash bytea NOT NULL,
    payload jsonb NOT NULL,
    status text NOT NULL DEFAULT 'received',
    attempt_count integer NOT NULL DEFAULT 0,
    max_attempts integer NOT NULL DEFAULT 8,
    next_attempt_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    lease_owner text,
    lease_expires_at timestamptz,
    last_error_code text,
    received_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    processed_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT webhook_inbox_delivery_unique UNIQUE (provider, idempotency_key),
    CONSTRAINT webhook_inbox_provider_nonempty CHECK (length(provider) BETWEEN 1 AND 80),
    CONSTRAINT webhook_inbox_key_nonempty CHECK (length(idempotency_key) BETWEEN 8 AND 200),
    CONSTRAINT webhook_inbox_payload_hash_sha256 CHECK (octet_length(payload_hash) = 32),
    CONSTRAINT webhook_inbox_status_valid CHECK (
        status IN ('received', 'processing', 'processed', 'retryable', 'failed')
    ),
    CONSTRAINT webhook_inbox_attempts_valid CHECK (
        attempt_count >= 0 AND max_attempts > 0 AND attempt_count <= max_attempts
    ),
    CONSTRAINT webhook_inbox_lease_pair CHECK (
        (lease_owner IS NULL) = (lease_expires_at IS NULL)
    )
);

CREATE INDEX webhook_inbox_claim_idx
    ON webhook_inbox (next_attempt_at, received_at)
    WHERE status IN ('received', 'processing', 'retryable') AND attempt_count < max_attempts;
