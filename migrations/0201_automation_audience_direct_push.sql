-- Owners: Automation (direct-push facts) and Outbound (provider receipts).
-- OneID values are retained only by Identity; immutable message snapshots
-- retain content facts while protocol receipts keep digests, never signatures.

CREATE TABLE automation_audience_push_configs (
  package_id BIGINT PRIMARY KEY CHECK(package_id>0),
  webhook_reference TEXT NOT NULL UNIQUE,
  enabled BOOLEAN NOT NULL DEFAULT false,
  max_per_customer_24h SMALLINT NOT NULL DEFAULT 1 CHECK(max_per_customer_24h BETWEEN 1 AND 100),
  version BIGINT NOT NULL DEFAULT 1 CHECK(version>0),
  updated_by BIGINT NOT NULL CHECK(updated_by>0),
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  CHECK(webhook_reference ~ '^[A-Za-z0-9._:-]{1,128}$' AND position('://' IN webhook_reference)=0)
);

CREATE TABLE automation_audience_push_batches (
  id BIGSERIAL PRIMARY KEY,
  package_id BIGINT NOT NULL CHECK(package_id>0),
  event_id_digest BYTEA NOT NULL CHECK(octet_length(event_id_digest)=32),
  payload_digest BYTEA NOT NULL CHECK(octet_length(payload_digest)=32),
  result_snapshot JSONB NOT NULL CHECK(jsonb_typeof(result_snapshot)='object'),
  accepted_count INTEGER NOT NULL CHECK(accepted_count BETWEEN 0 AND 1000),
  rejected_count INTEGER NOT NULL CHECK(rejected_count BETWEEN 0 AND 1000),
  created_at TIMESTAMPTZ NOT NULL,
  UNIQUE(package_id,event_id_digest),
  CHECK(accepted_count+rejected_count BETWEEN 1 AND 1000)
);

