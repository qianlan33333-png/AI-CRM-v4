-- Owner: Identity. Original declared-phone receipts remain immutable.
-- One source can have one audited canonical recovery, including a conflict.
CREATE TABLE identity_phone_canonical_recoveries (
 original_key_digest BYTEA PRIMARY KEY REFERENCES identity_declared_phone_receipts(key_digest) ON DELETE RESTRICT,
 original_payload_digest BYTEA NOT NULL CHECK (octet_length(original_payload_digest)=32),
 original_customer_id BIGINT NOT NULL REFERENCES customers(id) ON DELETE RESTRICT,
 target_customer_id BIGINT NOT NULL REFERENCES customers(id) ON DELETE RESTRICT,
 target_lineage_version BIGINT NOT NULL CHECK (target_lineage_version>0),
 lineage_digest BYTEA NOT NULL CHECK (octet_length(lineage_digest)=32),
 merge_ids BIGINT[] NOT NULL CHECK (cardinality(merge_ids)>0),
 recovery_key_digest BYTEA NOT NULL UNIQUE REFERENCES identity_declared_phone_receipts(key_digest) ON DELETE RESTRICT,
 created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
 CHECK (original_customer_id<>target_customer_id)
);
