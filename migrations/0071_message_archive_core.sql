-- Owner: internal/messagearchive.  Archive data stays separate from customer,
-- identity, media-library and webhook-owner tables.  Provider reads and all
-- decryption happen before this transaction; a page's facts and cursor commit
-- together or not at all.

CREATE TABLE message_archive_sync_state (
    corp_scope TEXT PRIMARY KEY CHECK (corp_scope ~ '^wecom-corp:.+$'),
    last_seq BIGINT NOT NULL DEFAULT 0 CHECK (last_seq >= 0),
    last_notify_received_at TIMESTAMPTZ,
    last_notify_processed_at TIMESTAMPTZ,
    last_pull_started_at TIMESTAMPTZ,
    last_pull_succeeded_at TIMESTAMPTZ,
    last_error_code TEXT NOT NULL DEFAULT '' CHECK (length(last_error_code) <= 120),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);

CREATE TABLE message_archive_sync_runs (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    corp_scope TEXT NOT NULL REFERENCES message_archive_sync_state(corp_scope) ON DELETE RESTRICT,
    trigger_type TEXT NOT NULL CHECK (trigger_type IN ('notify','manual','bootstrap','migration')),
    webhook_delivery_id BIGINT,
    start_seq BIGINT NOT NULL CHECK (start_seq >= 0),
    end_seq BIGINT NOT NULL CHECK (end_seq >= 0),
    pages INTEGER NOT NULL DEFAULT 0 CHECK (pages >= 0),
    fetched_count INTEGER NOT NULL DEFAULT 0 CHECK (fetched_count >= 0),
    inserted_count INTEGER NOT NULL DEFAULT 0 CHECK (inserted_count >= 0),
    duplicate_count INTEGER NOT NULL DEFAULT 0 CHECK (duplicate_count >= 0),
    unresolved_count INTEGER NOT NULL DEFAULT 0 CHECK (unresolved_count >= 0),
    issue_count INTEGER NOT NULL DEFAULT 0 CHECK (issue_count >= 0),
    status TEXT NOT NULL CHECK (status IN ('running','succeeded','failed')),
    error_code TEXT NOT NULL DEFAULT '' CHECK (length(error_code) <= 120),
    started_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    finished_at TIMESTAMPTZ
);
CREATE INDEX message_archive_sync_runs_scope_started_idx ON message_archive_sync_runs(corp_scope, id DESC);

CREATE TABLE message_archive_messages (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    corp_scope TEXT NOT NULL REFERENCES message_archive_sync_state(corp_scope) ON DELETE RESTRICT,
    seq BIGINT NOT NULL CHECK (seq >= 0),
    msgid TEXT NOT NULL CHECK (length(msgid) BETWEEN 1 AND 512),
    action TEXT NOT NULL DEFAULT '' CHECK (length(action) <= 80),
    msgtype TEXT NOT NULL CHECK (length(msgtype) BETWEEN 1 AND 120),
    conversation_type TEXT NOT NULL CHECK (conversation_type IN ('private','group')),
    roomid TEXT NOT NULL DEFAULT '',
    msgtime_ms BIGINT NOT NULL CHECK (msgtime_ms >= 0),
    occurred_at TIMESTAMPTZ NOT NULL,
    content_text TEXT NOT NULL DEFAULT '',
    normalized_payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    provider_payload JSONB,
    recalled_msgid TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT message_archive_messages_scope_msgid_unique UNIQUE(corp_scope,msgid)
);
CREATE INDEX message_archive_messages_scope_seq_idx ON message_archive_messages(corp_scope,seq);
CREATE INDEX message_archive_messages_occurred_idx ON message_archive_messages(occurred_at DESC,id DESC);
CREATE INDEX message_archive_messages_type_occurred_idx ON message_archive_messages(msgtype,occurred_at DESC,id DESC);
CREATE INDEX message_archive_messages_room_occurred_idx ON message_archive_messages(roomid,occurred_at DESC,id DESC) WHERE roomid <> '';

