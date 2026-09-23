-- Owner: internal/messagearchive. A one-to-one protected compatibility
-- projection for facts present in the frozen legacy archive row, not a second
-- message ledger. Values are read only by the authorized external projection.
CREATE TABLE message_archive_legacy_projections (
    message_id BIGINT PRIMARY KEY REFERENCES message_archive_messages(id) ON DELETE CASCADE,
    historical_unionid TEXT NOT NULL DEFAULT '' CHECK (length(historical_unionid) <= 1024 AND historical_unionid = btrim(historical_unionid)),
    historical_group_name TEXT NOT NULL DEFAULT '' CHECK (length(historical_group_name) <= 512 AND historical_group_name = btrim(historical_group_name)),
    source_projection_digest BYTEA NOT NULL CHECK (octet_length(source_projection_digest) = 32),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
