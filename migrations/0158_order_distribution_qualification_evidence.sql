-- Owner: internal/order. Historical qualification needs explicit verified
-- evidence because historical Order rows often lack a native checkout snapshot
-- or first-paid event. Only the import verifier writes this append-only map;
-- Distribution reads it through Order's dedicated Port.
ALTER TABLE order_checkout_snapshots ADD COLUMN profit_sharing_required BOOLEAN NOT NULL DEFAULT FALSE;

CREATE TABLE order_distribution_qualification_evidence (
    order_id BIGINT NOT NULL REFERENCES orders(id) ON DELETE RESTRICT,
    order_item_line INTEGER NOT NULL CHECK (order_item_line > 0),
    product_id BIGINT NOT NULL CHECK (product_id > 0),
    product_type TEXT NOT NULL CHECK (product_type IN ('standard_product','service_period')),
    source_product_code TEXT NOT NULL CHECK (length(source_product_code) BETWEEN 1 AND 200 AND btrim(source_product_code)=source_product_code),
    payer_customer_id BIGINT NOT NULL CHECK (payer_customer_id > 0),
    beneficiary_customer_id BIGINT NOT NULL CHECK (beneficiary_customer_id > 0),
    item_paid_minor BIGINT NOT NULL CHECK (item_paid_minor > 0),
    payment_confirmed_at TIMESTAMPTZ NOT NULL,
    source_order_digest BYTEA NOT NULL CHECK (octet_length(source_order_digest)=32),
    source_digest BYTEA NOT NULL CHECK (octet_length(source_digest)=32),
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY(order_id, order_item_line)
);
CREATE INDEX order_distribution_qualification_evidence_lookup_idx ON order_distribution_qualification_evidence(product_type, product_id, payer_customer_id, beneficiary_customer_id);
CREATE TRIGGER order_distribution_qualification_evidence_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON order_distribution_qualification_evidence FOR EACH STATEMENT EXECUTE FUNCTION order_immutable_facts_reject_mutation();
