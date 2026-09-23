-- Owner: Automation. Digest-only evidence for fenced manual reconciliation.
-- No Provider response, external identity, message body or token is stored.
CREATE TABLE automation_run_reconciliations (
  id BIGSERIAL PRIMARY KEY,
  run_id BIGINT NOT NULL REFERENCES automation_runs(id) ON DELETE RESTRICT,
  recipient_id BIGINT NOT NULL REFERENCES automation_run_recipients(id) ON DELETE RESTRICT,
  effect_id TEXT NOT NULL CHECK(length(effect_id) BETWEEN 5 AND 200),
  generation BIGINT NOT NULL CHECK(generation>0),
  fence BIGINT NOT NULL CHECK(fence>0),
  lease_expires_at TIMESTAMPTZ NOT NULL,
  evidence_digest BYTEA NOT NULL CHECK(octet_length(evidence_digest)=32),
  resolution TEXT NOT NULL CHECK(resolution IN ('provider_accepted','delivery_proven','final_failed')),
  actor_id BIGINT NOT NULL CHECK(actor_id>0),
  receipt_key_digest BYTEA NOT NULL CHECK(octet_length(receipt_key_digest)=32),
  created_at TIMESTAMPTZ NOT NULL,
  UNIQUE(effect_id,generation,fence),
  UNIQUE(receipt_key_digest)
);
CREATE INDEX automation_run_reconciliations_run_idx ON automation_run_reconciliations(run_id,id);
CREATE TRIGGER automation_run_reconciliations_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON automation_run_reconciliations FOR EACH STATEMENT EXECUTE FUNCTION automation_append_only();
