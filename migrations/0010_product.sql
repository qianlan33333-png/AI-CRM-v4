-- Owner: internal/product.
--
-- Product is the only authoritative row for both ordinary and service-period
-- products.  The service-period view is a typed projection selected by the
-- status in legacy_admin_projection; no second product identity/table is
-- introduced.  Provider credentials, orders, payments, entitlements,
-- members, and customer/OneID facts are deliberately absent.

CREATE TABLE products (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    product_code TEXT NOT NULL,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    price_minor BIGINT NOT NULL CHECK (price_minor >= 0),
    currency TEXT NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    stock_quantity INTEGER NOT NULL CHECK (stock_quantity >= 0),
    images JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_by BIGINT NOT NULL CHECK (created_by > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    legacy_admin_projection JSONB NOT NULL,
    CONSTRAINT products_product_code_nonempty CHECK (length(product_code) BETWEEN 1 AND 200),
    CONSTRAINT products_name_nonempty CHECK (length(name) BETWEEN 1 AND 200),
    CONSTRAINT products_description_length CHECK (length(description) <= 10000),
    CONSTRAINT products_images_shape CHECK (jsonb_typeof(images) = 'array' AND jsonb_array_length(images) <= 20),
    CONSTRAINT products_projection_shape CHECK (jsonb_typeof(legacy_admin_projection) = 'object'),
    CONSTRAINT products_updated_not_before_created CHECK (updated_at >= created_at),
    CONSTRAINT products_product_code_unique UNIQUE (product_code)
);

CREATE INDEX products_status_id_idx
    ON products ((legacy_admin_projection ->> 'status'), id);

-- Product-local operation receipts are separate from the platform's generic
-- receipt table so Product can enforce its own actor/key/payload tuple while
-- still completing the receipt, audit event, and outbox event in one UoW.
CREATE TABLE product_operation_receipts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    operation TEXT NOT NULL CHECK (length(operation) BETWEEN 1 AND 120),
    actor_scope TEXT NOT NULL CHECK (length(actor_scope) BETWEEN 1 AND 200),
    idempotency_key_digest BYTEA NOT NULL CHECK (octet_length(idempotency_key_digest) = 32),
    payload_digest BYTEA NOT NULL CHECK (octet_length(payload_digest) = 32),
    state TEXT NOT NULL DEFAULT 'in_progress' CHECK (state IN ('in_progress', 'completed')),
    result_snapshot JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    completed_at TIMESTAMPTZ,
    CONSTRAINT product_operation_receipts_completion CHECK (
        (state = 'completed' AND result_snapshot IS NOT NULL AND completed_at IS NOT NULL)
        OR (state = 'in_progress' AND result_snapshot IS NULL AND completed_at IS NULL)
    ),
    CONSTRAINT product_operation_receipts_key_unique UNIQUE (operation, actor_scope, idempotency_key_digest)
);

CREATE TABLE product_external_push_configurations (
    product_id BIGINT NOT NULL REFERENCES products(id) ON DELETE RESTRICT,
    product_kind TEXT NOT NULL CHECK (product_kind IN ('wechat_pay', 'service_period')),
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    configuration_reference TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (product_id, product_kind),
    CONSTRAINT product_external_push_reference_shape CHECK (
        (enabled AND length(configuration_reference) BETWEEN 1 AND 128)
        OR (NOT enabled AND configuration_reference = '')
    )
);

-- A test is a local Product fact only.  effect_id is an internal acceptance
-- identifier, not a Provider receipt; all provider/delivery flags remain
-- false by construction.
CREATE TABLE product_external_push_tests (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    product_id BIGINT NOT NULL REFERENCES products(id) ON DELETE RESTRICT,
    product_kind TEXT NOT NULL CHECK (product_kind IN ('wechat_pay', 'service_period')),
    configuration_digest BYTEA NOT NULL CHECK (octet_length(configuration_digest) = 32),
    receipt_id BIGINT NOT NULL REFERENCES product_operation_receipts(id) ON DELETE RESTRICT,
    effect_id TEXT NOT NULL CHECK (effect_id ~ '^eer_[1-9][0-9]*$'),
    state TEXT NOT NULL CHECK (state IN ('accepted', 'queued')),
    provider_accepted BOOLEAN NOT NULL DEFAULT FALSE CHECK (provider_accepted = FALSE),
    delivery_proven BOOLEAN NOT NULL DEFAULT FALSE CHECK (delivery_proven = FALSE),
    real_external_call_executed BOOLEAN NOT NULL DEFAULT FALSE CHECK (real_external_call_executed = FALSE),
    auto_retry_allowed BOOLEAN NOT NULL DEFAULT FALSE CHECK (auto_retry_allowed = FALSE),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT product_external_push_tests_configuration_unique UNIQUE (product_id, product_kind, configuration_digest)
);

CREATE INDEX product_external_push_tests_receipt_idx
    ON product_external_push_tests(receipt_id);
