-- Owner: internal/channel. This is a callback-scoped welcome intent, not an
-- execution queue. The External Effects/River rows accepted with it remain
-- the sole durable execution kernel.
-- The new queue name only isolates the existing welcome effect from ordinary
-- outbound backlog; it still uses the same River job and External Effects
-- receipt/claim/retry model.
ALTER TABLE external_effect_jobs DROP CONSTRAINT IF EXISTS external_effect_jobs_queue_check;
ALTER TABLE external_effect_jobs ADD CONSTRAINT external_effect_jobs_queue_check CHECK(queue IN ('outbound','outbound_welcome'));

CREATE TABLE channel_welcome_intents (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    callback_id TEXT NOT NULL UNIQUE CHECK(callback_id=btrim(callback_id) AND char_length(callback_id) BETWEEN 1 AND 512),
    channel_id BIGINT,
    config_version BIGINT,
    customer_id BIGINT REFERENCES customers(id) ON DELETE RESTRICT,
    welcome_grant_ref TEXT NOT NULL CHECK(welcome_grant_ref ~ '^wgrant_[1-9a-f][0-9a-f]*$'),
    welcome_material_snapshot JSONB,
    source_ref_digest TEXT NOT NULL UNIQUE CHECK(source_ref_digest ~ '^sha256:[0-9a-f]{64}$'),
    intent_digest BYTEA NOT NULL CHECK(octet_length(intent_digest)=32),
    effect_ref TEXT UNIQUE CHECK(effect_ref IS NULL OR effect_ref ~ '^eer_[1-9][0-9]*$'),
    accept_receipt_ref TEXT CHECK(accept_receipt_ref IS NULL OR accept_receipt_ref ~ '^eerop_[1-9][0-9]*$'),
    queue_receipt_ref TEXT CHECK(queue_receipt_ref IS NULL OR queue_receipt_ref ~ '^eerop_[1-9][0-9]*$'),
    first_received_at TIMESTAMPTZ NOT NULL,
    send_deadline_at TIMESTAMPTZ NOT NULL CHECK(send_deadline_at=first_received_at+interval '20 seconds'),
    state TEXT NOT NULL CHECK(state IN ('queued','executed','outcome_unknown','retryable_failed','final_failed','state_unmatched','state_ambiguous','channel_unavailable','welcome_not_configured','welcome_material_unavailable')),
    result_digest TEXT CHECK(result_digest IS NULL OR result_digest ~ '^sha256:[0-9a-f]{64}$'),
    -- This is a small, safe, Channel-owned explanation of the terminal or
    -- pending outcome. It never contains a callback payload, welcome code, or
    -- Provider response. The digest remains the tamper-evident receipt.
    result_reason TEXT CHECK(result_reason IS NULL OR result_reason IN ('state_unmatched','state_ambiguous','channel_unavailable','welcome_not_configured','welcome_material_unavailable','deadline_missing','deadline_expired','grant_expired','material_invalid','provider_unavailable','outcome_unknown','sent','final_failed')),
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK(attempt_count>=0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CHECK((channel_id IS NULL) = (config_version IS NULL)),
    -- A resolved channel may intentionally have no effect when its configured
    -- welcome is absent or its frozen material cannot be used. Those are
    -- durable no-send outcomes, while receipt fields remain all-or-none.
    CHECK((effect_ref IS NULL) = (accept_receipt_ref IS NULL)
      AND (effect_ref IS NULL) = (queue_receipt_ref IS NULL)),
    CHECK((channel_id IS NULL AND state IN ('state_unmatched','state_ambiguous','channel_unavailable'))
       OR (channel_id IS NOT NULL AND state IN ('queued','executed','outcome_unknown','retryable_failed','final_failed','welcome_not_configured','welcome_material_unavailable'))),
    CHECK((effect_ref IS NOT NULL) = (state IN ('queued','executed','outcome_unknown','retryable_failed','final_failed'))),
    CHECK((state='outcome_unknown') = COALESCE(result_reason='outcome_unknown',FALSE)),
    CONSTRAINT channel_welcome_intents_config_fk FOREIGN KEY(channel_id,config_version)
        REFERENCES channel_config_versions(channel_id,config_version) ON DELETE RESTRICT
);

CREATE INDEX channel_welcome_intents_channel_timeline_idx ON channel_welcome_intents(channel_id,id DESC);
CREATE INDEX channel_welcome_intents_customer_idx ON channel_welcome_intents(customer_id) WHERE customer_id IS NOT NULL;

CREATE FUNCTION channel_welcome_intent_guard() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
  IF TG_OP IN ('DELETE','TRUNCATE')
     OR NEW.callback_id IS DISTINCT FROM OLD.callback_id
     OR NEW.channel_id IS DISTINCT FROM OLD.channel_id
     OR NEW.config_version IS DISTINCT FROM OLD.config_version
     OR NEW.welcome_grant_ref IS DISTINCT FROM OLD.welcome_grant_ref
     OR NEW.welcome_material_snapshot IS DISTINCT FROM OLD.welcome_material_snapshot
     OR NEW.source_ref_digest IS DISTINCT FROM OLD.source_ref_digest
     OR NEW.intent_digest IS DISTINCT FROM OLD.intent_digest
     OR NEW.effect_ref IS DISTINCT FROM OLD.effect_ref
     OR NEW.accept_receipt_ref IS DISTINCT FROM OLD.accept_receipt_ref
     OR NEW.queue_receipt_ref IS DISTINCT FROM OLD.queue_receipt_ref
     OR NEW.first_received_at IS DISTINCT FROM OLD.first_received_at
     OR NEW.send_deadline_at IS DISTINCT FROM OLD.send_deadline_at
     OR NEW.created_at IS DISTINCT FROM OLD.created_at
     OR (OLD.customer_id IS NOT NULL AND NEW.customer_id IS DISTINCT FROM OLD.customer_id)
     OR (OLD.customer_id IS NULL AND NEW.customer_id IS NOT NULL AND NEW.customer_id <= 0)
  THEN RAISE EXCEPTION 'channel welcome intent identity is immutable'; END IF;
  RETURN NEW;
END; $$;
CREATE TRIGGER channel_welcome_intents_guard BEFORE UPDATE OR DELETE ON channel_welcome_intents FOR EACH ROW EXECUTE FUNCTION channel_welcome_intent_guard();
CREATE TRIGGER channel_welcome_intents_no_truncate BEFORE TRUNCATE ON channel_welcome_intents FOR EACH STATEMENT EXECUTE FUNCTION channel_welcome_intent_guard();
