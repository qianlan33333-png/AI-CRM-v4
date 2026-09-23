-- Owner: internal/externaleffects. All values are opaque digests or local IDs.
CREATE TABLE external_effects (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    owner TEXT NOT NULL CHECK (owner IN ('outbound')),
    kind TEXT NOT NULL CHECK (kind IN ('outbound_message','outbound_media','wecom_tag_catalog','group_message')),
    source_ref_digest TEXT NOT NULL CHECK (source_ref_digest ~ '^sha256:[0-9a-f]{64}$'),
    target_ref_digest TEXT NOT NULL CHECK (target_ref_digest ~ '^sha256:[0-9a-f]{64}$'),
    payload_digest TEXT NOT NULL CHECK (payload_digest ~ '^sha256:[0-9a-f]{64}$'),
    policy_version_hash TEXT NOT NULL CHECK (policy_version_hash ~ '^sha256:[0-9a-f]{64}$'),
    envelope_fingerprint TEXT NOT NULL UNIQUE CHECK (envelope_fingerprint ~ '^sha256:[0-9a-f]{64}$'),
    state TEXT NOT NULL CHECK (state IN ('accepted','queued','attempted','executed','outcome_unknown','reconciled','retryable_failed','final_failed','cancelled')),
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    generation BIGINT NOT NULL DEFAULT 1 CHECK (generation > 0),
    lease_fence BIGINT NOT NULL DEFAULT 0 CHECK (lease_fence >= 0),
    lease_expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT external_effects_lease_consistency CHECK (lease_fence = 0 OR lease_expires_at IS NOT NULL),
    CONSTRAINT external_effects_unknown_requires_attempt CHECK (state <> 'outcome_unknown' OR attempt_count > 0)
);
CREATE INDEX external_effects_runtime_idx ON external_effects (state, updated_at DESC);

CREATE TABLE external_effect_generations (
    effect_id BIGINT NOT NULL REFERENCES external_effects(id),
    generation BIGINT NOT NULL CHECK (generation > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (effect_id, generation)
);

CREATE TABLE external_effect_operation_receipts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    operation TEXT NOT NULL CHECK (operation IN ('accept','queue','retry','cancel','complete','reconcile')),
    effect_id BIGINT NOT NULL REFERENCES external_effects(id),
    receipt_key_digest TEXT NOT NULL CHECK (receipt_key_digest ~ '^sha256:[0-9a-f]{64}$'),
    command_digest TEXT NOT NULL CHECK (command_digest ~ '^sha256:[0-9a-f]{64}$'),
    actor_admin_user_id BIGINT CHECK (actor_admin_user_id IS NULL OR actor_admin_user_id > 0),
    state TEXT NOT NULL,
    completed_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE(operation, effect_id, receipt_key_digest),
    UNIQUE(operation, effect_id, command_digest),
    CONSTRAINT external_effect_manual_control_requires_actor CHECK (operation NOT IN ('retry','cancel','reconcile') OR actor_admin_user_id IS NOT NULL)
);

CREATE TABLE external_effect_attempts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    effect_id BIGINT NOT NULL REFERENCES external_effects(id),
    number INTEGER NOT NULL CHECK (number > 0),
    generation BIGINT NOT NULL,
    fence BIGINT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('attempted','executed','outcome_unknown','retryable_failed','final_failed','reconciled')),
    receipt_digest TEXT CHECK (receipt_digest IS NULL OR receipt_digest ~ '^sha256:[0-9a-f]{64}$'),
    evidence_digest TEXT CHECK (evidence_digest IS NULL OR evidence_digest ~ '^sha256:[0-9a-f]{64}$'),
    call_attempted BOOLEAN NOT NULL DEFAULT false,
    real_external_call_executed BOOLEAN NOT NULL DEFAULT false,
    started_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    completed_at TIMESTAMPTZ,
    UNIQUE(effect_id, number, generation),
    CONSTRAINT external_effect_attempts_real_call_requires_attempt CHECK (NOT real_external_call_executed OR call_attempted)
);
CREATE UNIQUE INDEX external_effect_accept_receipt_key_unique ON external_effect_operation_receipts (receipt_key_digest) WHERE operation = 'accept';

CREATE TABLE external_effect_jobs (
    effect_id BIGINT NOT NULL,
    generation BIGINT NOT NULL,
    river_job_id BIGINT NOT NULL UNIQUE,
    queue TEXT NOT NULL CHECK (queue = 'outbound'),
    args_digest TEXT NOT NULL CHECK (args_digest ~ '^sha256:[0-9a-f]{64}$'),
    scheduled_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (effect_id, generation),
    FOREIGN KEY (effect_id, generation) REFERENCES external_effect_generations(effect_id, generation)
);

-- Effects, attempts and receipts are audit facts. Operators create a new
-- control receipt; they never delete or rewrite historical execution facts.
CREATE OR REPLACE FUNCTION public.external_effects_reject_delete() RETURNS trigger LANGUAGE plpgsql SET search_path = pg_catalog AS $$
BEGIN
  RAISE EXCEPTION 'external effect facts are immutable';
END;
$$;
CREATE TRIGGER external_effects_no_delete BEFORE DELETE ON external_effects FOR EACH ROW EXECUTE FUNCTION public.external_effects_reject_delete();
CREATE TRIGGER external_effect_generations_no_delete BEFORE DELETE ON external_effect_generations FOR EACH ROW EXECUTE FUNCTION public.external_effects_reject_delete();
CREATE TRIGGER external_effect_attempts_no_delete BEFORE DELETE ON external_effect_attempts FOR EACH ROW EXECUTE FUNCTION public.external_effects_reject_delete();
CREATE TRIGGER external_effect_receipts_no_delete BEFORE DELETE ON external_effect_operation_receipts FOR EACH ROW EXECUTE FUNCTION public.external_effects_reject_delete();
CREATE TRIGGER external_effect_receipts_no_update BEFORE UPDATE ON external_effect_operation_receipts FOR EACH ROW EXECUTE FUNCTION public.external_effects_reject_delete();
