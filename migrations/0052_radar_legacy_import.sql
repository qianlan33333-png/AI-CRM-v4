-- Owner: Radar. One-time source mapping and read-only historical facts.
-- Historical rows cannot be treated as verified identity evidence.

CREATE TABLE radar_migration_batches (
  id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  batch_key TEXT NOT NULL UNIQUE CHECK(batch_key <> '' AND length(batch_key) <= 128),
  source_system TEXT NOT NULL CHECK(source_system <> '' AND length(source_system) <= 128),
  donor_commit TEXT NOT NULL CHECK(donor_commit ~ '^[0-9a-f]{40}$'),
  snapshot_at TIMESTAMPTZ NOT NULL,
  snapshot_digest BYTEA NOT NULL UNIQUE CHECK(octet_length(snapshot_digest) = 32),
  source_count BIGINT NOT NULL CHECK(source_count >= 0),
  imported_count BIGINT NOT NULL DEFAULT 0 CHECK(imported_count >= 0),
  quarantined_count BIGINT NOT NULL DEFAULT 0 CHECK(quarantined_count >= 0),
  status TEXT NOT NULL CHECK(status IN ('dry_run','importing','imported','reconciled','failed')),
  created_at TIMESTAMPTZ NOT NULL,
  completed_at TIMESTAMPTZ
);

CREATE TABLE radar_migration_source_map (
  id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  batch_id BIGINT NOT NULL REFERENCES radar_migration_batches(id) ON DELETE RESTRICT,
  source_table TEXT NOT NULL CHECK(source_table <> '' AND length(source_table) <= 128),
  source_pk TEXT NOT NULL CHECK(source_pk <> '' AND length(source_pk) <= 200),
  target_table TEXT NOT NULL CHECK(target_table IN ('radar_links','radar_legacy_events')),
  target_pk BIGINT,
  record_digest BYTEA NOT NULL CHECK(octet_length(record_digest) = 32),
  disposition TEXT NOT NULL CHECK(disposition IN ('imported','duplicate','unattributed','invalid','quarantine')),
  created_at TIMESTAMPTZ NOT NULL,
  UNIQUE(batch_id, source_table, source_pk)
);
CREATE INDEX radar_migration_source_map_batch_idx ON radar_migration_source_map(batch_id, id);

CREATE TABLE radar_migration_quarantine (
  id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  batch_id BIGINT NOT NULL REFERENCES radar_migration_batches(id) ON DELETE RESTRICT,
  source_table TEXT NOT NULL CHECK(source_table <> '' AND length(source_table) <= 128),
  source_pk TEXT NOT NULL CHECK(source_pk <> '' AND length(source_pk) <= 200),
  reason_code TEXT NOT NULL CHECK(reason_code <> '' AND length(reason_code) <= 80),
  safe_summary JSONB NOT NULL DEFAULT '{}'::jsonb CHECK(jsonb_typeof(safe_summary) = 'object'),
  record_digest BYTEA NOT NULL CHECK(octet_length(record_digest) = 32),
  created_at TIMESTAMPTZ NOT NULL,
  UNIQUE(batch_id, source_table, source_pk, reason_code)
);

CREATE TABLE radar_legacy_events (
  id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  batch_id BIGINT NOT NULL REFERENCES radar_migration_batches(id) ON DELETE RESTRICT,
  source_table TEXT NOT NULL CHECK(source_table <> '' AND length(source_table) <= 128),
  source_pk TEXT NOT NULL CHECK(source_pk <> '' AND length(source_pk) <= 200),
  radar_id BIGINT REFERENCES radar_links(id) ON DELETE RESTRICT,
  source_stage TEXT NOT NULL CHECK(source_stage <> '' AND length(source_stage) <= 80),
  record_digest BYTEA NOT NULL CHECK(octet_length(record_digest) = 32),
  safe_summary JSONB NOT NULL DEFAULT '{}'::jsonb CHECK(jsonb_typeof(safe_summary) = 'object'),
  occurred_at TIMESTAMPTZ NOT NULL,
  imported_at TIMESTAMPTZ NOT NULL,
  read_only BOOLEAN NOT NULL DEFAULT TRUE CHECK(read_only),
  identity_attributed BOOLEAN NOT NULL DEFAULT FALSE CHECK(NOT identity_attributed),
  replayable BOOLEAN NOT NULL DEFAULT FALSE CHECK(NOT replayable),
  UNIQUE(batch_id, source_table, source_pk)
);
CREATE INDEX radar_legacy_events_radar_idx ON radar_legacy_events(radar_id, occurred_at DESC, id DESC);

CREATE TRIGGER radar_migration_source_map_guard BEFORE UPDATE OR DELETE OR TRUNCATE ON radar_migration_source_map FOR EACH STATEMENT EXECUTE FUNCTION radar_reject_immutable_mutation();
CREATE TRIGGER radar_migration_quarantine_guard BEFORE UPDATE OR DELETE OR TRUNCATE ON radar_migration_quarantine FOR EACH STATEMENT EXECUTE FUNCTION radar_reject_immutable_mutation();
CREATE TRIGGER radar_legacy_events_guard BEFORE UPDATE OR DELETE OR TRUNCATE ON radar_legacy_events FOR EACH STATEMENT EXECUTE FUNCTION radar_reject_immutable_mutation();
