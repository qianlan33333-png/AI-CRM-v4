-- Owner: internal/coupon. Rule administration only: no customer, claim,
-- redemption, order, payment, entitlement, public-link, or identity table.
CREATE TABLE coupon_rules (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 45),
    discount_amount_total BIGINT NOT NULL CHECK (discount_amount_total > 0),
    currency TEXT NOT NULL CHECK (currency = 'CNY'),
    status TEXT NOT NULL CHECK (status IN ('draft','published','stopped','archived')),
    total_issue_limit BIGINT NOT NULL CHECK (total_issue_limit > 0),
    per_user_issue_limit BIGINT NOT NULL CHECK (per_user_issue_limit > 0 AND per_user_issue_limit <= total_issue_limit),
    issued_count BIGINT NOT NULL DEFAULT 0 CHECK (issued_count >= 0 AND issued_count <= total_issue_limit),
    claim_starts_at TIMESTAMPTZ NOT NULL,
    claim_ends_at TIMESTAMPTZ NOT NULL CHECK (claim_ends_at > claim_starts_at),
    validity_mode TEXT NOT NULL CHECK (validity_mode IN ('fixed_range','relative_days')),
    use_starts_at TIMESTAMPTZ,
    use_ends_at TIMESTAMPTZ,
    relative_validity_days INTEGER,
    instructions TEXT NOT NULL DEFAULT '' CHECK (length(instructions) <= 200),
    created_by BIGINT NOT NULL CHECK (created_by > 0),
    updated_by BIGINT NOT NULL CHECK (updated_by > 0),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT coupon_rules_validity_shape CHECK (
      (validity_mode = 'fixed_range' AND use_starts_at IS NOT NULL AND use_ends_at IS NOT NULL AND use_ends_at > use_starts_at AND relative_validity_days IS NULL)
      OR (validity_mode = 'relative_days' AND use_starts_at IS NULL AND use_ends_at IS NULL AND relative_validity_days IS NOT NULL AND relative_validity_days > 0)
    )
);
CREATE INDEX coupon_rules_list_idx ON coupon_rules(status, updated_at DESC, id DESC);

CREATE TABLE coupon_rule_targets (
    coupon_id BIGINT NOT NULL REFERENCES coupon_rules(id) ON DELETE CASCADE,
    target_ref TEXT NOT NULL CHECK (target_ref ~ '^(standard_product|service_period):[1-9][0-9]*$'),
    position INTEGER NOT NULL CHECK (position >= 0),
    PRIMARY KEY(coupon_id,target_ref), UNIQUE(coupon_id,position)
);

CREATE TABLE coupon_operation_receipts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    operation TEXT NOT NULL CHECK (operation IN ('create','update','publish','stop','archive','delete','copy')),
    actor_scope TEXT NOT NULL CHECK (actor_scope ~ '^admin:[1-9][0-9]*$'),
    key_digest BYTEA NOT NULL CHECK (octet_length(key_digest) = 32),
    payload_digest BYTEA NOT NULL CHECK (octet_length(payload_digest) = 32),
    state TEXT NOT NULL CHECK (state IN ('in_progress','completed')),
    result_snapshot JSONB,
    created_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    UNIQUE(operation,actor_scope,key_digest),
    CONSTRAINT coupon_receipt_completed CHECK ((state='completed') = (result_snapshot IS NOT NULL AND completed_at IS NOT NULL))
);

CREATE TABLE coupon_audit_events (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    event_type TEXT NOT NULL CHECK (event_type ~ '^coupon[.][a-z_]+$'),
    coupon_id BIGINT NOT NULL CHECK (coupon_id > 0),
    actor_admin_user_id BIGINT NOT NULL CHECK (actor_admin_user_id > 0),
    payload JSONB NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
    occurred_at TIMESTAMPTZ NOT NULL
);
CREATE TABLE coupon_outbox (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    event_type TEXT NOT NULL CHECK (event_type ~ '^coupon[.][a-z_]+$'),
    idempotency_key TEXT NOT NULL UNIQUE CHECK (length(idempotency_key) BETWEEN 1 AND 240),
    aggregate_id BIGINT NOT NULL CHECK (aggregate_id > 0),
    payload JSONB NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
    occurred_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    published_at TIMESTAMPTZ
);
CREATE FUNCTION coupon_audit_events_reject_mutation() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'coupon_audit_events is append-only'; END; $$;
CREATE TRIGGER coupon_audit_events_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON coupon_audit_events FOR EACH STATEMENT EXECUTE FUNCTION coupon_audit_events_reject_mutation();
