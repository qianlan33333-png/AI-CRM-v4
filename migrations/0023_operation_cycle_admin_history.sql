-- Owner: internal/operationcycle.
-- Additive immutable strategy/run history plus actor-scoped admin mutation
-- receipts. These tables contain local operation definitions only.

CREATE TABLE operation_cycle_strategy_versions (
  strategy_key TEXT NOT NULL REFERENCES operation_cycle_strategies(strategy_key) ON DELETE RESTRICT,
  version INTEGER NOT NULL,
  title TEXT NOT NULL,
  status TEXT NOT NULL,
  definition JSONB NOT NULL,
  snapshot JSONB NOT NULL,
  created_by TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (strategy_key, version),
  CONSTRAINT operation_cycle_strategy_versions_version CHECK (version > 0),
  CONSTRAINT operation_cycle_strategy_versions_status CHECK (status IN ('draft', 'active', 'paused', 'archived'))
);

INSERT INTO operation_cycle_strategy_versions
  (strategy_key, version, title, status, definition, snapshot, created_by, created_at)
SELECT strategy_key, version, title, status, definition, snapshot, 'migration:0023', updated_at
FROM operation_cycle_strategies
ON CONFLICT DO NOTHING;

CREATE TABLE operation_cycle_run_versions (
  run_key TEXT NOT NULL REFERENCES operation_cycle_runs(run_key) ON DELETE RESTRICT,
  snapshot_revision INTEGER NOT NULL,
  strategy_key TEXT NOT NULL REFERENCES operation_cycle_strategies(strategy_key) ON DELETE RESTRICT,
  snapshot JSONB NOT NULL,
  received_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (run_key, snapshot_revision),
  CONSTRAINT operation_cycle_run_versions_revision CHECK (snapshot_revision > 0)
);
CREATE INDEX operation_cycle_run_versions_strategy_received
  ON operation_cycle_run_versions (strategy_key, received_at DESC, run_key DESC, snapshot_revision DESC);

INSERT INTO operation_cycle_run_versions
  (run_key, snapshot_revision, strategy_key, snapshot, received_at)
SELECT run_key, snapshot_revision, strategy_key, snapshot, received_at
FROM operation_cycle_runs
ON CONFLICT DO NOTHING;

-- The frozen donor accepts a numeric run id in its detail URL. This mapping
-- is assigned once per immutable run key, so inserting or re-sorting list
-- rows can never make an existing URL resolve to another dossier.
CREATE TABLE operation_cycle_run_ordinals (
  ordinal INTEGER GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  run_key TEXT NOT NULL UNIQUE REFERENCES operation_cycle_runs(run_key) ON DELETE RESTRICT,
  CONSTRAINT operation_cycle_run_ordinals_donor_range CHECK (ordinal BETWEEN 1 AND 999999999)
);

INSERT INTO operation_cycle_run_ordinals(run_key)
SELECT run_key FROM operation_cycle_runs ORDER BY run_key
ON CONFLICT DO NOTHING;

CREATE FUNCTION operation_cycle_assign_run_ordinal() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  INSERT INTO operation_cycle_run_ordinals(run_key) VALUES (NEW.run_key)
  ON CONFLICT DO NOTHING;
  RETURN NEW;
END; $$;
CREATE TRIGGER operation_cycle_runs_assign_ordinal
  AFTER INSERT ON operation_cycle_runs
  FOR EACH ROW EXECUTE FUNCTION operation_cycle_assign_run_ordinal();

CREATE TABLE operation_cycle_admin_receipts (
  actor_id TEXT NOT NULL,
  key_digest BYTEA NOT NULL,
  payload_digest BYTEA NOT NULL,
  response JSONB NOT NULL,
  created_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (actor_id, key_digest),
  CONSTRAINT operation_cycle_admin_receipts_key_digest CHECK (octet_length(key_digest) = 32),
  CONSTRAINT operation_cycle_admin_receipts_payload_digest CHECK (octet_length(payload_digest) = 32)
);

CREATE FUNCTION operation_cycle_history_reject_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'operation-cycle history and receipts are append-only' USING ERRCODE = '55000';
END; $$;
CREATE TRIGGER operation_cycle_strategy_versions_append_only
  BEFORE UPDATE OR DELETE OR TRUNCATE ON operation_cycle_strategy_versions
  FOR EACH STATEMENT EXECUTE FUNCTION operation_cycle_history_reject_mutation();
CREATE TRIGGER operation_cycle_run_versions_append_only
  BEFORE UPDATE OR DELETE OR TRUNCATE ON operation_cycle_run_versions
  FOR EACH STATEMENT EXECUTE FUNCTION operation_cycle_history_reject_mutation();
CREATE TRIGGER operation_cycle_run_ordinals_append_only
  BEFORE UPDATE OR DELETE OR TRUNCATE ON operation_cycle_run_ordinals
  FOR EACH STATEMENT EXECUTE FUNCTION operation_cycle_history_reject_mutation();
CREATE TRIGGER operation_cycle_admin_receipts_append_only
  BEFORE UPDATE OR DELETE OR TRUNCATE ON operation_cycle_admin_receipts
  FOR EACH STATEMENT EXECUTE FUNCTION operation_cycle_history_reject_mutation();
