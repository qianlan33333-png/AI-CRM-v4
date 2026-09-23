-- Owner: AdminOps. Only bounded process metadata; never copies deleted payload.
CREATE TABLE adminops_retention_runs (
 id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 hour_key TIMESTAMPTZ NOT NULL,
 policy TEXT NOT NULL,
 policy_version TEXT NOT NULL,
 cutoff TIMESTAMPTZ NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('running','completed','failed')),
 deleted_rows BIGINT NOT NULL DEFAULT 0,
 payload_bytes BIGINT NOT NULL DEFAULT 0,
 started_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 completed_at TIMESTAMPTZ,
 failure_code TEXT NOT NULL DEFAULT '',
 UNIQUE(hour_key,policy)
);
CREATE INDEX adminops_retention_runs_recent ON adminops_retention_runs(started_at DESC);
