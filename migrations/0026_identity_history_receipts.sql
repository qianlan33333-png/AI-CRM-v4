-- Owner: internal/identity. One receipt per authoritative source person or
-- safely quarantined identity row makes the one-off production import fully
-- accountable without persisting raw quarantined identifiers.
CREATE FUNCTION identity_history_receipts_reject_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'identity history import receipts are append-only';
END;
$$;

CREATE TABLE identity_history_import_receipts (
  id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  run_key TEXT NOT NULL CHECK (length(run_key) BETWEEN 1 AND 200 AND btrim(run_key)=run_key),
  source_key TEXT NOT NULL CHECK (length(source_key) BETWEEN 1 AND 200 AND btrim(source_key)=source_key),
  source_digest BYTEA NOT NULL CHECK (octet_length(source_digest)=32),
  outcome TEXT NOT NULL CHECK (outcome IN ('canonical','quarantined')),
  customer_id BIGINT,
  identity_count INTEGER NOT NULL DEFAULT 0 CHECK (identity_count>=0),
  reason_code TEXT,
  safe_evidence JSONB,
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  UNIQUE(run_key,source_key),
  CHECK (
    (outcome='canonical' AND customer_id>0 AND identity_count>0 AND reason_code IS NULL AND safe_evidence IS NULL)
    OR
    (outcome='quarantined' AND customer_id IS NULL AND identity_count=0 AND reason_code IS NOT NULL AND jsonb_typeof(safe_evidence)='object')
  )
);

CREATE TRIGGER identity_history_import_receipts_append_only
  BEFORE UPDATE OR DELETE OR TRUNCATE ON identity_history_import_receipts
  FOR EACH STATEMENT EXECUTE FUNCTION identity_history_receipts_reject_mutation();
