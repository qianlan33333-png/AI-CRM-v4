-- Owner: identity.
-- HXC identity values remain encrypted source observations until they can be
-- attached to an existing Customer root. This migration never provisions a
-- Customer and is forward-only.

CREATE TABLE identity_source_subjects (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    source_system TEXT NOT NULL CHECK (source_system = 'hxc'),
    subject_digest BYTEA NOT NULL CHECK (octet_length(subject_digest) = 32),
    status TEXT NOT NULL CHECK (status IN ('pending','matched','conflict','invalid','retired')),
    customer_id BIGINT REFERENCES customers(id) ON DELETE RESTRICT,
    matched_by TEXT NOT NULL CHECK (matched_by IN ('none','unionid','phone','both')),
    reason_code TEXT NOT NULL CHECK (reason_code <> '' AND length(reason_code) <= 80),
    latest_payload_digest BYTEA NOT NULL CHECK (octet_length(latest_payload_digest) = 32),
    source_updated_at TIMESTAMPTZ NOT NULL,
    first_seen_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    missed_complete_snapshots SMALLINT NOT NULL DEFAULT 0 CHECK (missed_complete_snapshots BETWEEN 0 AND 2),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    UNIQUE (source_system, subject_digest),
    CHECK ((status = 'matched' AND customer_id IS NOT NULL AND matched_by <> 'none')
        OR (status <> 'matched' AND customer_id IS NULL AND matched_by = 'none'))
);

CREATE INDEX identity_source_subjects_status_idx
    ON identity_source_subjects(source_system, status, id);
CREATE INDEX identity_source_subjects_customer_idx
    ON identity_source_subjects(customer_id) WHERE customer_id IS NOT NULL;

CREATE TABLE identity_source_observations (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    subject_id BIGINT NOT NULL REFERENCES identity_source_subjects(id) ON DELETE RESTRICT,
    kind TEXT NOT NULL CHECK (kind IN ('unionid','phone')),
    scope_key TEXT NOT NULL CHECK (
        (kind = 'unionid' AND left(scope_key, 21) = 'wechat-open-platform:' AND length(scope_key) > 21)
        OR (kind = 'phone' AND scope_key = 'phone:cn11')
    ),
    lookup_digest BYTEA NOT NULL CHECK (octet_length(lookup_digest) = 32),
    ciphertext BYTEA NOT NULL CHECK (octet_length(ciphertext) >= 29),
    display_value TEXT NOT NULL CHECK (display_value <> '' AND length(display_value) <= 32),
    key_version SMALLINT NOT NULL CHECK (key_version > 0),
    assurance TEXT NOT NULL CHECK (assurance IN ('verified','declared')),
    status TEXT NOT NULL CHECK (status IN ('active','retired')),
    customer_identity_id BIGINT REFERENCES customer_identities(id) ON DELETE RESTRICT,
    source_updated_at TIMESTAMPTZ NOT NULL,
    first_seen_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0)
);

CREATE UNIQUE INDEX identity_source_observations_one_active_kind_idx
    ON identity_source_observations(subject_id, kind) WHERE status = 'active';
CREATE INDEX identity_source_observations_lookup_idx
    ON identity_source_observations(kind, scope_key, lookup_digest) WHERE status = 'active';

CREATE TABLE identity_source_conflicts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    subject_id BIGINT NOT NULL REFERENCES identity_source_subjects(id) ON DELETE RESTRICT,
    reason_code TEXT NOT NULL CHECK (reason_code <> '' AND length(reason_code) <= 80),
    left_customer_id BIGINT REFERENCES customers(id) ON DELETE RESTRICT,
    right_customer_id BIGINT REFERENCES customers(id) ON DELETE RESTRICT,
    merge_candidate_id BIGINT REFERENCES customer_merge_candidates(id) ON DELETE RESTRICT,
    evidence_digest BYTEA NOT NULL CHECK (octet_length(evidence_digest) = 32),
    status TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open','resolved','ignored')),
    resolution_code TEXT NOT NULL DEFAULT '' CHECK (length(resolution_code) <= 80),
    resolved_by TEXT NOT NULL DEFAULT '' CHECK (length(resolved_by) <= 200),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    resolved_at TIMESTAMPTZ,
    CHECK (left_customer_id IS NULL OR right_customer_id IS NULL OR left_customer_id <> right_customer_id),
    CHECK ((status = 'open' AND resolution_code = '' AND resolved_by = '' AND resolved_at IS NULL)
        OR (status <> 'open' AND resolution_code <> '' AND resolved_by <> '' AND resolved_at IS NOT NULL))
);

CREATE UNIQUE INDEX identity_source_conflicts_one_open_reason_idx
    ON identity_source_conflicts(subject_id, reason_code) WHERE status = 'open';
CREATE INDEX identity_source_conflicts_status_idx
    ON identity_source_conflicts(status, id);

CREATE TABLE identity_source_resolution_receipts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    key_digest BYTEA NOT NULL UNIQUE CHECK (octet_length(key_digest) = 32),
    payload_digest BYTEA NOT NULL CHECK (octet_length(payload_digest) = 32),
    subject_id BIGINT NOT NULL REFERENCES identity_source_subjects(id) ON DELETE RESTRICT,
    rule_version TEXT NOT NULL CHECK (rule_version <> '' AND length(rule_version) <= 80),
    disposition TEXT NOT NULL CHECK (disposition IN ('matched','unmatched','conflict','invalid')),
    matched_by TEXT NOT NULL CHECK (matched_by IN ('none','unionid','phone','both')),
    reason_code TEXT NOT NULL CHECK (reason_code <> '' AND length(reason_code) <= 80),
    customer_id BIGINT REFERENCES customers(id) ON DELETE RESTRICT,
    conflict_id BIGINT REFERENCES identity_source_conflicts(id) ON DELETE RESTRICT,
    merge_candidate_id BIGINT REFERENCES customer_merge_candidates(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CHECK ((disposition = 'matched' AND customer_id IS NOT NULL AND matched_by <> 'none')
        OR (disposition <> 'matched' AND customer_id IS NULL AND matched_by = 'none'))
);

CREATE INDEX identity_source_resolution_receipts_subject_idx
    ON identity_source_resolution_receipts(subject_id, id DESC);

CREATE FUNCTION identity_source_receipts_reject_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'identity source receipts are append-only';
END;
$$;

CREATE TRIGGER identity_source_resolution_receipts_append_only
    BEFORE UPDATE OR DELETE OR TRUNCATE ON identity_source_resolution_receipts
    FOR EACH STATEMENT EXECUTE FUNCTION identity_source_receipts_reject_mutation();

CREATE TABLE identity_source_conflict_actions (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    conflict_id BIGINT NOT NULL REFERENCES identity_source_conflicts(id) ON DELETE RESTRICT,
    key_digest BYTEA NOT NULL UNIQUE CHECK (octet_length(key_digest) = 32),
    payload_digest BYTEA NOT NULL CHECK (octet_length(payload_digest) = 32),
    action TEXT NOT NULL CHECK (action = 'ignore'),
    reason_code TEXT NOT NULL CHECK (reason_code IN ('not_same_person','shared_phone','source_data_error','accepted_risk')),
    actor TEXT NOT NULL CHECK (actor <> '' AND length(actor) <= 200),
    outcome TEXT NOT NULL CHECK (outcome IN ('ignored','replayed')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TRIGGER identity_source_conflict_actions_append_only
    BEFORE UPDATE OR DELETE OR TRUNCATE ON identity_source_conflict_actions
    FOR EACH STATEMENT EXECUTE FUNCTION identity_source_receipts_reject_mutation();
