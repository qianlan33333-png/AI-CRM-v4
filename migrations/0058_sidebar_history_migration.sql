-- Owner: migration boundary. Source identities are never persisted here.
CREATE TABLE sidebar_history_migration_batches (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    run_key TEXT NOT NULL UNIQUE CHECK (char_length(run_key) BETWEEN 8 AND 200),
    manifest_digest BYTEA NOT NULL CHECK (octet_length(manifest_digest)=32),
    source_system TEXT NOT NULL CHECK (char_length(source_system) BETWEEN 1 AND 80),
    input_count BIGINT NOT NULL CHECK (input_count >= 0),
    imported_count BIGINT NOT NULL DEFAULT 0 CHECK (imported_count >= 0),
    replayed_count BIGINT NOT NULL DEFAULT 0 CHECK (replayed_count >= 0),
    quarantined_count BIGINT NOT NULL DEFAULT 0 CHECK (quarantined_count >= 0),
    status TEXT NOT NULL CHECK (status IN ('applying','applied','reconciled')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    completed_at TIMESTAMPTZ
);

CREATE TABLE sidebar_history_migration_source_map (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    batch_id BIGINT NOT NULL REFERENCES sidebar_history_migration_batches(id) ON DELETE RESTRICT,
    source_kind TEXT NOT NULL CHECK (source_kind IN ('service_period_entitlement','coupon_claim')),
    source_key TEXT NOT NULL CHECK (char_length(source_key) BETWEEN 1 AND 200),
    source_digest BYTEA NOT NULL CHECK (octet_length(source_digest)=32),
    customer_id BIGINT NOT NULL REFERENCES customers(id) ON DELETE RESTRICT,
    target_table TEXT NOT NULL CHECK (target_table IN ('order_service_entitlements','coupon_customer_claims')),
    target_id BIGINT NOT NULL CHECK (target_id > 0),
    disposition TEXT NOT NULL CHECK (disposition IN ('imported','replayed')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE(batch_id,source_kind,source_key)
);

CREATE TABLE sidebar_history_migration_quarantine (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    batch_id BIGINT NOT NULL REFERENCES sidebar_history_migration_batches(id) ON DELETE RESTRICT,
    source_kind TEXT NOT NULL CHECK (source_kind IN ('service_period_entitlement','coupon_claim')),
    source_key TEXT NOT NULL CHECK (char_length(source_key) BETWEEN 1 AND 200),
    source_digest BYTEA NOT NULL CHECK (octet_length(source_digest)=32),
    subject_digest BYTEA NOT NULL CHECK (octet_length(subject_digest)=32),
    reason TEXT NOT NULL CHECK (reason IN ('identity_not_found','identity_conflict','definition_not_mapped','invalid_source_row')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE(batch_id,source_kind,source_key)
);
