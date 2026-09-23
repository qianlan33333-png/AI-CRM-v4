-- Owner: internal/order. Canonical customer ownership is resolved before an
-- entitlement reaches this table; no UnionID, phone or external_userid is
-- persisted here.
CREATE TABLE order_service_entitlements (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    source_system TEXT NOT NULL CHECK (char_length(source_system) BETWEEN 1 AND 80),
    source_key TEXT NOT NULL CHECK (char_length(source_key) BETWEEN 1 AND 200),
    customer_id BIGINT NOT NULL REFERENCES customers(id) ON DELETE RESTRICT,
    service_product_id BIGINT NOT NULL CHECK (service_product_id > 0),
    product_name TEXT NOT NULL CHECK (char_length(product_name) BETWEEN 1 AND 500),
    last_order_id BIGINT REFERENCES orders(id) ON DELETE SET NULL,
    status TEXT NOT NULL CHECK (status IN ('active','expired','refunded')),
    start_at TIMESTAMPTZ NOT NULL,
    end_at TIMESTAMPTZ NOT NULL,
    remark TEXT NOT NULL DEFAULT '' CHECK (char_length(remark) <= 500),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    source_digest BYTEA NOT NULL CHECK (octet_length(source_digest)=32),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    UNIQUE(source_system, source_key),
    CHECK (end_at >= start_at)
);
CREATE INDEX order_service_entitlements_customer_idx
    ON order_service_entitlements(customer_id, end_at DESC, id DESC);

CREATE TABLE order_entitlement_operation_receipts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    operation TEXT NOT NULL CHECK (operation IN ('remark')),
    key_digest BYTEA NOT NULL UNIQUE CHECK (octet_length(key_digest)=32),
    payload_digest BYTEA NOT NULL CHECK (octet_length(payload_digest)=32),
    entitlement_id BIGINT NOT NULL REFERENCES order_service_entitlements(id) ON DELETE RESTRICT,
    outcome TEXT NOT NULL CHECK (outcome IN ('updated','version_conflict')),
    result_snapshot JSONB NOT NULL CHECK (jsonb_typeof(result_snapshot)='object'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);

CREATE TABLE order_entitlement_audit_events (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entitlement_id BIGINT NOT NULL REFERENCES order_service_entitlements(id) ON DELETE RESTRICT,
    operation TEXT NOT NULL CHECK (operation='remark'),
    actor_digest BYTEA NOT NULL CHECK (octet_length(actor_digest)=32),
    payload_digest BYTEA NOT NULL CHECK (octet_length(payload_digest)=32),
    occurred_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE order_entitlement_outbox (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    event_type TEXT NOT NULL CHECK (event_type='order.entitlement.remark_updated.v1'),
    entitlement_id BIGINT NOT NULL REFERENCES order_service_entitlements(id) ON DELETE RESTRICT,
    payload JSONB NOT NULL CHECK (jsonb_typeof(payload)='object'),
    idempotency_digest BYTEA NOT NULL UNIQUE CHECK (octet_length(idempotency_digest)=32),
    occurred_at TIMESTAMPTZ NOT NULL
);
