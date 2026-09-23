-- Owner: Radar. Public access state and immutable behavioral facts.
-- Raw external identity values remain Identity-owned.

CREATE TABLE radar_oauth_states (
  state_digest BYTEA PRIMARY KEY CHECK(octet_length(state_digest) = 32),
  radar_id BIGINT NOT NULL REFERENCES radar_links(id) ON DELETE RESTRICT,
  radar_version BIGINT NOT NULL CHECK(radar_version > 0),
  redirect_path TEXT NOT NULL CHECK(redirect_path ~ '^/r/[A-Za-z0-9_-]+$' AND length(redirect_path) <= 256),
  expires_at TIMESTAMPTZ NOT NULL,
  consumed_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL,
  FOREIGN KEY(radar_id, radar_version) REFERENCES radar_link_versions(radar_id, version) ON DELETE RESTRICT,
  CHECK(consumed_at IS NULL OR consumed_at >= created_at)
);
CREATE INDEX radar_oauth_states_expiry_idx ON radar_oauth_states(expires_at) WHERE consumed_at IS NULL;

CREATE TABLE radar_view_sessions (
  id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  session_digest BYTEA NOT NULL UNIQUE CHECK(octet_length(session_digest) = 32),
  radar_id BIGINT NOT NULL REFERENCES radar_links(id) ON DELETE RESTRICT,
  radar_version BIGINT NOT NULL CHECK(radar_version > 0),
  identity_id BIGINT REFERENCES customer_identities(id) ON DELETE RESTRICT,
  customer_id BIGINT REFERENCES customers(id) ON DELETE RESTRICT,
  attribution_status TEXT NOT NULL CHECK(attribution_status IN ('anonymous','resolved','pending','conflict','failed')),
  evidence_digest BYTEA CHECK(evidence_digest IS NULL OR octet_length(evidence_digest) = 32),
  expires_at TIMESTAMPTZ NOT NULL,
  revoked_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL,
  FOREIGN KEY(radar_id, radar_version) REFERENCES radar_link_versions(radar_id, version) ON DELETE RESTRICT,
  CONSTRAINT radar_view_sessions_identity_shape CHECK(
    (attribution_status = 'resolved' AND identity_id IS NOT NULL AND customer_id IS NOT NULL AND evidence_digest IS NOT NULL)
    OR (attribution_status <> 'resolved' AND identity_id IS NULL AND customer_id IS NULL)
  )
);
CREATE INDEX radar_view_sessions_radar_idx ON radar_view_sessions(radar_id, created_at DESC, id DESC);
CREATE INDEX radar_view_sessions_identity_idx ON radar_view_sessions(radar_id, identity_id) WHERE identity_id IS NOT NULL;

CREATE TABLE radar_events (
  id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  receipt_id TEXT NOT NULL UNIQUE CHECK(receipt_id <> '' AND length(receipt_id) <= 128),
  radar_id BIGINT NOT NULL REFERENCES radar_links(id) ON DELETE RESTRICT,
  radar_version BIGINT NOT NULL CHECK(radar_version > 0),
  session_id BIGINT NOT NULL REFERENCES radar_view_sessions(id) ON DELETE RESTRICT,
  stage TEXT NOT NULL CHECK(stage IN ('landing','oauth_started','oauth_verified','identity_resolved','content_opened','redirected','image_loaded','pdf_opened','failed')),
  attribution_status TEXT NOT NULL CHECK(attribution_status IN ('anonymous','resolved','pending','conflict','failed')),
  identity_id BIGINT REFERENCES customer_identities(id) ON DELETE RESTRICT,
  customer_id BIGINT REFERENCES customers(id) ON DELETE RESTRICT,
  key_digest BYTEA NOT NULL CHECK(octet_length(key_digest) = 32),
  payload_digest BYTEA NOT NULL CHECK(octet_length(payload_digest) = 32),
  failure_code TEXT CHECK(failure_code IS NULL OR (failure_code <> '' AND length(failure_code) <= 80)),
  occurred_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ NOT NULL,
  FOREIGN KEY(radar_id, radar_version) REFERENCES radar_link_versions(radar_id, version) ON DELETE RESTRICT,
  UNIQUE(session_id, radar_version, stage),
  CONSTRAINT radar_events_identity_shape CHECK(
    (attribution_status = 'resolved' AND identity_id IS NOT NULL AND customer_id IS NOT NULL)
    OR (attribution_status <> 'resolved' AND identity_id IS NULL AND customer_id IS NULL)
  )
);
CREATE INDEX radar_events_radar_time_idx ON radar_events(radar_id, occurred_at DESC, id DESC);
CREATE INDEX radar_events_radar_stage_idx ON radar_events(radar_id, stage, occurred_at DESC, id DESC);
CREATE INDEX radar_events_radar_identity_idx ON radar_events(radar_id, identity_id) WHERE identity_id IS NOT NULL;

CREATE TRIGGER radar_events_guard BEFORE UPDATE OR DELETE OR TRUNCATE ON radar_events FOR EACH STATEMENT EXECUTE FUNCTION radar_reject_immutable_mutation();
