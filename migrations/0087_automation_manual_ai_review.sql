-- Owner: Automation runtime. AI plan IDs are opaque stable-port references;
-- this migration deliberately creates no cross-domain foreign key.
ALTER TABLE automation_runs
  ADD COLUMN ai_plan_id BIGINT CHECK(ai_plan_id IS NULL OR ai_plan_id>0);

CREATE UNIQUE INDEX automation_runs_ai_plan_id_unique
  ON automation_runs(ai_plan_id) WHERE ai_plan_id IS NOT NULL;

ALTER TABLE automation_runs
  DROP CONSTRAINT automation_runs_state_check;
ALTER TABLE automation_runs
  ADD CONSTRAINT automation_runs_state_check
  CHECK(state IN ('preparing','ready','pending_review','executing','completed','partial_failed','outcome_unknown','cancelled'));
