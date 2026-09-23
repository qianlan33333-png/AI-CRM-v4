-- Owner: internal/coupon. This is the canonical Customer projection of a
-- coupon claim. Raw channel identities remain in Identity and are resolved by
-- the migration adapter before insertion.
CREATE TABLE coupon_customer_claims (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    source_system TEXT NOT NULL CHECK (char_length(source_system) BETWEEN 1 AND 80),
    source_key TEXT NOT NULL CHECK (char_length(source_key) BETWEEN 1 AND 200),
    customer_id BIGINT NOT NULL REFERENCES customers(id) ON DELETE RESTRICT,
    coupon_id BIGINT NOT NULL REFERENCES coupon_rules(id) ON DELETE RESTRICT,
    status TEXT NOT NULL CHECK (status IN ('claimed','reserved','redeemed','expired','cancelled')),
    claim_no_masked TEXT NOT NULL DEFAULT '' CHECK (char_length(claim_no_masked) <= 80),
    claimed_at TIMESTAMPTZ NOT NULL,
    valid_from TIMESTAMPTZ,
    valid_until TIMESTAMPTZ,
    redeemed_at TIMESTAMPTZ,
    source_digest BYTEA NOT NULL CHECK (octet_length(source_digest)=32),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    UNIQUE(source_system, source_key),
    CHECK (valid_until IS NULL OR valid_from IS NULL OR valid_until >= valid_from)
);
CREATE INDEX coupon_customer_claims_customer_idx
    ON coupon_customer_claims(customer_id, claimed_at DESC, id DESC);
