-- Owner: internal/order.
-- Forward-only financial facts. Order owns canonical orders, immutable item
-- snapshots, status history, local receipts/audit/outbox and historical import
-- ledgers. Payment/refund/provider effects are deliberately absent.

CREATE TABLE orders (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    provider TEXT NOT NULL CHECK (provider IN ('wechat_pay','wechat_shop','alipay')),
    source_system TEXT NOT NULL CHECK (length(source_system) BETWEEN 1 AND 100 AND btrim(source_system)=source_system),
    source_key TEXT NOT NULL CHECK (length(source_key) BETWEEN 1 AND 200 AND btrim(source_key)=source_key),
    merchant_order_no TEXT NOT NULL CHECK (length(merchant_order_no) BETWEEN 1 AND 200 AND btrim(merchant_order_no)=merchant_order_no),
    provider_transaction_no TEXT NOT NULL DEFAULT '' CHECK (length(provider_transaction_no) <= 200 AND btrim(provider_transaction_no)=provider_transaction_no),
    payer_customer_id BIGINT CHECK (payer_customer_id > 0),
    beneficiary_customer_id BIGINT CHECK (beneficiary_customer_id > 0),
    amount_minor BIGINT NOT NULL CHECK (amount_minor > 0),
    refunded_minor BIGINT NOT NULL DEFAULT 0 CHECK (refunded_minor >= 0 AND refunded_minor <= amount_minor),
    currency TEXT NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    status TEXT NOT NULL CHECK (status IN ('pending_payment','paid','partially_refunded','refunded','cancelled','payment_failed','closed')),
    record_origin TEXT NOT NULL CHECK (record_origin IN ('native','history')),
    effect_eligible BOOLEAN NOT NULL,
    source_row_digest BYTEA,
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT orders_source_unique UNIQUE(source_system,source_key),
    CONSTRAINT orders_provider_merchant_unique UNIQUE(provider,merchant_order_no),
    CONSTRAINT orders_origin_effect_shape CHECK (
      (record_origin='native' AND provider <> 'alipay' AND effect_eligible=TRUE AND source_row_digest IS NULL AND payer_customer_id IS NOT NULL AND beneficiary_customer_id IS NOT NULL)
      OR (record_origin='history' AND effect_eligible=FALSE AND octet_length(source_row_digest)=32)
    ),
    CONSTRAINT orders_refund_status_shape CHECK (
      (status='partially_refunded' AND refunded_minor > 0 AND refunded_minor < amount_minor)
      OR (status='refunded' AND refunded_minor = amount_minor)
      OR (status NOT IN ('partially_refunded','refunded') AND refunded_minor = 0)
    ),
    CONSTRAINT orders_time_shape CHECK (updated_at >= created_at)
);
CREATE UNIQUE INDEX orders_provider_transaction_unique ON orders(provider,provider_transaction_no) WHERE provider_transaction_no <> '';
CREATE INDEX orders_list_cursor_idx ON orders(created_at DESC,id DESC);
CREATE INDEX orders_payer_cursor_idx ON orders(payer_customer_id,created_at DESC,id DESC) WHERE payer_customer_id IS NOT NULL;
CREATE INDEX orders_beneficiary_cursor_idx ON orders(beneficiary_customer_id,created_at DESC,id DESC) WHERE beneficiary_customer_id IS NOT NULL;

CREATE TABLE order_items (
    order_id BIGINT NOT NULL REFERENCES orders(id) ON DELETE RESTRICT,
    line_no INTEGER NOT NULL CHECK (line_no > 0),
    product_id BIGINT CHECK (product_id > 0),
    product_code TEXT NOT NULL CHECK (length(product_code) BETWEEN 1 AND 200 AND btrim(product_code)=product_code),
    product_name TEXT NOT NULL CHECK (length(product_name) BETWEEN 1 AND 500 AND btrim(product_name)=product_name),
    unit_amount_minor BIGINT NOT NULL CHECK (unit_amount_minor > 0),
    quantity INTEGER NOT NULL CHECK (quantity > 0),
    line_amount_minor BIGINT NOT NULL CHECK (line_amount_minor > 0),
    PRIMARY KEY(order_id,line_no),
    CONSTRAINT order_items_amount_shape CHECK (line_amount_minor = unit_amount_minor * quantity)
);

CREATE TABLE order_status_history (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    order_id BIGINT NOT NULL REFERENCES orders(id) ON DELETE RESTRICT,
    from_status TEXT,
    to_status TEXT NOT NULL CHECK (to_status IN ('pending_payment','paid','partially_refunded','refunded','cancelled','payment_failed','closed')),
    refunded_minor BIGINT NOT NULL CHECK (refunded_minor >= 0),
    order_version BIGINT NOT NULL CHECK (order_version > 0),
    actor_scope TEXT NOT NULL CHECK (length(actor_scope) BETWEEN 1 AND 200),
    occurred_at TIMESTAMPTZ NOT NULL,
    UNIQUE(order_id,order_version)
);
CREATE INDEX order_status_history_order_idx ON order_status_history(order_id,occurred_at,id);

