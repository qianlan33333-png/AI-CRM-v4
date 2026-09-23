-- Owner: internal/access. Machine clients are independent of admin sessions
-- and can never inherit an administrator role. Secret material is Argon2id
-- hashed and cannot be recovered by the UI or historical importer.

CREATE TABLE access_machine_clients (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    client_id TEXT NOT NULL UNIQUE CHECK (client_id ~ '^[A-Za-z0-9][A-Za-z0-9_.-]{2,119}$'),
    display_name TEXT NOT NULL CHECK (length(btrim(display_name)) BETWEEN 1 AND 160),
    purpose TEXT NOT NULL CHECK (purpose IN ('external_agent', 'mcp', 'direct_api_key', 'identity', 'group_broadcast', 'campaign_agent', 'ops_reporter', 'operation_runner')),
    secret_hash TEXT NOT NULL CHECK (secret_hash LIKE '$argon2id$%'),
    credential_hint TEXT NOT NULL CHECK (length(credential_hint) BETWEEN 4 AND 40),
    audiences TEXT[] NOT NULL CHECK (cardinality(audiences) BETWEEN 1 AND 16),
    scopes TEXT[] NOT NULL CHECK (cardinality(scopes) BETWEEN 1 AND 16),
    allowed_cidrs CIDR[] NOT NULL DEFAULT '{}',
    corp_id TEXT NOT NULL DEFAULT '',
    owner_scope JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(owner_scope) = 'object'),
    token_ttl_seconds INTEGER NOT NULL CHECK (token_ttl_seconds BETWEEN 60 AND 3600),
    expires_at TIMESTAMPTZ,
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    reissue_required BOOLEAN NOT NULL DEFAULT FALSE,
    auth_version BIGINT NOT NULL DEFAULT 1 CHECK (auth_version > 0),
    last_used_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);

CREATE TABLE access_machine_client_grants (
    machine_client_id BIGINT NOT NULL REFERENCES access_machine_clients(id) ON DELETE CASCADE,
    capability TEXT NOT NULL CHECK (length(capability) BETWEEN 3 AND 120),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (machine_client_id, capability)
);