CREATE TABLE message_archive_participants (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    message_id BIGINT NOT NULL REFERENCES message_archive_messages(id) ON DELETE CASCADE,
    participant_role TEXT NOT NULL CHECK (participant_role IN ('sender','recipient','subject')),
    actor_type TEXT NOT NULL CHECK (actor_type IN ('staff','external_customer','robot','unknown')),
    provider_value TEXT NOT NULL DEFAULT '',
    provider_value_digest BYTEA NOT NULL CHECK (octet_length(provider_value_digest)=32),
    staff_user_id BIGINT REFERENCES admin_users(id) ON DELETE RESTRICT,
    customer_id_at_ingest BIGINT REFERENCES customers(id) ON DELETE RESTRICT,
    identity_id_at_ingest BIGINT REFERENCES customer_identities(id) ON DELETE RESTRICT,
    resolution_status TEXT NOT NULL CHECK (resolution_status IN ('found','not_found','conflict','not_applicable')),
    resolution_reason TEXT NOT NULL DEFAULT '' CHECK (length(resolution_reason) <= 120),
    resolved_at TIMESTAMPTZ,
    CONSTRAINT message_archive_participant_unique UNIQUE(message_id,participant_role,provider_value_digest),
    CONSTRAINT message_archive_participant_found_customer CHECK (
        (resolution_status='found' AND customer_id_at_ingest IS NOT NULL)
        OR (resolution_status<>'found' AND customer_id_at_ingest IS NULL)
    )
);
CREATE INDEX message_archive_participants_customer_idx ON message_archive_participants(customer_id_at_ingest,message_id) WHERE customer_id_at_ingest IS NOT NULL;
CREATE INDEX message_archive_participants_resolution_idx ON message_archive_participants(resolution_status,id) WHERE resolution_status IN ('not_found','conflict');

CREATE TABLE message_archive_media (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    message_id BIGINT NOT NULL REFERENCES message_archive_messages(id) ON DELETE CASCADE,
    media_kind TEXT NOT NULL CHECK (length(media_kind) BETWEEN 1 AND 80),
    provider_file_ref TEXT NOT NULL,
    provider_file_digest BYTEA NOT NULL CHECK (octet_length(provider_file_digest)=32),
    expected_md5 TEXT NOT NULL DEFAULT '',
    actual_md5 TEXT NOT NULL DEFAULT '',
    expected_size BIGINT,
    actual_size BIGINT,
    storage_key TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL CHECK (status IN ('ready','provider_unavailable','invalid','pending')),
    error_code TEXT NOT NULL DEFAULT '' CHECK (length(error_code) <= 120),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT message_archive_media_message_provider_unique UNIQUE(message_id,provider_file_digest)
);

CREATE TABLE message_archive_ingest_issues (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    corp_scope TEXT NOT NULL REFERENCES message_archive_sync_state(corp_scope) ON DELETE RESTRICT,
    seq BIGINT NOT NULL CHECK (seq >= 0),
    msgid TEXT NOT NULL DEFAULT '',
    stage TEXT NOT NULL CHECK (length(stage) BETWEEN 1 AND 80),
    reason_code TEXT NOT NULL CHECK (length(reason_code) BETWEEN 1 AND 120),
    payload_digest BYTEA NOT NULL CHECK (octet_length(payload_digest)=32),
    protected_payload BYTEA,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT message_archive_ingest_issues_unique UNIQUE(corp_scope,seq,stage,payload_digest)
);

CREATE TABLE message_archive_resolution_attempts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    participant_id BIGINT NOT NULL REFERENCES message_archive_participants(id) ON DELETE RESTRICT,
    attempt_source TEXT NOT NULL CHECK (attempt_source IN ('ingest','manual','migration')),
    previous_status TEXT NOT NULL DEFAULT '',
    new_status TEXT NOT NULL CHECK (new_status IN ('found','not_found','conflict','not_applicable')),
    identity_id BIGINT REFERENCES customer_identities(id) ON DELETE RESTRICT,
    customer_id BIGINT REFERENCES customers(id) ON DELETE RESTRICT,
    reason_code TEXT NOT NULL DEFAULT '' CHECK (length(reason_code) <= 120),
    operator_id BIGINT REFERENCES admin_users(id) ON DELETE RESTRICT,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
