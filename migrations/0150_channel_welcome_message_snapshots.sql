-- Owner: internal/channel. Forward-only. This adds an encrypted, immutable
-- Channel snapshot for the rendered welcome body; External Effects/River
-- remains the only execution, retry, receipt, and reconciliation kernel.
-- No plaintext message, customer display name, callback body, welcome code, or
-- external identity is persisted here.
ALTER TABLE channel_welcome_intents
    ADD COLUMN effect_target_digest TEXT,
    ADD COLUMN effect_payload_digest TEXT,
    ADD COLUMN effect_policy_digest TEXT,
    ADD COLUMN effect_envelope_fingerprint TEXT;

ALTER TABLE channel_welcome_intents
    ADD CONSTRAINT channel_welcome_intents_effect_envelope_columns_check CHECK (
        num_nonnulls(effect_target_digest,effect_payload_digest,effect_policy_digest,effect_envelope_fingerprint) IN (0,4)
        AND (
            (effect_target_digest IS NULL AND effect_payload_digest IS NULL AND effect_policy_digest IS NULL AND effect_envelope_fingerprint IS NULL)
            OR
            (effect_target_digest ~ '^sha256:[0-9a-f]{64}$' AND effect_payload_digest ~ '^sha256:[0-9a-f]{64}$'
             AND effect_policy_digest ~ '^sha256:[0-9a-f]{64}$' AND effect_envelope_fingerprint ~ '^sha256:[0-9a-f]{64}$')
        )
    );

ALTER TABLE channel_welcome_intents
    DROP CONSTRAINT channel_welcome_intents_result_reason_check;
ALTER TABLE channel_welcome_intents
    ADD CONSTRAINT channel_welcome_intents_result_reason_check CHECK(result_reason IS NULL OR result_reason IN (
        'state_unmatched','state_ambiguous','channel_unavailable','welcome_not_configured','welcome_material_unavailable',
        'deadline_missing','deadline_expired','grant_expired','material_invalid','provider_unavailable','outcome_unknown',
        'sent','final_failed','customer_name_unavailable','welcome_template_invalid','welcome_message_too_long','frozen_message_unavailable'
    ));

-- Preserve the 0066 immutable intent guard while making the new envelope facts
-- write-once. Existing pre-0150 records retain NULL facts and fail closed if a
-- later worker cannot prove its envelope binding.
CREATE OR REPLACE FUNCTION channel_welcome_intent_guard() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
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
     OR NEW.effect_target_digest IS DISTINCT FROM OLD.effect_target_digest
     OR NEW.effect_payload_digest IS DISTINCT FROM OLD.effect_payload_digest
     OR NEW.effect_policy_digest IS DISTINCT FROM OLD.effect_policy_digest
     OR NEW.effect_envelope_fingerprint IS DISTINCT FROM OLD.effect_envelope_fingerprint
     OR NEW.first_received_at IS DISTINCT FROM OLD.first_received_at
     OR NEW.send_deadline_at IS DISTINCT FROM OLD.send_deadline_at
     OR NEW.created_at IS DISTINCT FROM OLD.created_at
     OR (OLD.customer_id IS NOT NULL AND NEW.customer_id IS DISTINCT FROM OLD.customer_id)
     OR (OLD.customer_id IS NULL AND NEW.customer_id IS NOT NULL AND NEW.customer_id <= 0)
  THEN RAISE EXCEPTION 'channel welcome intent identity is immutable'; END IF;
  RETURN NEW;
END; $$;

CREATE TABLE channel_welcome_message_snapshots (
    welcome_intent_id BIGINT PRIMARY KEY REFERENCES channel_welcome_intents(id) ON DELETE RESTRICT,
    template_digest BYTEA NOT NULL CHECK(octet_length(template_digest)=32),
    rendered_message_digest BYTEA NOT NULL CHECK(octet_length(rendered_message_digest)=32),
    ciphertext BYTEA NOT NULL CHECK(octet_length(ciphertext)>=28),
    cipher_version SMALLINT NOT NULL CHECK(cipher_version=1),
    envelope_fingerprint TEXT NOT NULL CHECK(envelope_fingerprint ~ '^sha256:[0-9a-f]{64}$'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);

CREATE FUNCTION channel_welcome_message_snapshot_guard() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
  RAISE EXCEPTION 'channel welcome message snapshots are immutable';
END; $$;
CREATE TRIGGER channel_welcome_message_snapshots_guard BEFORE UPDATE OR DELETE ON channel_welcome_message_snapshots FOR EACH ROW EXECUTE FUNCTION channel_welcome_message_snapshot_guard();
CREATE TRIGGER channel_welcome_message_snapshots_no_truncate BEFORE TRUNCATE ON channel_welcome_message_snapshots FOR EACH STATEMENT EXECUTE FUNCTION channel_welcome_message_snapshot_guard();
