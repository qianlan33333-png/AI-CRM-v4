-- Migration 0062. Owner: internal/wecom.
-- Provider employee identifiers remain in Access-owned admin_users; this run
-- ledger stores only aggregate counts and a canonical SHA-256 digest. It is
-- forward-only because successful refresh receipts are audit evidence.
CREATE TABLE wecom_staff_directory_refresh_runs (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    run_key TEXT NOT NULL UNIQUE,
    trigger TEXT NOT NULL CHECK (trigger IN ('initial','periodic','manual')),
    state TEXT NOT NULL CHECK (state IN ('running','succeeded','failed_retryable','failed_terminal')),
    attempt_count INTEGER NOT NULL DEFAULT 1 CHECK (attempt_count > 0),
    discovered_count BIGINT NOT NULL DEFAULT 0 CHECK (discovered_count >= 0),
    created_count BIGINT NOT NULL DEFAULT 0 CHECK (created_count >= 0),
    existing_count BIGINT NOT NULL DEFAULT 0 CHECK (existing_count >= 0),
    inactive_count BIGINT NOT NULL DEFAULT 0 CHECK (inactive_count >= 0),
    directory_digest BYTEA CHECK (directory_digest IS NULL OR octet_length(directory_digest)=32),
    last_error_code TEXT NOT NULL DEFAULT '' CHECK (char_length(last_error_code) <= 100),
    started_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT ck_wecom_staff_refresh_completion CHECK (
        (state='succeeded' AND completed_at IS NOT NULL AND directory_digest IS NOT NULL AND last_error_code='') OR
        (state IN ('failed_retryable','failed_terminal') AND completed_at IS NOT NULL AND last_error_code<>'') OR
        (state='running' AND completed_at IS NULL)
    )
);

CREATE INDEX wecom_staff_directory_refresh_recent_idx
    ON wecom_staff_directory_refresh_runs(created_at DESC, id DESC);
