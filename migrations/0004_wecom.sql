-- Owner: internal/wecom. All writes are made through injected platform UOWs.

CREATE TABLE wecom_oauth_states (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    purpose TEXT NOT NULL CHECK (purpose IN ('admin', 'sidebar')),
    state_digest BYTEA NOT NULL UNIQUE,
    nonce_digest BYTEA NOT NULL UNIQUE,
    redirect_path TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    used_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT wecom_oauth_states_digest_sha256 CHECK (octet_length(state_digest) = 32),
    CONSTRAINT wecom_oauth_states_nonce_digest_sha256 CHECK (octet_length(nonce_digest) = 32),
    CONSTRAINT wecom_oauth_states_redirect_nonempty CHECK (length(redirect_path) BETWEEN 1 AND 2048),
    CONSTRAINT wecom_oauth_states_expiry_after_creation CHECK (expires_at > created_at),
    CONSTRAINT wecom_oauth_states_used_after_creation CHECK (used_at IS NULL OR used_at >= created_at)
);
CREATE INDEX wecom_oauth_states_expiry_idx ON wecom_oauth_states (expires_at) WHERE used_at IS NULL;

CREATE TABLE wecom_follow_relationships (
    corp_id TEXT NOT NULL,
    employee_id TEXT NOT NULL,
    customer_id BIGINT NOT NULL REFERENCES customers(id),
    active BOOLEAN NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (corp_id, employee_id, customer_id),
    CONSTRAINT wecom_follow_relationships_corp_nonempty CHECK (length(corp_id) BETWEEN 1 AND 256),
    CONSTRAINT wecom_follow_relationships_employee_nonempty CHECK (length(employee_id) BETWEEN 1 AND 512)
);
CREATE INDEX wecom_follow_relationships_active_idx ON wecom_follow_relationships (corp_id, employee_id, customer_id) WHERE active;

-- Provider credentials are intentionally absent. Runtime secrets are injected
-- by configuration; this table can only retain opaque metadata if a future
-- adapter needs its provider cache lifecycle audited.
CREATE TABLE wecom_provider_cache_metadata (
    cache_key TEXT PRIMARY KEY,
    version BIGINT NOT NULL DEFAULT 1,
    expires_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT wecom_provider_cache_metadata_key_nonempty CHECK (length(cache_key) BETWEEN 1 AND 200)
);
