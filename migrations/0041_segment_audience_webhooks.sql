-- Owner: Segment/Audience. Signed inbound facts retain digests and disposition
-- only; raw external identifiers, signatures and request bodies are forbidden.
CREATE TABLE segment_audience_webhook_receipts (
  id BIGSERIAL PRIMARY KEY,
  package_id BIGINT NOT NULL REFERENCES segment_audience_packages(id) ON DELETE RESTRICT,
  event_id_digest BYTEA NOT NULL CHECK (octet_length(event_id_digest)=32),
  payload_digest BYTEA NOT NULL CHECK (octet_length(payload_digest)=32),
  identity_kind TEXT NOT NULL CHECK (length(identity_kind) BETWEEN 1 AND 64),
  identity_scope_digest BYTEA NOT NULL CHECK (octet_length(identity_scope_digest)=32),
  identity_value_digest BYTEA NOT NULL CHECK (octet_length(identity_value_digest)=32),
  disposition TEXT NOT NULL CHECK (disposition IN ('resolved','unresolved','conflict','invalid')),
  customer_id BIGINT CHECK (customer_id IS NULL OR customer_id>0),
  identity_id BIGINT CHECK (identity_id IS NULL OR identity_id>0),
  occurred_at TIMESTAMPTZ NOT NULL,
  accepted_at TIMESTAMPTZ NOT NULL,
  refresh_run_id BIGINT REFERENCES segment_audience_refresh_runs(id) ON DELETE RESTRICT,
  UNIQUE(package_id,event_id_digest),
  CHECK ((disposition='resolved')=(customer_id IS NOT NULL AND identity_id IS NOT NULL)),
  CHECK ((refresh_run_id IS NOT NULL)=(disposition='resolved'))
);
CREATE INDEX segment_audience_webhook_receipts_package_idx ON segment_audience_webhook_receipts(package_id,accepted_at DESC,id DESC);
CREATE TRIGGER segment_audience_webhook_receipts_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON segment_audience_webhook_receipts FOR EACH STATEMENT EXECUTE FUNCTION segment_audience_append_only();

ALTER TABLE segment_audience_audit_events DROP CONSTRAINT segment_audience_audit_events_resource_kind_check;
ALTER TABLE segment_audience_audit_events ADD CONSTRAINT segment_audience_audit_events_resource_kind_check
  CHECK (resource_kind IN ('group','package','configuration','refresh_run','snapshot','webhook_receipt'));
ALTER TABLE segment_audience_outbox DROP CONSTRAINT segment_audience_outbox_aggregate_kind_check;
ALTER TABLE segment_audience_outbox ADD CONSTRAINT segment_audience_outbox_aggregate_kind_check
  CHECK (aggregate_kind IN ('group','package','configuration','refresh_run','snapshot','webhook_receipt'));
