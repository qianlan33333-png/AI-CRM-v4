-- Owners: Segment (current imported audience) and Automation (history/import ledger).
-- Historical runs are sealed facts only. No row in this migration references
-- River or external_effects, so applying a v2 snapshot can never execute it.
CREATE TABLE automation_operations_migration_batches (
  id BIGSERIAL PRIMARY KEY,
  batch_key TEXT NOT NULL UNIQUE,
  source_system TEXT NOT NULL,
  donor_commit TEXT NOT NULL CHECK(donor_commit ~ '^[0-9a-f]{40}$'),
  snapshot_at TIMESTAMPTZ NOT NULL,
  source_watermark_digest BYTEA NOT NULL CHECK(octet_length(source_watermark_digest)=32),
  manifest JSONB NOT NULL CHECK(jsonb_typeof(manifest)='object'),
  manifest_digest BYTEA NOT NULL CHECK(octet_length(manifest_digest)=32),
  provider_effect_count_before BIGINT NOT NULL CHECK(provider_effect_count_before>=0),
  provider_effect_count_after BIGINT CHECK(provider_effect_count_after IS NULL OR provider_effect_count_after>=0),
  river_job_count_before BIGINT NOT NULL CHECK(river_job_count_before>=0),
  river_job_count_after BIGINT CHECK(river_job_count_after IS NULL OR river_job_count_after>=0),
  status TEXT NOT NULL CHECK(status IN ('importing','imported','reconciled','rolled_back','failed')),
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE automation_operations_migration_source_map (
  id BIGSERIAL PRIMARY KEY,
  batch_id BIGINT NOT NULL REFERENCES automation_operations_migration_batches(id) ON DELETE RESTRICT,
  source_system TEXT NOT NULL,
  source_table TEXT NOT NULL,
  source_pk TEXT NOT NULL,
  target_table TEXT NOT NULL,
  target_pk BIGINT,
  record_digest BYTEA NOT NULL CHECK(octet_length(record_digest)=32),
  disposition TEXT NOT NULL CHECK(disposition IN ('imported','duplicate','mapped','unresolved','conflict','invalid','quarantine')),
  created_at TIMESTAMPTZ NOT NULL,
  UNIQUE(source_system,source_table,source_pk)
);
CREATE INDEX automation_operations_migration_source_map_batch_idx ON automation_operations_migration_source_map(batch_id,source_table,id);

CREATE TABLE automation_operations_migration_quarantine (
  id BIGSERIAL PRIMARY KEY,
  batch_id BIGINT NOT NULL REFERENCES automation_operations_migration_batches(id) ON DELETE RESTRICT,
  source_system TEXT NOT NULL,
  source_table TEXT NOT NULL,
  source_pk TEXT NOT NULL,
  reason_code TEXT NOT NULL,
  safe_summary JSONB NOT NULL DEFAULT '{}'::jsonb CHECK(jsonb_typeof(safe_summary)='object'),
  record_digest BYTEA NOT NULL CHECK(octet_length(record_digest)=32),
  created_at TIMESTAMPTZ NOT NULL,
  UNIQUE(source_system,source_table,source_pk,reason_code)
);

CREATE TABLE automation_operations_legacy_history (
  id BIGSERIAL PRIMARY KEY,
  batch_id BIGINT NOT NULL REFERENCES automation_operations_migration_batches(id) ON DELETE RESTRICT,
  source_system TEXT NOT NULL,
  source_table TEXT NOT NULL,
  source_pk TEXT NOT NULL,
  source_state TEXT NOT NULL,
  source_effect_digest BYTEA CHECK(source_effect_digest IS NULL OR octet_length(source_effect_digest)=32),
  record_digest BYTEA NOT NULL CHECK(octet_length(record_digest)=32),
  safe_summary JSONB NOT NULL DEFAULT '{}'::jsonb CHECK(jsonb_typeof(safe_summary)='object'),
  occurred_at TIMESTAMPTZ NOT NULL,
  imported_at TIMESTAMPTZ NOT NULL,
  read_only BOOLEAN NOT NULL DEFAULT TRUE CHECK(read_only),
  replayable BOOLEAN NOT NULL DEFAULT FALSE CHECK(NOT replayable),
  UNIQUE(source_system,source_table,source_pk)
);
CREATE INDEX automation_operations_legacy_history_state_idx ON automation_operations_legacy_history(source_table,source_state,occurred_at DESC,id DESC);

CREATE TRIGGER automation_operations_migration_source_map_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON automation_operations_migration_source_map FOR EACH STATEMENT EXECUTE FUNCTION automation_append_only();
CREATE TRIGGER automation_operations_migration_quarantine_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON automation_operations_migration_quarantine FOR EACH STATEMENT EXECUTE FUNCTION automation_append_only();
CREATE TRIGGER automation_operations_legacy_history_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON automation_operations_legacy_history FOR EACH STATEMENT EXECUTE FUNCTION automation_append_only();
