-- Owner: Segment/Audience. Durable cursor for cron-triggered refreshes.
-- River periodically scans this state; the cursor and refresh acceptance are
-- committed in one UoW so process restarts cannot lose or duplicate a due run.
CREATE TABLE segment_audience_schedule_states (
  configuration_version_id BIGINT PRIMARY KEY,
  package_id BIGINT NOT NULL,
  next_due_at TIMESTAMPTZ NOT NULL,
  last_dispatched_at TIMESTAMPTZ,
  version BIGINT NOT NULL DEFAULT 1 CHECK(version>0),
  updated_at TIMESTAMPTZ NOT NULL,
  FOREIGN KEY(configuration_version_id,package_id)
    REFERENCES segment_audience_configuration_versions(id,package_id) ON DELETE RESTRICT
);
CREATE INDEX segment_audience_schedule_states_due_idx ON segment_audience_schedule_states(next_due_at,package_id);

ALTER TABLE segment_audience_audit_events DROP CONSTRAINT segment_audience_audit_events_resource_kind_check;
ALTER TABLE segment_audience_audit_events ADD CONSTRAINT segment_audience_audit_events_resource_kind_check
  CHECK(resource_kind IN ('group','package','configuration','refresh_run','snapshot','webhook_receipt','binding','sender_set','schedule'));
ALTER TABLE segment_audience_outbox DROP CONSTRAINT segment_audience_outbox_aggregate_kind_check;
ALTER TABLE segment_audience_outbox ADD CONSTRAINT segment_audience_outbox_aggregate_kind_check
  CHECK(aggregate_kind IN ('group','package','configuration','refresh_run','snapshot','webhook_receipt','binding','sender_set','schedule'));
