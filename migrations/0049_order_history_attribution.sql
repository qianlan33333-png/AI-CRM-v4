-- Owner: internal/order.
-- Append-only receipts for one-time, verified-identity attribution of floating
-- historical orders. Raw external identity values are never persisted here.

CREATE TABLE order_history_attribution_runs (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    run_key TEXT NOT NULL UNIQUE CHECK (length(run_key) BETWEEN 1 AND 200 AND btrim(run_key)=run_key),
    source_manifest_digest BYTEA NOT NULL CHECK (octet_length(source_manifest_digest)=32),
    source_schema_digest BYTEA NOT NULL CHECK (octet_length(source_schema_digest)=32),
    identity_scope TEXT NOT NULL CHECK (identity_scope LIKE 'wecom-corp:%' AND length(identity_scope)>11 AND length(identity_scope)<=256),
    snapshot_at TIMESTAMPTZ NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('applying','applied','reconciled','failed')),
    input_count BIGINT NOT NULL CHECK (input_count>=0),
    linked_count BIGINT NOT NULL DEFAULT 0 CHECK (linked_count>=0),
    already_linked_count BIGINT NOT NULL DEFAULT 0 CHECK (already_linked_count>=0),
    quarantined_count BIGINT NOT NULL DEFAULT 0 CHECK (quarantined_count>=0),
    replayed_count BIGINT NOT NULL DEFAULT 0 CHECK (replayed_count>=0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    completed_at TIMESTAMPTZ
);

CREATE TABLE order_history_attribution_receipts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    run_id BIGINT NOT NULL REFERENCES order_history_attribution_runs(id) ON DELETE RESTRICT,
    source_key TEXT NOT NULL CHECK (length(source_key) BETWEEN 1 AND 200 AND btrim(source_key)=source_key),
    order_reference TEXT NOT NULL CHECK (length(order_reference) BETWEEN 1 AND 200 AND btrim(order_reference)=order_reference),
    evidence_digest BYTEA NOT NULL CHECK (octet_length(evidence_digest)=32),
    outcome TEXT NOT NULL CHECK (outcome IN ('linked','already_linked','source_identity_missing','source_identity_not_found','source_external_identity_ambiguous','target_identity_not_found','target_identity_conflict','order_not_found','order_reference_conflict','order_payer_conflict')),
    order_id BIGINT REFERENCES orders(id) ON DELETE RESTRICT,
    payer_customer_id BIGINT CHECK (payer_customer_id>0),
    payer_identity_id BIGINT CHECK (payer_identity_id>0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE(run_id,source_key),
    CONSTRAINT order_history_attribution_receipt_shape CHECK (
      (outcome IN ('linked','already_linked') AND order_id IS NOT NULL AND payer_customer_id IS NOT NULL AND payer_identity_id IS NOT NULL)
      OR (outcome NOT IN ('linked','already_linked') AND payer_customer_id IS NULL AND payer_identity_id IS NULL)
    )
);
CREATE INDEX order_history_attribution_receipts_run_outcome_idx ON order_history_attribution_receipts(run_id,outcome);

CREATE TRIGGER order_history_attribution_receipts_append_only
BEFORE UPDATE OR DELETE OR TRUNCATE ON order_history_attribution_receipts
FOR EACH STATEMENT EXECUTE FUNCTION order_immutable_facts_reject_mutation();
