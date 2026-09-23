-- Owner: aiassistant. New v3 plans only; no donor history is imported.
-- Forward-only: rollback is a follow-up migration because review history,
-- idempotency receipts and effect bindings must never be silently discarded.
-- Customer, Staff, Media, Outbound and External Effects references are opaque
-- values coordinated through stable ports, never cross-domain table writes.

CREATE TABLE ai_assistant_plans (
    id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL,
    source_kind TEXT NOT NULL,
    source_digest BYTEA NOT NULL,
    state TEXT NOT NULL DEFAULT 'pending_review',
    version BIGINT NOT NULL DEFAULT 1,
    target_count INTEGER NOT NULL,
    pending_count INTEGER NOT NULL,
    approved_count INTEGER NOT NULL DEFAULT 0,
    rejected_count INTEGER NOT NULL DEFAULT 0,
    ineligible_count INTEGER NOT NULL DEFAULT 0,
    needs_attention_count INTEGER NOT NULL DEFAULT 0,
    created_by BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    rejected_reason TEXT,
    CONSTRAINT ck_ai_assistant_plan_name CHECK (length(btrim(name)) BETWEEN 1 AND 200),
    CONSTRAINT ck_ai_assistant_source_kind CHECK (length(btrim(source_kind)) BETWEEN 1 AND 80),
    CONSTRAINT ck_ai_assistant_source_digest CHECK (octet_length(source_digest) = 32),
    CONSTRAINT ck_ai_assistant_plan_state CHECK (state IN ('pending_review','partially_approved','approved','rejected','dispatching','needs_attention','completed_with_failures','completed')),
    CONSTRAINT ck_ai_assistant_plan_version CHECK (version > 0),
    CONSTRAINT ck_ai_assistant_plan_counts CHECK (
        target_count BETWEEN 1 AND 5000 AND pending_count >= 0 AND approved_count >= 0 AND
        rejected_count >= 0 AND ineligible_count >= 0 AND needs_attention_count >= 0 AND
        pending_count + approved_count + rejected_count + ineligible_count = target_count AND
        needs_attention_count <= target_count
    )
);
CREATE INDEX ai_assistant_plans_list_idx ON ai_assistant_plans(updated_at DESC, id DESC);
CREATE INDEX ai_assistant_plans_state_idx ON ai_assistant_plans(state, updated_at DESC, id DESC);

CREATE TABLE ai_assistant_plan_recipients (
    id BIGSERIAL PRIMARY KEY,
    plan_id BIGINT NOT NULL REFERENCES ai_assistant_plans(id) ON DELETE RESTRICT,
    customer_id BIGINT NOT NULL,
    staff_id BIGINT NOT NULL,
    review_state TEXT NOT NULL DEFAULT 'pending_review',
    execution_state TEXT NOT NULL DEFAULT 'not_accepted',
    version BIGINT NOT NULL DEFAULT 1,
    current_content_version_id BIGINT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT uq_ai_assistant_plan_customer UNIQUE (plan_id, customer_id),
    CONSTRAINT ck_ai_assistant_customer_ref CHECK (customer_id > 0),
    CONSTRAINT ck_ai_assistant_staff_ref CHECK (staff_id > 0),
    CONSTRAINT ck_ai_assistant_recipient_review CHECK (review_state IN ('pending_review','approved','rejected','ineligible')),
    CONSTRAINT ck_ai_assistant_recipient_execution CHECK (execution_state IN ('not_accepted','accepted','queued','attempted','provider_accepted','retryable_failed','outcome_unknown','reconciled','final_failed','delivery_proven')),
    CONSTRAINT ck_ai_assistant_recipient_version CHECK (version > 0)
);
CREATE INDEX ai_assistant_recipients_page_idx ON ai_assistant_plan_recipients(plan_id, id);
CREATE INDEX ai_assistant_recipients_review_idx ON ai_assistant_plan_recipients(plan_id, review_state, id);

CREATE TABLE ai_assistant_content_versions (
    id BIGSERIAL PRIMARY KEY,
    recipient_id BIGINT NOT NULL REFERENCES ai_assistant_plan_recipients(id) ON DELETE RESTRICT,
    version BIGINT NOT NULL,
    content_digest BYTEA NOT NULL,
    content_payload JSONB NOT NULL,
    created_by BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT uq_ai_assistant_content_owner UNIQUE (id, recipient_id),
    CONSTRAINT uq_ai_assistant_content_version UNIQUE (recipient_id, version),
    CONSTRAINT uq_ai_assistant_content_digest UNIQUE (recipient_id, content_digest),
    CONSTRAINT ck_ai_assistant_content_version CHECK (version > 0),
    CONSTRAINT ck_ai_assistant_content_digest CHECK (octet_length(content_digest) = 32),
    CONSTRAINT ck_ai_assistant_content_payload CHECK (jsonb_typeof(content_payload) = 'array' AND jsonb_array_length(content_payload) BETWEEN 1 AND 20)
);
ALTER TABLE ai_assistant_plan_recipients
    ADD CONSTRAINT fk_ai_assistant_current_content
    FOREIGN KEY (current_content_version_id, id) REFERENCES ai_assistant_content_versions(id, recipient_id) ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE ai_assistant_review_decisions (
    id BIGSERIAL PRIMARY KEY,
    plan_id BIGINT NOT NULL REFERENCES ai_assistant_plans(id) ON DELETE RESTRICT,
    recipient_id BIGINT REFERENCES ai_assistant_plan_recipients(id) ON DELETE RESTRICT,
    decision TEXT NOT NULL,
    reason TEXT NOT NULL DEFAULT '',
    actor_id BIGINT NOT NULL,
    aggregate_version BIGINT NOT NULL,
    idempotency_digest BYTEA NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT ck_ai_assistant_decision CHECK (decision IN ('approved','rejected')),
    CONSTRAINT ck_ai_assistant_decision_reason CHECK (length(reason) <= 500),
    CONSTRAINT ck_ai_assistant_decision_version CHECK (aggregate_version > 0),
    CONSTRAINT ck_ai_assistant_decision_digest CHECK (octet_length(idempotency_digest) = 32)
);

