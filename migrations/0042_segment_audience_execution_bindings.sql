-- Owner: Segment/Audience. Cross-domain references are opaque snapshots only.
CREATE TABLE segment_audience_automation_binding_versions (
  id BIGSERIAL PRIMARY KEY,
  package_id BIGINT NOT NULL REFERENCES segment_audience_packages(id) ON DELETE RESTRICT,
  version BIGINT NOT NULL CHECK(version>0),
  agent_id BIGINT NOT NULL CHECK(agent_id>0),
  automation_type TEXT NOT NULL CHECK(automation_type IN ('agent','fixed_script')),
  agent_published_version BIGINT NOT NULL CHECK(agent_published_version>0),
  content_digest BYTEA NOT NULL CHECK(octet_length(content_digest)=32),
  materials_digest BYTEA NOT NULL CHECK(octet_length(materials_digest)=32),
  created_by BIGINT NOT NULL CHECK(created_by>0),
  created_at TIMESTAMPTZ NOT NULL,
  UNIQUE(package_id,version), UNIQUE(id,package_id)
);
CREATE TRIGGER segment_audience_binding_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON segment_audience_automation_binding_versions FOR EACH STATEMENT EXECUTE FUNCTION segment_audience_append_only();
ALTER TABLE segment_audience_packages ADD COLUMN current_automation_binding_id BIGINT;
ALTER TABLE segment_audience_packages ADD CONSTRAINT segment_audience_packages_current_binding_fk FOREIGN KEY(current_automation_binding_id,id) REFERENCES segment_audience_automation_binding_versions(id,package_id) ON DELETE RESTRICT;

CREATE TABLE segment_audience_sender_sets (
  id BIGSERIAL PRIMARY KEY,
  package_id BIGINT NOT NULL REFERENCES segment_audience_packages(id) ON DELETE RESTRICT,
  version BIGINT NOT NULL CHECK(version>0),
  created_by BIGINT NOT NULL CHECK(created_by>0),
  created_at TIMESTAMPTZ NOT NULL,
  UNIQUE(package_id,version), UNIQUE(id,package_id)
);
CREATE TABLE segment_audience_sender_set_members (
  sender_set_id BIGINT NOT NULL REFERENCES segment_audience_sender_sets(id) ON DELETE RESTRICT,
  sort_order SMALLINT NOT NULL CHECK(sort_order BETWEEN 1 AND 5),
  staff_id BIGINT NOT NULL CHECK(staff_id>0),
  eligibility_version BIGINT NOT NULL CHECK(eligibility_version>0),
  eligibility_refreshed_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY(sender_set_id,sort_order), UNIQUE(sender_set_id,staff_id)
);
CREATE TRIGGER segment_audience_sender_sets_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON segment_audience_sender_sets FOR EACH STATEMENT EXECUTE FUNCTION segment_audience_append_only();
CREATE TRIGGER segment_audience_sender_members_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON segment_audience_sender_set_members FOR EACH STATEMENT EXECUTE FUNCTION segment_audience_append_only();
ALTER TABLE segment_audience_packages ADD COLUMN current_sender_set_id BIGINT;
ALTER TABLE segment_audience_packages ADD CONSTRAINT segment_audience_packages_current_sender_set_fk FOREIGN KEY(current_sender_set_id,id) REFERENCES segment_audience_sender_sets(id,package_id) ON DELETE RESTRICT;

ALTER TABLE segment_audience_audit_events DROP CONSTRAINT segment_audience_audit_events_resource_kind_check;
ALTER TABLE segment_audience_audit_events ADD CONSTRAINT segment_audience_audit_events_resource_kind_check CHECK(resource_kind IN ('group','package','configuration','refresh_run','snapshot','webhook_receipt','binding','sender_set'));
ALTER TABLE segment_audience_outbox DROP CONSTRAINT segment_audience_outbox_aggregate_kind_check;
ALTER TABLE segment_audience_outbox ADD CONSTRAINT segment_audience_outbox_aggregate_kind_check CHECK(aggregate_kind IN ('group','package','configuration','refresh_run','snapshot','webhook_receipt','binding','sender_set'));
