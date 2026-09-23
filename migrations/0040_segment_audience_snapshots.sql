-- Owner: Segment/Audience. Forward-only durable snapshot and refresh facts.
-- Customer IDs are canonical OneIDs supplied through the Customer Port; this
-- migration contains no external identity or Provider identifier.

CREATE TABLE segment_audience_refresh_runs (
  id BIGSERIAL PRIMARY KEY,
  package_id BIGINT NOT NULL REFERENCES segment_audience_packages(id) ON DELETE RESTRICT,
  configuration_version_id BIGINT NOT NULL,
  source_key_digest BYTEA NOT NULL CHECK (octet_length(source_key_digest)=32),
  reference_time TIMESTAMPTZ NOT NULL,
  state TEXT NOT NULL CHECK (state IN ('accepted','queued','evaluating','staging','published','failed')),
  river_job_id BIGINT,
  error_code TEXT CHECK (error_code IS NULL OR length(error_code)<=100),
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  completed_at TIMESTAMPTZ,
  UNIQUE(package_id,source_key_digest),
  FOREIGN KEY (configuration_version_id,package_id) REFERENCES segment_audience_configuration_versions(id,package_id) ON DELETE RESTRICT
);

CREATE TABLE segment_audience_snapshots (
  id BIGSERIAL PRIMARY KEY,
  package_id BIGINT NOT NULL REFERENCES segment_audience_packages(id) ON DELETE RESTRICT,
  configuration_version_id BIGINT NOT NULL,
  refresh_run_id BIGINT NOT NULL UNIQUE REFERENCES segment_audience_refresh_runs(id) ON DELETE RESTRICT,
  state TEXT NOT NULL CHECK (state IN ('preparing','published','failed')),
  reference_time TIMESTAMPTZ NOT NULL,
  member_count BIGINT NOT NULL DEFAULT 0 CHECK (member_count BETWEEN 0 AND 100000),
  member_digest BYTEA CHECK (member_digest IS NULL OR octet_length(member_digest)=32),
  source_watermark_digest BYTEA CHECK (source_watermark_digest IS NULL OR octet_length(source_watermark_digest)=32),
  created_at TIMESTAMPTZ NOT NULL,
  published_at TIMESTAMPTZ,
  UNIQUE(id,package_id),
  FOREIGN KEY (configuration_version_id,package_id) REFERENCES segment_audience_configuration_versions(id,package_id) ON DELETE RESTRICT,
  CHECK ((state='published')=(published_at IS NOT NULL AND member_digest IS NOT NULL AND source_watermark_digest IS NOT NULL))
);

CREATE TABLE segment_audience_snapshot_members (
  snapshot_id BIGINT NOT NULL REFERENCES segment_audience_snapshots(id) ON DELETE RESTRICT,
  customer_id BIGINT NOT NULL CHECK (customer_id>0),
  entered_at TIMESTAMPTZ NOT NULL,
  identity_disposition TEXT NOT NULL CHECK (identity_disposition = 'resolved'),
  PRIMARY KEY(snapshot_id,customer_id)
);
CREATE INDEX segment_audience_snapshot_members_customer_idx ON segment_audience_snapshot_members(customer_id,snapshot_id);

CREATE TABLE segment_audience_refresh_batches (
  refresh_run_id BIGINT NOT NULL REFERENCES segment_audience_refresh_runs(id) ON DELETE RESTRICT,
  batch_ordinal INTEGER NOT NULL CHECK (batch_ordinal>=0),
  first_customer_id BIGINT NOT NULL CHECK (first_customer_id>0),
  last_customer_id BIGINT NOT NULL CHECK (last_customer_id>=first_customer_id),
  member_count INTEGER NOT NULL CHECK (member_count BETWEEN 1 AND 1000),
  member_digest BYTEA NOT NULL CHECK (octet_length(member_digest)=32),
  completed_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY(refresh_run_id,batch_ordinal)
);

ALTER TABLE segment_audience_packages ADD COLUMN published_snapshot_id BIGINT;
ALTER TABLE segment_audience_packages ADD CONSTRAINT segment_audience_packages_published_snapshot_fk
  FOREIGN KEY (published_snapshot_id,id) REFERENCES segment_audience_snapshots(id,package_id) ON DELETE RESTRICT;

CREATE TRIGGER segment_audience_snapshot_members_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON segment_audience_snapshot_members FOR EACH STATEMENT EXECUTE FUNCTION segment_audience_append_only();
CREATE TRIGGER segment_audience_refresh_batches_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON segment_audience_refresh_batches FOR EACH STATEMENT EXECUTE FUNCTION segment_audience_append_only();

ALTER TABLE segment_audience_audit_events DROP CONSTRAINT segment_audience_audit_events_resource_kind_check;
ALTER TABLE segment_audience_audit_events ADD CONSTRAINT segment_audience_audit_events_resource_kind_check
  CHECK (resource_kind IN ('group','package','configuration','refresh_run','snapshot'));
ALTER TABLE segment_audience_outbox DROP CONSTRAINT segment_audience_outbox_aggregate_kind_check;
ALTER TABLE segment_audience_outbox ADD CONSTRAINT segment_audience_outbox_aggregate_kind_check
  CHECK (aggregate_kind IN ('group','package','configuration','refresh_run','snapshot'));
