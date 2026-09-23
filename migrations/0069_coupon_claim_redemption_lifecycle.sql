-- Owner: internal/coupon.
--
-- Extends the existing canonical Customer claim projection with the local
-- claim, checkout reservation and redemption lifecycle. It contains only
-- customers.id and local commerce facts; channel identities remain owned by
-- Identity and payment settlement remains owned by Payment.

ALTER TABLE coupon_customer_claims
    DROP CONSTRAINT coupon_customer_claims_status_check;
ALTER TABLE coupon_customer_claims
    ADD CONSTRAINT coupon_customer_claims_status_check
    CHECK (status IN ('claimed','available','reserved','redeemed','expired','cancelled'));

ALTER TABLE coupon_customer_claims
    ADD COLUMN IF NOT EXISTS reserved_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS expired_at TIMESTAMPTZ;

CREATE INDEX coupon_customer_claims_coupon_customer_idx
    ON coupon_customer_claims(coupon_id, customer_id, claimed_at, id);
CREATE INDEX coupon_customer_claims_checkout_idx
    ON coupon_customer_claims(customer_id, status, valid_until, claimed_at, id);

CREATE TABLE coupon_claim_operation_receipts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    operation TEXT NOT NULL CHECK (operation='claim'),
    actor_scope TEXT NOT NULL CHECK (char_length(actor_scope) BETWEEN 1 AND 200),
    key_digest BYTEA NOT NULL CHECK (octet_length(key_digest)=32),
    payload_digest BYTEA NOT NULL CHECK (octet_length(payload_digest)=32),
    claim_id BIGINT NOT NULL REFERENCES coupon_customer_claims(id) ON DELETE RESTRICT,
    result_snapshot JSONB NOT NULL CHECK (jsonb_typeof(result_snapshot)='object'),
    created_at TIMESTAMPTZ NOT NULL,
    UNIQUE(operation, actor_scope, key_digest)
);

CREATE TABLE coupon_order_redemptions (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    claim_id BIGINT NOT NULL REFERENCES coupon_customer_claims(id) ON DELETE RESTRICT,
    order_reference TEXT NOT NULL CHECK (char_length(order_reference) BETWEEN 1 AND 200),
    product_id BIGINT NOT NULL CHECK (product_id>0),
    product_type TEXT NOT NULL CHECK (product_type IN ('standard_product','service_period')),
    product_code TEXT NOT NULL CHECK (char_length(product_code) BETWEEN 1 AND 200),
    rule_version BIGINT NOT NULL CHECK (rule_version>0),
    gross_amount_minor BIGINT NOT NULL CHECK (gross_amount_minor>0),
    discount_amount_minor BIGINT NOT NULL CHECK (discount_amount_minor>0 AND discount_amount_minor<gross_amount_minor),
    payable_amount_minor BIGINT NOT NULL CHECK (payable_amount_minor=gross_amount_minor-discount_amount_minor),
    currency TEXT NOT NULL CHECK (currency='CNY'),
    status TEXT NOT NULL CHECK (status IN ('reserved','consumed','released')),
    reserved_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    released_at TIMESTAMPTZ,
    release_reason TEXT NOT NULL DEFAULT '' CHECK (char_length(release_reason)<=120),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    UNIQUE(order_reference),
    CHECK ((status='reserved' AND consumed_at IS NULL AND released_at IS NULL)
        OR (status='consumed' AND consumed_at IS NOT NULL AND released_at IS NULL)
        OR (status='released' AND consumed_at IS NULL AND released_at IS NOT NULL))
);

CREATE UNIQUE INDEX coupon_order_redemptions_one_active_claim
    ON coupon_order_redemptions(claim_id) WHERE status IN ('reserved','consumed');
CREATE INDEX coupon_order_redemptions_claim_idx
    ON coupon_order_redemptions(claim_id, id DESC);

CREATE TABLE coupon_redemption_operation_receipts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    operation TEXT NOT NULL CHECK (operation IN ('reserve','consume','release')),
    actor_scope TEXT NOT NULL CHECK (char_length(actor_scope) BETWEEN 1 AND 200),
    key_digest BYTEA NOT NULL CHECK (octet_length(key_digest)=32),
    payload_digest BYTEA NOT NULL CHECK (octet_length(payload_digest)=32),
    redemption_id BIGINT NOT NULL REFERENCES coupon_order_redemptions(id) ON DELETE RESTRICT,
    result_snapshot JSONB NOT NULL CHECK (jsonb_typeof(result_snapshot)='object'),
    created_at TIMESTAMPTZ NOT NULL,
    UNIQUE(operation, actor_scope, key_digest),
    UNIQUE(operation, redemption_id)
);

CREATE TABLE coupon_claim_audit_events (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    claim_id BIGINT NOT NULL REFERENCES coupon_customer_claims(id) ON DELETE RESTRICT,
    redemption_id BIGINT REFERENCES coupon_order_redemptions(id) ON DELETE RESTRICT,
    operation TEXT NOT NULL CHECK (operation IN ('claim','reserve','consume','release')),
    actor_scope_digest BYTEA NOT NULL CHECK (octet_length(actor_scope_digest)=32),
    payload_digest BYTEA NOT NULL CHECK (octet_length(payload_digest)=32),
    occurred_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE coupon_claim_outbox (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    event_type TEXT NOT NULL CHECK (event_type IN ('coupon.claimed.v1','coupon.reserved.v1','coupon.consumed.v1','coupon.released.v1')),
    claim_id BIGINT NOT NULL REFERENCES coupon_customer_claims(id) ON DELETE RESTRICT,
    redemption_id BIGINT REFERENCES coupon_order_redemptions(id) ON DELETE RESTRICT,
    payload JSONB NOT NULL CHECK (jsonb_typeof(payload)='object'),
    idempotency_digest BYTEA NOT NULL UNIQUE CHECK (octet_length(idempotency_digest)=32),
    occurred_at TIMESTAMPTZ NOT NULL
);

CREATE FUNCTION coupon_claim_audit_events_reject_mutation() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'coupon_claim_audit_events is append-only'; END; $$;
CREATE TRIGGER coupon_claim_audit_events_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON coupon_claim_audit_events FOR EACH STATEMENT EXECUTE FUNCTION coupon_claim_audit_events_reject_mutation();
