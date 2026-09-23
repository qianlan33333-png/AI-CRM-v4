-- Owners: internal/product (service-period term) and internal/order
-- (entitlement fulfillment). The existing Product-owned term table keeps its
-- name so the already-shipped definition importer remains compatible; it is
-- now also read by the Product checkout projection. No second Product or
-- entitlement aggregate is created.

CREATE TABLE order_entitlement_fulfillment_receipts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    operation TEXT NOT NULL CHECK (operation IN ('grant','refund')),
    source_order_id BIGINT NOT NULL REFERENCES orders(id) ON DELETE RESTRICT,
    payload_digest BYTEA NOT NULL CHECK (octet_length(payload_digest)=32),
    entitlement_id BIGINT NOT NULL REFERENCES order_service_entitlements(id) ON DELETE RESTRICT,
    result_snapshot JSONB NOT NULL CHECK (jsonb_typeof(result_snapshot)='object'),
    duration_days INTEGER NOT NULL CHECK (duration_days>0),
    -- Frozen only on grant. It preserves active imported/legacy coverage that
    -- predates native per-order receipts, so a later refund can remove this
    -- order's days without erasing that prior unrefunded period. This is an
    -- aggregate edge and is not itself proof of independent history.
    prior_active_end_at TIMESTAMPTZ,
    -- Only independent, non-native legacy coverage is recorded here. A prior
    -- end made solely by another native grant must never be retained after
    -- that grant has also been refunded.
    prior_historical_end_at TIMESTAMPTZ,
    -- The first positive refund is the immutable revocation fact. Further
    -- refunds of this same order are deliberate successful no-ops.
    refund_amount_minor BIGINT NOT NULL DEFAULT 0 CHECK (refund_amount_minor>=0),
    created_at TIMESTAMPTZ NOT NULL,
    UNIQUE(operation, source_order_id)
);
CREATE INDEX order_entitlement_fulfillment_entitlement_idx
    ON order_entitlement_fulfillment_receipts(entitlement_id, operation, source_order_id);

-- A history import does not itself grant or renew an entitlement. When an
-- imported source order has been reconciled, this owner-local mapping retains
-- the source period needed to process a later authoritative refund without
-- manufacturing a native payment receipt.
CREATE TABLE order_entitlement_historical_sources (
    source_order_id BIGINT PRIMARY KEY REFERENCES orders(id) ON DELETE RESTRICT,
    entitlement_id BIGINT NOT NULL REFERENCES order_service_entitlements(id) ON DELETE RESTRICT,
    source_line_no INTEGER NOT NULL CHECK (source_line_no>0),
    product_id BIGINT NOT NULL CHECK (product_id>0),
    product_code TEXT NOT NULL CHECK (char_length(product_code) BETWEEN 1 AND 200),
    issued_duration_days INTEGER NOT NULL CHECK (issued_duration_days>0),
    source_start_at TIMESTAMPTZ NOT NULL,
    source_end_at TIMESTAMPTZ NOT NULL,
    imported_at TIMESTAMPTZ NOT NULL,
    CHECK (source_end_at>source_start_at)
);
CREATE INDEX order_entitlement_historical_sources_entitlement_idx
    ON order_entitlement_historical_sources(entitlement_id, source_order_id);

ALTER TABLE order_entitlement_audit_events
    DROP CONSTRAINT order_entitlement_audit_events_operation_check;
ALTER TABLE order_entitlement_audit_events
    ADD CONSTRAINT order_entitlement_audit_events_operation_check
    CHECK (operation IN ('remark','grant','renew','refund'));

ALTER TABLE order_entitlement_outbox
    DROP CONSTRAINT order_entitlement_outbox_event_type_check;
ALTER TABLE order_entitlement_outbox
    ADD CONSTRAINT order_entitlement_outbox_event_type_check
    CHECK (event_type IN ('order.entitlement.remark_updated.v1','order.entitlement.granted.v1','order.entitlement.renewed.v1','order.entitlement.refunded.v1'));
