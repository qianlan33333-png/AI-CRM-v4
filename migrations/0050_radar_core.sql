-- Owner: Radar. Local configuration, idempotency, audit and domain outbox.
-- No external identity values are stored in this schema.

CREATE FUNCTION radar_reject_immutable_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'radar immutable facts cannot be changed';
END;
$$;

CREATE TABLE radar_links (
  id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  public_code TEXT NOT NULL UNIQUE CHECK(public_code ~ '^rd_[A-Za-z0-9_-]{16,64}$'),
  name TEXT NOT NULL CHECK(name <> '' AND length(name) <= 120 AND name = btrim(name)),
  title TEXT NOT NULL CHECK(title <> '' AND length(title) <= 200 AND title = btrim(title)),
  description TEXT NOT NULL DEFAULT '' CHECK(length(description) <= 2000 AND description = btrim(description)),
  content_type TEXT NOT NULL CHECK(content_type IN ('link','image','pdf')),
  destination_url TEXT CHECK(destination_url IS NULL OR (destination_url LIKE 'https://%' AND octet_length(destination_url) <= 2048)),
  media_id BIGINT CHECK(media_id IS NULL OR media_id > 0),
  auth_policy TEXT NOT NULL CHECK(auth_policy IN ('anonymous','unionid_required')),
  status TEXT NOT NULL CHECK(status IN ('draft','enabled','disabled')),
  version BIGINT NOT NULL DEFAULT 1 CHECK(version > 0),
  created_by BIGINT NOT NULL CHECK(created_by > 0),
  updated_by BIGINT NOT NULL CHECK(updated_by > 0),
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL CHECK(updated_at >= created_at),
  CONSTRAINT radar_links_content_shape CHECK(
    (content_type = 'link' AND destination_url IS NOT NULL AND media_id IS NULL)
    OR (content_type IN ('image','pdf') AND destination_url IS NULL AND media_id IS NOT NULL)
  )
);
CREATE INDEX radar_links_status_updated_idx ON radar_links(status, updated_at DESC, id DESC);
CREATE INDEX radar_links_type_updated_idx ON radar_links(content_type, updated_at DESC, id DESC);

CREATE TABLE radar_link_versions (
  radar_id BIGINT NOT NULL REFERENCES radar_links(id) ON DELETE RESTRICT,
  version BIGINT NOT NULL CHECK(version > 0),
  snapshot JSONB NOT NULL CHECK(jsonb_typeof(snapshot) = 'object'),
  actor_id BIGINT NOT NULL CHECK(actor_id > 0),
  created_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY(radar_id, version)
);

CREATE TABLE radar_operation_receipts (
  id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  operation TEXT NOT NULL CHECK(operation IN ('create','update','enable','disable','export')),
  actor_id BIGINT NOT NULL CHECK(actor_id > 0),
  key_digest BYTEA NOT NULL CHECK(octet_length(key_digest) = 32),
  payload_digest BYTEA NOT NULL CHECK(octet_length(payload_digest) = 32),
  state TEXT NOT NULL CHECK(state IN ('in_progress','completed')),
  radar_id BIGINT REFERENCES radar_links(id) ON DELETE RESTRICT,
  version BIGINT CHECK(version IS NULL OR version > 0),
  result_snapshot JSONB CHECK(result_snapshot IS NULL OR jsonb_typeof(result_snapshot) = 'object'),
  created_at TIMESTAMPTZ NOT NULL,
  completed_at TIMESTAMPTZ,
  CONSTRAINT radar_operation_receipts_key_unique UNIQUE(operation, actor_id, key_digest),
  CONSTRAINT radar_operation_receipts_completion CHECK(
    (state = 'in_progress' AND radar_id IS NULL AND version IS NULL AND result_snapshot IS NULL AND completed_at IS NULL)
    OR (state = 'completed' AND radar_id IS NOT NULL AND version IS NOT NULL AND result_snapshot IS NOT NULL AND completed_at IS NOT NULL)
  )
);
CREATE INDEX radar_operation_receipts_created_idx ON radar_operation_receipts(created_at DESC, id DESC);

CREATE TABLE radar_audit_events (
  id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  operation TEXT NOT NULL,
  radar_id BIGINT NOT NULL REFERENCES radar_links(id) ON DELETE RESTRICT,
  version BIGINT NOT NULL CHECK(version > 0),
  actor_id BIGINT NOT NULL CHECK(actor_id > 0),
  payload_digest BYTEA NOT NULL CHECK(octet_length(payload_digest) = 32),
  occurred_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX radar_audit_events_radar_idx ON radar_audit_events(radar_id, occurred_at DESC, id DESC);

CREATE TABLE radar_outbox (
  id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  event_id TEXT NOT NULL UNIQUE CHECK(event_id <> '' AND length(event_id) <= 128),
  event_type TEXT NOT NULL CHECK(event_type ~ '^radar[.][a-z_]+$'),
  aggregate_id BIGINT NOT NULL REFERENCES radar_links(id) ON DELETE RESTRICT,
  aggregate_version BIGINT NOT NULL CHECK(aggregate_version > 0),
  payload JSONB NOT NULL CHECK(jsonb_typeof(payload) = 'object'),
  idempotency_digest BYTEA NOT NULL CHECK(octet_length(idempotency_digest) = 32),
  occurred_at TIMESTAMPTZ NOT NULL,
  published_at TIMESTAMPTZ,
  UNIQUE(event_type, idempotency_digest)
);
CREATE INDEX radar_outbox_unpublished_idx ON radar_outbox(id) WHERE published_at IS NULL;

CREATE TRIGGER radar_link_versions_guard BEFORE UPDATE OR DELETE OR TRUNCATE ON radar_link_versions FOR EACH STATEMENT EXECUTE FUNCTION radar_reject_immutable_mutation();
CREATE TRIGGER radar_audit_events_guard BEFORE UPDATE OR DELETE OR TRUNCATE ON radar_audit_events FOR EACH STATEMENT EXECUTE FUNCTION radar_reject_immutable_mutation();