CREATE TABLE automation_audience_push_config_receipts (
  idempotency_key TEXT PRIMARY KEY CHECK(char_length(idempotency_key) BETWEEN 8 AND 200),
  package_id BIGINT NOT NULL CHECK(package_id>0),
  payload_digest BYTEA NOT NULL CHECK(octet_length(payload_digest)=32),
  result_snapshot JSONB NOT NULL CHECK(jsonb_typeof(result_snapshot)='object'),
  created_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE automation_audience_push_config_audit_events (
  id BIGSERIAL PRIMARY KEY,
  package_id BIGINT NOT NULL CHECK(package_id>0),
  operation TEXT NOT NULL CHECK(operation IN ('configured')),
  actor_id BIGINT NOT NULL CHECK(actor_id>0),
  payload_digest BYTEA NOT NULL CHECK(octet_length(payload_digest)=32),
  occurred_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE automation_audience_push_items (
  id BIGSERIAL PRIMARY KEY,
  batch_id BIGINT NOT NULL REFERENCES automation_audience_push_batches(id) ON DELETE RESTRICT,
  package_id BIGINT NOT NULL CHECK(package_id>0),
  snapshot_id BIGINT NOT NULL CHECK(snapshot_id>0),
  identity_id BIGINT NOT NULL CHECK(identity_id>0),
  customer_id BIGINT NOT NULL CHECK(customer_id>0),
  sender_staff_id BIGINT NOT NULL CHECK(sender_staff_id>0),
  sender_set_version BIGINT NOT NULL CHECK(sender_set_version>0),
  miniprogram_id BIGINT NOT NULL CHECK(miniprogram_id>0),
  client_reference TEXT NOT NULL DEFAULT '' CHECK(char_length(client_reference)<=128),
  content_snapshot JSONB NOT NULL CHECK(jsonb_typeof(content_snapshot)='object'),
  content_snapshot_digest BYTEA NOT NULL CHECK(octet_length(content_snapshot_digest)=32),
  outbound_intent_id BIGINT UNIQUE REFERENCES outbound_message_intents(id) ON DELETE RESTRICT,
  effect_id TEXT UNIQUE CHECK(effect_id IS NULL OR effect_id ~ '^eer_[1-9][0-9]*$'),
  send_state TEXT NOT NULL CHECK(send_state IN ('accepted','queued','attempted','provider_accepted','delivery_proven','retryable_failed','final_failed','outcome_unknown','reconciled','cancelled')),
  observation_state TEXT NOT NULL DEFAULT 'not_started' CHECK(observation_state IN ('not_started','observing','opened','not_opened','unavailable')),
  failure_code TEXT NOT NULL DEFAULT '' CHECK(char_length(failure_code)<=100),
  observation_reason TEXT NOT NULL DEFAULT '' CHECK(char_length(observation_reason)<=100),
  sent_at TIMESTAMPTZ,
  observation_due_at TIMESTAMPTZ,
  observed_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  UNIQUE(batch_id,id)
);
CREATE INDEX automation_audience_push_items_package_created_idx ON automation_audience_push_items(package_id,created_at DESC,id DESC);
CREATE INDEX automation_audience_push_items_rate_idx ON automation_audience_push_items(package_id,customer_id,created_at DESC) WHERE send_state NOT IN ('cancelled');
CREATE INDEX automation_audience_push_items_effect_idx ON automation_audience_push_items(effect_id) WHERE effect_id IS NOT NULL;
CREATE INDEX automation_audience_push_items_observation_idx ON automation_audience_push_items(observation_due_at,id) WHERE observation_state IN ('not_started','observing');

CREATE TABLE automation_audience_push_audit_events (
  id BIGSERIAL PRIMARY KEY,
  batch_id BIGINT NOT NULL REFERENCES automation_audience_push_batches(id) ON DELETE RESTRICT,
  event_type TEXT NOT NULL CHECK(event_type IN ('automation.audience_push.accepted.v1','automation.audience_push.observed.v1')),
  payload_digest BYTEA NOT NULL CHECK(octet_length(payload_digest)=32),
  occurred_at TIMESTAMPTZ NOT NULL
);
CREATE TABLE automation_audience_push_outbox (
  id BIGSERIAL PRIMARY KEY,
  event_type TEXT NOT NULL,
  batch_id BIGINT NOT NULL REFERENCES automation_audience_push_batches(id) ON DELETE RESTRICT,
  payload JSONB NOT NULL CHECK(jsonb_typeof(payload)='object'),
  idempotency_digest BYTEA NOT NULL CHECK(octet_length(idempotency_digest)=32),
  occurred_at TIMESTAMPTZ NOT NULL,
  UNIQUE(event_type,idempotency_digest)
);

-- Direct audience messages use the same immutable Automation message effect.
ALTER TABLE outbound_message_intents DROP CONSTRAINT outbound_message_intents_source_kind_check;
ALTER TABLE outbound_message_intents ADD CONSTRAINT outbound_message_intents_source_kind_check
  CHECK(source_kind IN ('automation_run','automation_enrollment','audience_direct_push'));
ALTER TABLE outbound_message_intents DROP CONSTRAINT outbound_message_intents_agent_id_check;
ALTER TABLE outbound_message_intents DROP CONSTRAINT outbound_message_intents_agent_published_version_check;
ALTER TABLE outbound_message_intents ADD CONSTRAINT outbound_message_intents_agent_shape CHECK(
  (source_kind IN ('automation_run','automation_enrollment') AND agent_id>0 AND agent_published_version>0)
  OR (source_kind='audience_direct_push' AND agent_id=0 AND agent_published_version=0)
);

CREATE TABLE outbound_message_receipts (
  content_reference TEXT PRIMARY KEY,
  message_intent_id BIGINT NOT NULL UNIQUE REFERENCES outbound_message_intents(id) ON DELETE RESTRICT,
  message_id TEXT NOT NULL DEFAULT '',
  sender_userid TEXT NOT NULL DEFAULT '',
  external_userid TEXT NOT NULL DEFAULT '',
  reason TEXT NOT NULL DEFAULT '',
  delivery_status INTEGER,
  sent_at TIMESTAMPTZ,
  observed_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
