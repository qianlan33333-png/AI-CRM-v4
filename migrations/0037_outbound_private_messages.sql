-- Owner: outbound. Provider-ready private-message intents contain canonical
-- references and immutable digests only; channel identifiers are resolved
-- after commit and are never persisted here.
CREATE TABLE outbound_private_message_intents (
    id BIGSERIAL PRIMARY KEY,
    source_reference TEXT NOT NULL,
    customer_id BIGINT NOT NULL,
    staff_id BIGINT NOT NULL,
    payload_reference TEXT NOT NULL,
    source_digest BYTEA NOT NULL,
    target_digest BYTEA NOT NULL,
    payload_digest BYTEA NOT NULL,
    policy_hash BYTEA NOT NULL,
    receipt_key BYTEA NOT NULL,
    external_effect_id TEXT NOT NULL,
    state TEXT NOT NULL DEFAULT 'queued',
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT uq_outbound_private_message_source UNIQUE(source_reference),
    CONSTRAINT uq_outbound_private_message_receipt UNIQUE(receipt_key),
    CONSTRAINT uq_outbound_private_message_effect UNIQUE(external_effect_id),
    CONSTRAINT uq_outbound_private_message_envelope UNIQUE(source_digest,target_digest,payload_digest,policy_hash),
    CONSTRAINT ck_outbound_private_message_refs CHECK (
        length(btrim(source_reference)) BETWEEN 1 AND 200 AND
        length(btrim(payload_reference)) BETWEEN 1 AND 200 AND
        customer_id > 0 AND staff_id > 0
    ),
    CONSTRAINT ck_outbound_private_message_digests CHECK (
        octet_length(source_digest)=32 AND octet_length(target_digest)=32 AND
        octet_length(payload_digest)=32 AND octet_length(policy_hash)=32 AND
        octet_length(receipt_key)=32
    ),
    CONSTRAINT ck_outbound_private_message_effect CHECK (external_effect_id ~ '^eer_[1-9][0-9]*$'),
    CONSTRAINT ck_outbound_private_message_state CHECK (state IN ('queued','attempted','provider_accepted','retryable_failed','outcome_unknown','reconciled','final_failed','delivery_proven'))
);
CREATE INDEX outbound_private_message_effect_idx ON outbound_private_message_intents(external_effect_id);
ALTER TABLE ai_assistant_effect_bindings
    ADD CONSTRAINT fk_ai_assistant_outbound_intent
    FOREIGN KEY (outbound_intent_id) REFERENCES outbound_private_message_intents(id) ON DELETE RESTRICT;
