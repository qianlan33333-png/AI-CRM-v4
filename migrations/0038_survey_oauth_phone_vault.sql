-- Owners: identity (phone vault and attach receipts), survey (binding receipts).
-- Retention: encrypted phone identities and binding receipts are durable audit
-- records. Forward-only: rollback disables writers; it never restores plaintext.

ALTER TABLE customer_identities
    ADD COLUMN normalized_value_digest BYTEA;

ALTER TABLE customer_identities DROP CONSTRAINT ck_customer_identities_nonempty;
ALTER TABLE customer_identities DROP CONSTRAINT ck_customer_identities_kind_scope;
ALTER TABLE customer_identities ADD CONSTRAINT ck_customer_identities_nonempty CHECK (
    kind <> '' AND scope_key <> '' AND source <> ''
    AND length(scope_key) <= 256 AND length(normalized_value) <= 1024 AND length(source) <= 128
    AND scope_key !~ '[[:space:][:cntrl:]]'
    AND normalized_value !~ '[[:cntrl:]]'
    AND source !~ '[[:space:][:cntrl:]]'
    AND (
        (kind <> 'phone' AND normalized_value <> '' AND normalized_value_digest IS NULL)
        OR (kind = 'phone' AND scope_key = 'phone:e164' AND normalized_value <> '')
        OR (kind = 'phone' AND scope_key = 'phone:cn11' AND normalized_value = '' AND octet_length(normalized_value_digest) = 32)
    )
);
ALTER TABLE customer_identities ADD CONSTRAINT ck_customer_identities_kind_scope CHECK (
    (kind = 'wecom_external_userid' AND left(scope_key, 11) = 'wecom-corp:' AND length(scope_key) > 11)
    OR (kind = 'unionid' AND left(scope_key, 21) = 'wechat-open-platform:' AND length(scope_key) > 21)
    OR (kind IN ('mp_openid', 'oa_openid') AND left(scope_key, 11) = 'wechat-app:' AND length(scope_key) > 11)
    OR (kind IN ('alipay_user_id','alipay_oauth_user_id','alipay_oauth_open_id','alipay_buyer_id','alipay_buyer_open_id') AND left(scope_key, 11) = 'alipay-app:' AND length(scope_key) > 11)
    OR (kind = 'first_party_member_id' AND left(scope_key, 12) = 'first-party:' AND length(scope_key) > 12)
    OR (kind = 'phone' AND scope_key IN ('phone:e164','phone:cn11'))
    OR (kind = 'ext' AND left(scope_key, 4) = 'ext:' AND length(scope_key) > 4)
);

DROP INDEX ux_customer_identities_active_key;
CREATE UNIQUE INDEX ux_customer_identities_active_key
    ON customer_identities(kind, scope_key, normalized_value)
    WHERE status = 'active' AND kind <> 'phone';
CREATE UNIQUE INDEX ux_customer_phone_identities_active_digest
    ON customer_identities(scope_key, normalized_value_digest)
    WHERE status = 'active' AND kind = 'phone' AND normalized_value_digest IS NOT NULL;
CREATE UNIQUE INDEX ux_customer_phone_identities_active_legacy
    ON customer_identities(scope_key, normalized_value)
    WHERE status = 'active' AND kind = 'phone' AND scope_key = 'phone:e164';

CREATE TABLE identity_phone_secrets (
    identity_id BIGINT PRIMARY KEY REFERENCES customer_identities(id) ON DELETE RESTRICT,
    ciphertext BYTEA NOT NULL CHECK (octet_length(ciphertext) >= 39),
    masked_value TEXT NOT NULL CHECK (masked_value ~ '^1[3-9][0-9][*]{4}[0-9]{4}$'),
    key_version SMALLINT NOT NULL CHECK (key_version > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE identity_declared_phone_receipts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    key_digest BYTEA NOT NULL UNIQUE CHECK (octet_length(key_digest) = 32),
    payload_digest BYTEA NOT NULL CHECK (octet_length(payload_digest) = 32),
    customer_id BIGINT REFERENCES customers(id) ON DELETE RESTRICT,
    identity_id BIGINT REFERENCES customer_identities(id) ON DELETE RESTRICT,
    outcome TEXT NOT NULL CHECK (outcome IN ('attached','already_linked','conflict','invalid')),
    source TEXT NOT NULL CHECK (source <> '' AND length(source) <= 128),
    source_event_id TEXT NOT NULL CHECK (source_event_id <> '' AND length(source_event_id) <= 200),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX identity_declared_phone_receipts_customer_idx ON identity_declared_phone_receipts(customer_id, created_at DESC);

CREATE TABLE identity_phone_vault_migration_runs (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    run_key TEXT NOT NULL UNIQUE CHECK (run_key <> '' AND length(run_key) <= 128),
    source_count BIGINT NOT NULL CHECK (source_count >= 0),
    migrated_count BIGINT NOT NULL DEFAULT 0 CHECK (migrated_count >= 0),
    customer_count_before BIGINT NOT NULL CHECK (customer_count_before >= 0),
    customer_count_after BIGINT CHECK (customer_count_after >= 0),
    status TEXT NOT NULL CHECK (status IN ('applying','applied')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    completed_at TIMESTAMPTZ
);

CREATE TABLE survey_phone_binding_receipts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    submission_id BIGINT NOT NULL REFERENCES survey_submissions(id) ON DELETE RESTRICT,
    answer_id BIGINT NOT NULL UNIQUE REFERENCES survey_submission_answers(id) ON DELETE RESTRICT,
    customer_id BIGINT NOT NULL REFERENCES customers(id) ON DELETE RESTRICT,
    identity_id BIGINT REFERENCES customer_identities(id) ON DELETE RESTRICT,
    status TEXT NOT NULL CHECK (status IN ('attached','already_linked','conflict','invalid','replayed')),
    evidence_digest BYTEA NOT NULL CHECK (octet_length(evidence_digest) = 32),
    created_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX survey_phone_binding_receipts_status_idx ON survey_phone_binding_receipts(status, created_at DESC);