CREATE TABLE ai_assistant_effect_bindings (
    recipient_id BIGINT PRIMARY KEY REFERENCES ai_assistant_plan_recipients(id) ON DELETE RESTRICT,
    outbound_intent_id BIGINT NOT NULL UNIQUE,
    external_effect_id TEXT NOT NULL UNIQUE,
    payload_digest BYTEA NOT NULL,
    state TEXT NOT NULL DEFAULT 'accepted',
    generation BIGINT NOT NULL DEFAULT 0,
    fence BIGINT NOT NULL DEFAULT 0,
    attempt_count INTEGER NOT NULL DEFAULT 0,
    provider_accepted BOOLEAN NOT NULL DEFAULT false,
    delivery_proven BOOLEAN NOT NULL DEFAULT false,
    provider_receipt_digest BYTEA,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT ck_ai_assistant_effect_id CHECK (external_effect_id ~ '^eer_[1-9][0-9]*$'),
    CONSTRAINT ck_ai_assistant_effect_payload_digest CHECK (octet_length(payload_digest) = 32),
    CONSTRAINT ck_ai_assistant_effect_state CHECK (state IN ('accepted','queued','attempted','provider_accepted','retryable_failed','outcome_unknown','reconciled','final_failed','delivery_proven')),
    CONSTRAINT ck_ai_assistant_effect_generation CHECK (generation >= 0 AND fence >= 0 AND attempt_count >= 0),
    CONSTRAINT ck_ai_assistant_provider_receipt CHECK (provider_receipt_digest IS NULL OR octet_length(provider_receipt_digest) = 32),
    CONSTRAINT ck_ai_assistant_delivery_proof CHECK (NOT delivery_proven OR provider_accepted)
);

CREATE TABLE ai_assistant_operation_receipts (
    id BIGSERIAL PRIMARY KEY,
    operation TEXT NOT NULL,
    actor_scope TEXT NOT NULL,
    key_digest BYTEA NOT NULL,
    payload_digest BYTEA NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('reserved','completed')),
    result_snapshot JSONB,
    created_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    CONSTRAINT uq_ai_assistant_operation_receipt UNIQUE (operation, actor_scope, key_digest),
    CONSTRAINT ck_ai_assistant_receipt_digests CHECK (octet_length(key_digest) = 32 AND octet_length(payload_digest) = 32)
);

CREATE TABLE ai_assistant_integration_nonces (
    key_digest BYTEA NOT NULL,
    nonce_digest BYTEA NOT NULL,
    idempotency_digest BYTEA NOT NULL,
    payload_digest BYTEA NOT NULL,
    request_timestamp TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (key_digest, nonce_digest),
    CONSTRAINT ck_ai_assistant_integration_nonce_digests CHECK (octet_length(key_digest) = 32 AND octet_length(nonce_digest) = 32 AND octet_length(idempotency_digest) = 32 AND octet_length(payload_digest) = 32),
    CONSTRAINT ck_ai_assistant_integration_nonce_window CHECK (expires_at > request_timestamp)
);
CREATE INDEX ai_assistant_integration_nonces_expiry_idx ON ai_assistant_integration_nonces(expires_at);

CREATE TABLE ai_assistant_audit_events (
    id BIGSERIAL PRIMARY KEY,
    plan_id BIGINT NOT NULL REFERENCES ai_assistant_plans(id) ON DELETE RESTRICT,
    recipient_id BIGINT REFERENCES ai_assistant_plan_recipients(id) ON DELETE RESTRICT,
    operation TEXT NOT NULL,
    actor_id BIGINT NOT NULL,
    payload_digest BYTEA NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT ck_ai_assistant_audit_digest CHECK (octet_length(payload_digest) = 32)
);

CREATE TABLE ai_assistant_outbox (
    id BIGSERIAL PRIMARY KEY,
    event_type TEXT NOT NULL,
    plan_id BIGINT NOT NULL REFERENCES ai_assistant_plans(id) ON DELETE RESTRICT,
    payload JSONB NOT NULL,
    idempotency_digest BYTEA NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT ck_ai_assistant_outbox_digest CHECK (octet_length(idempotency_digest) = 32)
);

CREATE OR REPLACE FUNCTION ai_assistant_reject_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'AI Assistant history is append-only'; END $$;

CREATE TRIGGER ai_assistant_content_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON ai_assistant_content_versions FOR EACH STATEMENT EXECUTE FUNCTION ai_assistant_reject_mutation();
CREATE TRIGGER ai_assistant_decisions_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON ai_assistant_review_decisions FOR EACH STATEMENT EXECUTE FUNCTION ai_assistant_reject_mutation();
CREATE TRIGGER ai_assistant_audit_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON ai_assistant_audit_events FOR EACH STATEMENT EXECUTE FUNCTION ai_assistant_reject_mutation();
CREATE TRIGGER ai_assistant_outbox_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON ai_assistant_outbox FOR EACH STATEMENT EXECUTE FUNCTION ai_assistant_reject_mutation();
