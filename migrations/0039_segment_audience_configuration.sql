-- Owner: Segment/Audience.
-- Retention: configuration versions, receipts, audits and outbox facts are durable.
-- Strategy: forward-only. This migration owns no Customer, Identity, Staff or
-- Provider fact and deliberately has no cross-domain foreign key.

CREATE TABLE segment_audience_groups (
  id BIGSERIAL PRIMARY KEY,
  name TEXT NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 100),
  sort_order INTEGER NOT NULL DEFAULT 0 CHECK (sort_order >= 0),
  version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
  created_by BIGINT NOT NULL CHECK (created_by > 0),
  updated_by BIGINT NOT NULL CHECK (updated_by > 0),
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL
);
CREATE UNIQUE INDEX segment_audience_groups_name_unique ON segment_audience_groups(lower(btrim(name)));
CREATE INDEX segment_audience_groups_order_idx ON segment_audience_groups(sort_order, id);

CREATE TABLE segment_audience_packages (
  id BIGSERIAL PRIMARY KEY,
  group_id BIGINT REFERENCES segment_audience_groups(id) ON DELETE RESTRICT,
  code TEXT NOT NULL CHECK (code ~ '^[a-z0-9][a-z0-9_-]{0,119}$'),
  name TEXT NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 200),
  lifecycle TEXT NOT NULL DEFAULT 'paused' CHECK (lifecycle IN ('paused','active','archived')),
  version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
  current_configuration_version_id BIGINT,
  created_by BIGINT NOT NULL CHECK (created_by > 0),
  updated_by BIGINT NOT NULL CHECK (updated_by > 0),
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  archived_at TIMESTAMPTZ,
  CHECK ((lifecycle = 'archived') = (archived_at IS NOT NULL))
);
CREATE UNIQUE INDEX segment_audience_packages_code_unique ON segment_audience_packages(lower(code));
CREATE INDEX segment_audience_packages_visible_idx ON segment_audience_packages(group_id, updated_at DESC, id DESC) WHERE archived_at IS NULL;

CREATE TABLE segment_audience_configuration_versions (
  id BIGSERIAL PRIMARY KEY,
  package_id BIGINT NOT NULL REFERENCES segment_audience_packages(id) ON DELETE RESTRICT,
  version BIGINT NOT NULL CHECK (version > 0),
  schema_version INTEGER NOT NULL CHECK (schema_version = 1),
  definition JSONB NOT NULL CHECK (jsonb_typeof(definition) = 'object'),
  refresh_cron_utc TEXT CHECK (refresh_cron_utc IS NULL OR length(btrim(refresh_cron_utc)) BETWEEN 1 AND 100),
  digest BYTEA NOT NULL CHECK (octet_length(digest) = 32),
  created_by BIGINT NOT NULL CHECK (created_by > 0),
  created_at TIMESTAMPTZ NOT NULL,
  UNIQUE(package_id, version),
  UNIQUE(id, package_id)
);
ALTER TABLE segment_audience_packages
  ADD CONSTRAINT segment_audience_packages_current_configuration_fk
  FOREIGN KEY (current_configuration_version_id, id)
  REFERENCES segment_audience_configuration_versions(id, package_id)
  ON DELETE RESTRICT;

CREATE TABLE segment_audience_operation_receipts (
  id BIGSERIAL PRIMARY KEY,
  operation TEXT NOT NULL CHECK (length(btrim(operation)) BETWEEN 1 AND 100),
  actor_scope TEXT NOT NULL CHECK (length(btrim(actor_scope)) BETWEEN 1 AND 200),
  key_digest BYTEA NOT NULL CHECK (octet_length(key_digest) = 32),
  payload_digest BYTEA NOT NULL CHECK (octet_length(payload_digest) = 32),
  state TEXT NOT NULL CHECK (state IN ('reserved','completed')),
  result_snapshot JSONB,
  created_at TIMESTAMPTZ NOT NULL,
  completed_at TIMESTAMPTZ,
  CHECK ((state = 'completed') = (completed_at IS NOT NULL AND result_snapshot IS NOT NULL)),
  UNIQUE(operation, actor_scope, key_digest)
);

CREATE TABLE segment_audience_audit_events (
  id BIGSERIAL PRIMARY KEY,
  resource_kind TEXT NOT NULL CHECK (resource_kind IN ('group','package','configuration')),
  resource_id BIGINT NOT NULL CHECK (resource_id > 0),
  operation TEXT NOT NULL CHECK (length(btrim(operation)) BETWEEN 1 AND 100),
  actor_id BIGINT NOT NULL CHECK (actor_id > 0),
  occurred_at TIMESTAMPTZ NOT NULL,
  payload_digest BYTEA NOT NULL CHECK (octet_length(payload_digest) = 32)
);

CREATE TABLE segment_audience_outbox (
  id BIGSERIAL PRIMARY KEY,
  event_type TEXT NOT NULL CHECK (length(btrim(event_type)) BETWEEN 1 AND 160),
  aggregate_kind TEXT NOT NULL CHECK (aggregate_kind IN ('group','package','configuration')),
  aggregate_id BIGINT NOT NULL CHECK (aggregate_id > 0),
  payload JSONB NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
  idempotency_digest BYTEA NOT NULL CHECK (octet_length(idempotency_digest) = 32),
  occurred_at TIMESTAMPTZ NOT NULL
);
CREATE UNIQUE INDEX segment_audience_outbox_idempotency_unique ON segment_audience_outbox(event_type, idempotency_digest);

CREATE OR REPLACE FUNCTION segment_audience_append_only() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'segment audience immutable facts are append-only';
END
$$;
CREATE TRIGGER segment_audience_configuration_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON segment_audience_configuration_versions FOR EACH STATEMENT EXECUTE FUNCTION segment_audience_append_only();
CREATE TRIGGER segment_audience_audit_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON segment_audience_audit_events FOR EACH STATEMENT EXECUTE FUNCTION segment_audience_append_only();
CREATE TRIGGER segment_audience_outbox_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON segment_audience_outbox FOR EACH STATEMENT EXECUTE FUNCTION segment_audience_append_only();
