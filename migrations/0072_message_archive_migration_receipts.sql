-- Owner: internal/messagearchive.  The import ledger is source-row granular;
-- this PR deliberately does not stop an old writer or initialise production
-- cursors from a live source.

CREATE TABLE message_archive_migration_runs (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    source_name TEXT NOT NULL CHECK (length(source_name) BETWEEN 1 AND 120),
    mode TEXT NOT NULL CHECK (mode IN ('inspect','dry_run','apply','reconcile')),
    status TEXT NOT NULL CHECK (status IN ('running','succeeded','failed')),
    source_digest BYTEA NOT NULL CHECK (octet_length(source_digest)=32),
    started_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    finished_at TIMESTAMPTZ,
    error_code TEXT NOT NULL DEFAULT '' CHECK (length(error_code) <= 120)
);

CREATE TABLE message_archive_migration_receipts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    migration_run_id BIGINT NOT NULL REFERENCES message_archive_migration_runs(id) ON DELETE RESTRICT,
    source_row_key TEXT NOT NULL CHECK (length(source_row_key) BETWEEN 1 AND 512),
    source_digest BYTEA NOT NULL CHECK (octet_length(source_digest)=32),
    source_msgid TEXT NOT NULL DEFAULT '',
    source_seq BIGINT,
    target_message_id BIGINT REFERENCES message_archive_messages(id) ON DELETE RESTRICT,
    outcome TEXT NOT NULL CHECK (outcome IN ('inserted','duplicate','unresolved','quarantined')),
    reason_code TEXT NOT NULL DEFAULT '' CHECK (length(reason_code) <= 120),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT message_archive_migration_receipts_run_row_unique UNIQUE(migration_run_id,source_row_key)
);
CREATE INDEX message_archive_migration_receipts_run_outcome_idx ON message_archive_migration_receipts(migration_run_id,outcome);