CREATE TABLE access_machine_audit (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    machine_client_id BIGINT NOT NULL REFERENCES access_machine_clients(id),
    actor_admin_user_id BIGINT REFERENCES admin_users(id),
    action TEXT NOT NULL CHECK (length(action) BETWEEN 3 AND 120),
    outcome TEXT NOT NULL CHECK (length(outcome) BETWEEN 2 AND 80),
    details JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(details) = 'object'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX ix_access_machine_audit_client_created ON access_machine_audit(machine_client_id, created_at DESC, id DESC);

-- Each protected source snapshot has its own batch identity. SourceRevision is
-- donor-code provenance only: two read-only factual snapshots may legitimately
-- come from the same frozen revision.
CREATE TABLE access_machine_import_batches (
    import_run_id TEXT PRIMARY KEY CHECK (import_run_id ~ '^open-platform:[a-f0-9]{32}$'),
    source_system TEXT NOT NULL CHECK (source_system = 'ai-crm'),
    source_revision TEXT NOT NULL CHECK (source_revision ~ '^[a-f0-9]{40}$'),
    manifest_digest BYTEA NOT NULL CHECK (octet_length(manifest_digest) = 32),
    snapshot_at TIMESTAMPTZ NOT NULL,
    client_count INTEGER NOT NULL CHECK (client_count >= 0),
    audit_count INTEGER NOT NULL CHECK (audit_count >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (source_system, manifest_digest)
);

-- These global receipts are keyed by the donor's actual source namespace and
-- record identity. A later snapshot may replay exactly the same record, but a
-- different digest for the same record is a hard migration conflict.
CREATE TABLE access_machine_import_receipts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    import_run_id TEXT NOT NULL REFERENCES access_machine_import_batches(import_run_id) ON DELETE RESTRICT,
    source_system TEXT NOT NULL CHECK (source_system = 'ai-crm'),
    source_scope TEXT NOT NULL CHECK (length(source_scope) BETWEEN 1 AND 320),
    source_row_id TEXT NOT NULL CHECK (length(source_row_id) BETWEEN 1 AND 240),
    source_row_digest BYTEA NOT NULL CHECK (octet_length(source_row_digest) = 32),
    source_owner_scope_digest BYTEA NOT NULL CHECK (octet_length(source_owner_scope_digest) = 32),
    owner_scope_mapping_status TEXT NOT NULL CHECK (owner_scope_mapping_status IN ('not_required', 'mapped', 'pending', 'incompatible_corp')),
    source_client_id TEXT NOT NULL CHECK (length(source_client_id) BETWEEN 1 AND 120),
    source_principal_id TEXT NOT NULL DEFAULT '' CHECK (length(source_principal_id) <= 240),
    source_principal_type TEXT NOT NULL DEFAULT '' CHECK (length(source_principal_type) <= 80),
    source_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    source_auth_version BIGINT NOT NULL DEFAULT 1 CHECK (source_auth_version > 0),
    machine_client_id BIGINT REFERENCES access_machine_clients(id),
    outcome TEXT NOT NULL CHECK (outcome IN ('reissue_required', 'excluded')),
    reason_code TEXT NOT NULL DEFAULT '' CHECK (length(reason_code) <= 120),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (source_system, source_scope, source_row_id),
    CHECK ((outcome = 'reissue_required' AND machine_client_id IS NOT NULL AND reason_code = '')
        OR (outcome = 'excluded' AND machine_client_id IS NULL AND reason_code <> ''))
);
CREATE INDEX ix_access_machine_import_receipts_client ON access_machine_import_receipts(machine_client_id) WHERE machine_client_id IS NOT NULL;

-- Every sealed snapshot receives a row-level receipt too. It makes overlap,
-- replay and drift reviewable without allowing a new batch to recreate an
-- existing credential.
CREATE TABLE access_machine_import_batch_receipts (
    import_run_id TEXT NOT NULL REFERENCES access_machine_import_batches(import_run_id) ON DELETE RESTRICT,
    source_system TEXT NOT NULL CHECK (source_system = 'ai-crm'),
    source_scope TEXT NOT NULL CHECK (length(source_scope) BETWEEN 1 AND 320),
    source_row_id TEXT NOT NULL CHECK (length(source_row_id) BETWEEN 1 AND 240),
    source_row_digest BYTEA NOT NULL CHECK (octet_length(source_row_digest) = 32),
    replayed BOOLEAN NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (import_run_id, source_scope, source_row_id)
);

-- Legacy actions are global immutable source facts. The encrypted snapshot
-- retains the protected source payload; this table retains only canonical
-- before/after digests and original action metadata.
CREATE TABLE access_machine_historical_audit_facts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    import_run_id TEXT NOT NULL REFERENCES access_machine_import_batches(import_run_id) ON DELETE RESTRICT,
    source_system TEXT NOT NULL CHECK (source_system = 'ai-crm'),
    source_scope TEXT NOT NULL CHECK (length(source_scope) BETWEEN 1 AND 320),
    source_audit_id BIGINT NOT NULL CHECK (source_audit_id > 0),
    source_row_digest BYTEA NOT NULL CHECK (octet_length(source_row_digest) = 32),
    source_operator TEXT NOT NULL CHECK (length(source_operator) <= 240),
    source_action TEXT NOT NULL CHECK (length(source_action) <= 240),
    source_target_type TEXT NOT NULL CHECK (source_target_type = 'api_client'),
    source_target_id TEXT NOT NULL CHECK (length(source_target_id) <= 240),
    before_payload_digest BYTEA NOT NULL CHECK (octet_length(before_payload_digest) = 32),
    after_payload_digest BYTEA NOT NULL CHECK (octet_length(after_payload_digest) = 32),
    occurred_at TIMESTAMPTZ NOT NULL,
    imported_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (source_system, source_scope, source_audit_id)
);
CREATE INDEX ix_access_machine_historical_audit_target ON access_machine_historical_audit_facts(source_system, source_scope, source_target_id, source_audit_id);

CREATE TABLE access_machine_historical_audit_batch_receipts (
    import_run_id TEXT NOT NULL REFERENCES access_machine_import_batches(import_run_id) ON DELETE RESTRICT,
    source_system TEXT NOT NULL CHECK (source_system = 'ai-crm'),
    source_scope TEXT NOT NULL CHECK (length(source_scope) BETWEEN 1 AND 320),
    source_audit_id BIGINT NOT NULL CHECK (source_audit_id > 0),
    source_row_digest BYTEA NOT NULL CHECK (octet_length(source_row_digest) = 32),
    replayed BOOLEAN NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (import_run_id, source_scope, source_audit_id)
);