CREATE TABLE order_operation_receipts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    operation TEXT NOT NULL CHECK (operation IN ('create','settlement')),
    actor_scope TEXT NOT NULL CHECK (length(actor_scope) BETWEEN 1 AND 200),
    key_digest BYTEA NOT NULL CHECK (octet_length(key_digest)=32),
    payload_digest BYTEA NOT NULL CHECK (octet_length(payload_digest)=32),
    state TEXT NOT NULL CHECK (state IN ('in_progress','completed')),
    result_snapshot JSONB,
    created_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    UNIQUE(operation,actor_scope,key_digest),
    CONSTRAINT order_operation_receipt_completion CHECK ((state='completed')=(result_snapshot IS NOT NULL AND completed_at IS NOT NULL))
);

CREATE TABLE order_export_receipts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    actor_admin_user_id BIGINT NOT NULL CHECK (actor_admin_user_id > 0),
    key_digest BYTEA NOT NULL CHECK (octet_length(key_digest)=32),
    filter_digest BYTEA NOT NULL CHECK (octet_length(filter_digest)=32),
    row_count INTEGER NOT NULL CHECK (row_count BETWEEN 0 AND 10000),
    byte_count INTEGER NOT NULL CHECK (byte_count BETWEEN 0 AND 5242880),
    content_digest BYTEA NOT NULL CHECK (octet_length(content_digest)=32),
    created_at TIMESTAMPTZ NOT NULL,
    UNIQUE(actor_admin_user_id,key_digest)
);

CREATE TABLE order_audit_events (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    event_type TEXT NOT NULL CHECK (event_type ~ '^order[.][a-z_]+$'),
    order_id BIGINT NOT NULL CHECK (order_id > 0),
    actor_scope TEXT NOT NULL CHECK (length(actor_scope) BETWEEN 1 AND 200),
    payload JSONB NOT NULL CHECK (jsonb_typeof(payload)='object'),
    occurred_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE order_outbox (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    event_type TEXT NOT NULL CHECK (event_type ~ '^order[.][a-z_]+$'),
    idempotency_key TEXT NOT NULL UNIQUE CHECK (length(idempotency_key) BETWEEN 1 AND 240),
    aggregate_id BIGINT NOT NULL CHECK (aggregate_id > 0),
    payload JSONB NOT NULL CHECK (jsonb_typeof(payload)='object'),
    occurred_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    published_at TIMESTAMPTZ
);

CREATE TABLE order_import_runs (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    run_key TEXT NOT NULL UNIQUE CHECK (length(run_key) BETWEEN 1 AND 200 AND btrim(run_key)=run_key),
    source_manifest_digest BYTEA NOT NULL CHECK (octet_length(source_manifest_digest)=32),
    source_schema_digest BYTEA NOT NULL CHECK (octet_length(source_schema_digest)=32),
    status TEXT NOT NULL CHECK (status IN ('inspected','dry_run','applying','applied','reconciled','rollback_planned','rolled_back','failed')),
    input_count BIGINT NOT NULL DEFAULT 0 CHECK (input_count >= 0),
    imported_count BIGINT NOT NULL DEFAULT 0 CHECK (imported_count >= 0),
    replayed_count BIGINT NOT NULL DEFAULT 0 CHECK (replayed_count >= 0),
    quarantined_count BIGINT NOT NULL DEFAULT 0 CHECK (quarantined_count >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    completed_at TIMESTAMPTZ
);

CREATE TABLE order_import_receipts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    run_id BIGINT NOT NULL REFERENCES order_import_runs(id) ON DELETE RESTRICT,
    source_system TEXT NOT NULL CHECK (length(source_system) BETWEEN 1 AND 100),
    source_key TEXT NOT NULL CHECK (length(source_key) BETWEEN 1 AND 200),
    source_row_digest BYTEA NOT NULL CHECK (octet_length(source_row_digest)=32),
    outcome TEXT NOT NULL CHECK (outcome IN ('imported','replayed','quarantined')),
    order_id BIGINT REFERENCES orders(id) ON DELETE RESTRICT,
    error_code TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE(run_id,source_system,source_key),
    CONSTRAINT order_import_receipt_shape CHECK (
      (outcome IN ('imported','replayed') AND order_id IS NOT NULL AND error_code IS NULL)
      OR (outcome='quarantined' AND order_id IS NULL AND error_code IS NOT NULL)
    )
);
CREATE INDEX order_import_receipts_run_outcome_idx ON order_import_receipts(run_id,outcome);

CREATE TABLE order_import_quarantine (
    receipt_id BIGINT PRIMARY KEY REFERENCES order_import_receipts(id) ON DELETE RESTRICT,
    reason_code TEXT NOT NULL CHECK (length(reason_code) BETWEEN 1 AND 100),
    safe_evidence JSONB NOT NULL CHECK (jsonb_typeof(safe_evidence)='object'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);

CREATE FUNCTION order_immutable_facts_reject_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'order immutable facts are append-only'; END; $$;
CREATE TRIGGER order_items_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON order_items FOR EACH STATEMENT EXECUTE FUNCTION order_immutable_facts_reject_mutation();
CREATE TRIGGER order_status_history_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON order_status_history FOR EACH STATEMENT EXECUTE FUNCTION order_immutable_facts_reject_mutation();
CREATE TRIGGER order_audit_events_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON order_audit_events FOR EACH STATEMENT EXECUTE FUNCTION order_immutable_facts_reject_mutation();
CREATE TRIGGER order_import_receipts_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON order_import_receipts FOR EACH STATEMENT EXECUTE FUNCTION order_immutable_facts_reject_mutation();
CREATE TRIGGER order_import_quarantine_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON order_import_quarantine FOR EACH STATEMENT EXECUTE FUNCTION order_immutable_facts_reject_mutation();
