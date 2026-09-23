-- Owner: Segment/Audience. Forward-only durable member-entered facts.
-- customer_id is the canonical OneID root. No external identity is stored.

CREATE TABLE segment_audience_member_events (
  id BIGSERIAL PRIMARY KEY,
  event_id TEXT NOT NULL UNIQUE CHECK (event_id ~ '^audmem_[1-9][0-9]*_[1-9][0-9]*$'),
  package_id BIGINT NOT NULL REFERENCES segment_audience_packages(id) ON DELETE RESTRICT,
  snapshot_id BIGINT NOT NULL,
  configuration_version_id BIGINT NOT NULL,
  customer_id BIGINT NOT NULL CHECK (customer_id > 0),
  occurred_at TIMESTAMPTZ NOT NULL,
  UNIQUE(snapshot_id,customer_id),
  FOREIGN KEY (snapshot_id,package_id) REFERENCES segment_audience_snapshots(id,package_id) ON DELETE RESTRICT,
  FOREIGN KEY (configuration_version_id,package_id) REFERENCES segment_audience_configuration_versions(id,package_id) ON DELETE RESTRICT
);
CREATE INDEX segment_audience_member_events_dispatch_idx ON segment_audience_member_events(snapshot_id,id);
CREATE TRIGGER segment_audience_member_events_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON segment_audience_member_events FOR EACH STATEMENT EXECUTE FUNCTION segment_audience_append_only();

ALTER TABLE segment_audience_audit_events DROP CONSTRAINT segment_audience_audit_events_resource_kind_check;
ALTER TABLE segment_audience_audit_events ADD CONSTRAINT segment_audience_audit_events_resource_kind_check
  CHECK (resource_kind IN ('group','package','configuration','refresh_run','snapshot','member_event_batch'));
ALTER TABLE segment_audience_outbox DROP CONSTRAINT segment_audience_outbox_aggregate_kind_check;
ALTER TABLE segment_audience_outbox ADD CONSTRAINT segment_audience_outbox_aggregate_kind_check
  CHECK (aggregate_kind IN ('group','package','configuration','refresh_run','snapshot','member_event_batch'));
