CREATE TABLE order_history_source_deltas (
 id bigserial PRIMARY KEY,
 order_id bigint NOT NULL REFERENCES orders(id),
 run_id bigint NOT NULL REFERENCES order_import_runs(id),
 before_source_digest bytea NOT NULL CHECK(octet_length(before_source_digest)=32),
 after_source_digest bytea NOT NULL CHECK(octet_length(after_source_digest)=32),
 before_version bigint NOT NULL CHECK(before_version>0),
 after_version bigint NOT NULL CHECK(after_version=before_version+1),
 before_status text NOT NULL,
 after_status text NOT NULL,
 before_refunded_minor bigint NOT NULL CHECK(before_refunded_minor>=0),
 after_refunded_minor bigint NOT NULL CHECK(after_refunded_minor>=before_refunded_minor),
 occurred_at timestamptz NOT NULL,
 UNIQUE(order_id,after_version),
 UNIQUE(run_id,order_id)
);
CREATE TRIGGER order_history_source_deltas_append_only
 BEFORE UPDATE OR DELETE OR TRUNCATE ON order_history_source_deltas
 FOR EACH STATEMENT EXECUTE FUNCTION order_immutable_facts_reject_mutation();
