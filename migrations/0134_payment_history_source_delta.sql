-- Owner: internal/payment. Historical evidence only; no provider effects.
CREATE TABLE payment_history_source_deltas (
 id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 result_kind TEXT NOT NULL CHECK(result_kind IN ('payment','refund')),
 result_id BIGINT NOT NULL CHECK(result_id>0),
 run_key TEXT NOT NULL,
 before_digest BYTEA NOT NULL CHECK(octet_length(before_digest)=32),
 after_digest BYTEA NOT NULL CHECK(octet_length(after_digest)=32),
 before_version BIGINT NOT NULL CHECK(before_version>0),
 after_version BIGINT NOT NULL CHECK(after_version=before_version+1),
 before_status TEXT NOT NULL,after_status TEXT NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(result_kind,result_id,run_key)
);
CREATE FUNCTION payment_history_delta_immutable() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'payment history evidence is append only'; END $$;
CREATE TRIGGER payment_history_delta_immutable BEFORE UPDATE OR DELETE OR TRUNCATE ON payment_history_source_deltas FOR EACH STATEMENT EXECUTE FUNCTION payment_history_delta_immutable();
