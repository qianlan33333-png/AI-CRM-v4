-- Owner: groupops. Extend its existing directory instead of creating a second
-- directory. A provider group need not have a local login account as owner.
ALTER TABLE group_ops_directory_groups ALTER COLUMN owner_staff_id DROP NOT NULL;
ALTER TABLE group_ops_directory_groups
 ADD COLUMN owner_user_id TEXT NOT NULL DEFAULT '',
 ADD COLUMN sync_state TEXT NOT NULL DEFAULT 'ready' CHECK(sync_state IN ('ready','pending','failed')),
 ADD COLUMN observed_at TIMESTAMPTZ,
 ADD COLUMN checked_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp();
UPDATE group_ops_directory_groups SET observed_at=refreshed_at,checked_at=refreshed_at;

CREATE TABLE group_ops_catalog_runs (
 id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 state TEXT NOT NULL CHECK(state IN ('queued','running','completed','partial','failed')),
 cursor TEXT NOT NULL DEFAULT '',
 started_at TIMESTAMPTZ NOT NULL,
 finished_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX group_ops_catalog_one_active ON group_ops_catalog_runs ((true)) WHERE state IN ('queued','running');
CREATE TABLE group_ops_catalog_control (
 singleton BOOLEAN PRIMARY KEY DEFAULT true CHECK(singleton),
 next_auto_at TIMESTAMPTZ NOT NULL,
 rerun_requested BOOLEAN NOT NULL DEFAULT false
);
-- Per-run observations are business progress, not a worker/retry queue.
CREATE TABLE group_ops_catalog_observations (
 run_id BIGINT NOT NULL REFERENCES group_ops_catalog_runs(id),
 chat_id TEXT NOT NULL,
 outcome TEXT NOT NULL CHECK(outcome IN ('added','updated','failed')),
 PRIMARY KEY(run_id,chat_id)
);
