-- Owners: internal/wecom owns callback receipts and follow relationships;
-- internal/channel owns acquisition state bindings and entrant receipts.
-- Retention: all receipt rows are durable audit facts. This migration is
-- forward-only; disabling the feature must never delete callback evidence.

-- Existing OAuth-era rows have no callback cursor. New callback writes use the
-- non-null event cursor and version CAS added here. CallbackID is the durable,
-- non-PII Inbox idempotency key; it is never treated as event chronology.
ALTER TABLE wecom_follow_relationships
    DROP CONSTRAINT wecom_follow_relationships_employee_nonempty,
    ADD CONSTRAINT wecom_follow_relationships_employee_nonempty CHECK (
        employee_id = btrim(employee_id) AND char_length(employee_id) BETWEEN 1 AND 1024
    ),
    ADD COLUMN version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    ADD COLUMN last_event_at TIMESTAMPTZ,
    ADD COLUMN last_callback_id TEXT,
    ADD COLUMN last_event_digest BYTEA,
    ADD CONSTRAINT wecom_follow_relationships_event_cursor_shape CHECK (
        (last_event_at IS NULL AND last_callback_id IS NULL AND last_event_digest IS NULL)
        OR (
            last_event_at IS NOT NULL
            AND last_callback_id IS NOT NULL
            AND last_event_digest IS NOT NULL
            AND btrim(last_callback_id) = last_callback_id
            AND char_length(last_callback_id) BETWEEN 1 AND 512
            AND octet_length(last_event_digest) = 32
        )
    );

CREATE INDEX wecom_follow_relationships_customer_active_idx
    ON wecom_follow_relationships (customer_id, corp_id, employee_id)
    WHERE active;

-- This identity-scoped cursor is written before OneID resolution. It retains
-- an unknown contact's delete without creating a ghost customer, so an older
-- delayed add cannot reactivate the employee/contact relationship. Provider
-- timestamps are second-granularity; same-second activation/deactivation uses
-- an explicit deletion-wins rule rather than ordering content hashes.
CREATE TABLE wecom_external_contact_event_cursors (
    corp_id TEXT NOT NULL,
    external_identity_digest BYTEA NOT NULL CHECK (octet_length(external_identity_digest) = 32),
    employee_id TEXT NOT NULL,
    active BOOLEAN NOT NULL,
    last_event_at TIMESTAMPTZ NOT NULL,
    last_callback_id TEXT NOT NULL,
    last_event_digest BYTEA NOT NULL CHECK (octet_length(last_event_digest) = 32),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (corp_id, external_identity_digest, employee_id),
    CONSTRAINT wecom_external_contact_event_cursors_corp CHECK (
        corp_id = btrim(corp_id) AND char_length(corp_id) BETWEEN 1 AND 256
    ),
    CONSTRAINT wecom_external_contact_event_cursors_employee CHECK (
        employee_id = btrim(employee_id) AND char_length(employee_id) BETWEEN 1 AND 1024
    ),
    CONSTRAINT wecom_external_contact_event_cursors_callback CHECK (
        last_callback_id = btrim(last_callback_id)
        AND char_length(last_callback_id) BETWEEN 1 AND 512
    )
);

-- One immutable processing receipt is allowed per Inbox attempt. Retry
-- requests are separate immutable operation receipts and never mutate the
-- processing fact they target. The platform webhook Store remains the sole
-- owner of Inbox lease/status CAS.
CREATE TABLE wecom_callback_receipts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    inbox_id BIGINT NOT NULL REFERENCES webhook_inbox(id) ON DELETE RESTRICT,
    receipt_kind TEXT NOT NULL CHECK (receipt_kind IN ('processing', 'retry_requested')),
    target_receipt_id BIGINT,
    attempt_number INTEGER NOT NULL CHECK (attempt_number > 0),
    command_digest BYTEA NOT NULL CHECK (octet_length(command_digest) = 32),
    event_type TEXT NOT NULL DEFAULT '',
    change_type TEXT NOT NULL DEFAULT '',
    prior_inbox_status TEXT NOT NULL CHECK (
        prior_inbox_status IN ('received', 'processing', 'processed', 'retryable', 'failed')
    ),
    resulting_inbox_status TEXT NOT NULL CHECK (
        resulting_inbox_status IN ('received', 'processing', 'processed', 'retryable', 'failed')
    ),
    result_codes TEXT[] NOT NULL DEFAULT '{}'::TEXT[] CHECK (
        cardinality(result_codes) <= 4
        AND result_codes <@ ARRAY[
            'customer_created', 'customer_resolved', 'relationship_activated',
            'relationship_deactivated', 'channel_attributed', 'channel_unmatched',
            'channel_ambiguous', 'identity_conflict', 'ignored', 'failed_terminal'
        ]::TEXT[]
    ),
    error_code TEXT NOT NULL DEFAULT '' CHECK (
        error_code = '' OR error_code ~ '^[a-z0-9][a-z0-9_.:-]{0,119}$'
    ),
    actor_admin_user_id BIGINT REFERENCES admin_users(id) ON DELETE RESTRICT,
    reason TEXT NOT NULL DEFAULT '' CHECK (
        char_length(reason) <= 500 AND btrim(reason) = reason
        AND reason !~ '[[:cntrl:]]'
    ),
    operation_key_digest BYTEA CHECK (
        operation_key_digest IS NULL OR octet_length(operation_key_digest) = 32
    ),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT wecom_callback_receipts_id_inbox_unique UNIQUE (id, inbox_id),
    CONSTRAINT wecom_callback_receipts_target_fk FOREIGN KEY (target_receipt_id, inbox_id)
        REFERENCES wecom_callback_receipts(id, inbox_id) ON DELETE RESTRICT,
    CONSTRAINT wecom_callback_receipts_shape CHECK (
        (
            receipt_kind = 'processing'
            AND target_receipt_id IS NULL
            AND event_type ~ '^[a-z0-9_]{1,128}$'
            AND change_type ~ '^[a-z0-9_]{1,128}$'
            AND prior_inbox_status = 'processing'
            AND actor_admin_user_id IS NULL
            AND reason = ''
            AND operation_key_digest IS NULL
            AND (
                (resulting_inbox_status = 'processed' AND cardinality(result_codes) BETWEEN 1 AND 4 AND error_code = '')
                OR (resulting_inbox_status = 'retryable' AND cardinality(result_codes) = 0 AND error_code <> '')
                OR (resulting_inbox_status = 'failed' AND result_codes = ARRAY['failed_terminal']::TEXT[] AND error_code <> '')
            )
        )
        OR
        (
            receipt_kind = 'retry_requested'
            AND target_receipt_id IS NOT NULL
            AND event_type = ''
            AND change_type = ''
            AND prior_inbox_status IN ('retryable', 'failed')
            AND resulting_inbox_status = 'retryable'
            AND cardinality(result_codes) = 0
            AND error_code = ''
            AND actor_admin_user_id IS NOT NULL
            AND reason <> ''
            AND operation_key_digest IS NOT NULL
        )
    )
);

CREATE UNIQUE INDEX wecom_callback_receipts_processing_once_idx
    ON wecom_callback_receipts (inbox_id, attempt_number)
    WHERE receipt_kind = 'processing';

CREATE UNIQUE INDEX wecom_callback_receipts_retry_key_idx
    ON wecom_callback_receipts (actor_admin_user_id, operation_key_digest)
    WHERE receipt_kind = 'retry_requested';

CREATE INDEX wecom_callback_receipts_timeline_idx
    ON wecom_callback_receipts (id DESC);

-- State never appears in this table. Callers provide a keyed 32-byte digest
-- and its key version. There is deliberately no uniqueness constraint on
-- (corp_id, digest_key_version, state_digest): overlapping active bindings for
-- different assets are a configuration ambiguity that Resolve must return as
-- cardinality > 1 rather than silently selecting one.
CREATE TABLE channel_acquisition_state_bindings (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    corp_id TEXT NOT NULL CHECK (
        corp_id = btrim(corp_id) AND char_length(corp_id) BETWEEN 1 AND 256
    ),
    digest_key_version SMALLINT NOT NULL CHECK (digest_key_version > 0),
    state_digest BYTEA NOT NULL CHECK (octet_length(state_digest) = 32),
    channel_id BIGINT NOT NULL CHECK (channel_id > 0),
    asset_kind TEXT NOT NULL CHECK (
        asset_kind IN ('contact_way_qrcode', 'customer_acquisition_link')
    ),
    asset_version BIGINT NOT NULL CHECK (asset_version > 0),
    binding_digest BYTEA NOT NULL CHECK (octet_length(binding_digest) = 32),
    active_from TIMESTAMPTZ NOT NULL,
    active_until TIMESTAMPTZ,
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT channel_acquisition_state_bindings_asset_unique
        UNIQUE (channel_id, asset_kind, asset_version),
    CONSTRAINT channel_acquisition_state_bindings_period CHECK (
        active_until IS NULL OR active_until > active_from
    )
);

CREATE INDEX channel_acquisition_state_bindings_resolve_idx
    ON channel_acquisition_state_bindings (
        corp_id, digest_key_version, state_digest, active_from, active_until, id
    );

-- The original entrant decision never changes. A manual correction appends a
-- separate reconciliation receipt below, preserving both before and after.
-- No State, external_userid, employee UserID, callback XML or Provider secret
-- is retained here.
CREATE TABLE channel_acquisition_entrant_receipts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    callback_id TEXT NOT NULL UNIQUE CHECK (
        callback_id = btrim(callback_id) AND char_length(callback_id) BETWEEN 1 AND 512
    ),
    inbox_id BIGINT NOT NULL UNIQUE REFERENCES webhook_inbox(id) ON DELETE RESTRICT,
    corp_id TEXT NOT NULL CHECK (
        corp_id = btrim(corp_id) AND char_length(corp_id) BETWEEN 1 AND 256
    ),
    input_digest BYTEA NOT NULL CHECK (octet_length(input_digest) = 32),
    command_digest BYTEA NOT NULL CHECK (octet_length(command_digest) = 32),
    change_type TEXT NOT NULL DEFAULT '' CHECK (
        change_type IN ('', 'add_external_contact', 'add_half_external_contact')
    ),
    status TEXT NOT NULL CHECK (
        status IN ('channel_attributed', 'channel_unmatched', 'channel_ambiguous', 'identity_conflict', 'ignored')
    ),
    binding_id BIGINT REFERENCES channel_acquisition_state_bindings(id) ON DELETE RESTRICT,
    customer_id BIGINT REFERENCES customers(id) ON DELETE RESTRICT,
    occurred_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT channel_acquisition_entrant_receipts_shape CHECK (
        (status = 'channel_attributed' AND binding_id IS NOT NULL AND customer_id IS NOT NULL)
        OR (status IN ('channel_unmatched', 'channel_ambiguous', 'ignored') AND binding_id IS NULL AND customer_id IS NOT NULL)
        OR (status = 'identity_conflict' AND customer_id IS NULL)
    )
);

CREATE INDEX channel_acquisition_entrant_receipts_unassigned_idx
    ON channel_acquisition_entrant_receipts (id DESC)
    WHERE status IN ('channel_unmatched', 'channel_ambiguous', 'identity_conflict');

CREATE TABLE channel_acquisition_entrant_reconciliation_receipts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entrant_receipt_id BIGINT NOT NULL UNIQUE
        REFERENCES channel_acquisition_entrant_receipts(id) ON DELETE RESTRICT,
    actor_admin_user_id BIGINT NOT NULL REFERENCES admin_users(id) ON DELETE RESTRICT,
    operation_key_digest BYTEA NOT NULL CHECK (octet_length(operation_key_digest) = 32),
    command_digest BYTEA NOT NULL CHECK (octet_length(command_digest) = 32),
    prior_status TEXT NOT NULL CHECK (
        prior_status IN ('channel_unmatched', 'channel_ambiguous', 'identity_conflict')
    ),
    resulting_status TEXT NOT NULL DEFAULT 'reconciled' CHECK (resulting_status = 'reconciled'),
    binding_id BIGINT NOT NULL REFERENCES channel_acquisition_state_bindings(id) ON DELETE RESTRICT,
    customer_id BIGINT NOT NULL REFERENCES customers(id) ON DELETE RESTRICT,
    reason TEXT NOT NULL CHECK (
        char_length(reason) BETWEEN 1 AND 500 AND btrim(reason) = reason
        AND reason !~ '[[:cntrl:]]'
    ),
    reconciled_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT channel_acquisition_entrant_reconciliation_key_unique
        UNIQUE (actor_admin_user_id, operation_key_digest)
);

CREATE OR REPLACE FUNCTION public.wecom_callback_facts_reject_mutation()
RETURNS trigger LANGUAGE plpgsql SET search_path = pg_catalog AS $$
BEGIN
    RAISE EXCEPTION 'callback and acquisition receipt facts are immutable';
END;
$$;

CREATE TRIGGER wecom_callback_receipts_no_update_or_delete
    BEFORE UPDATE OR DELETE ON wecom_callback_receipts
    FOR EACH ROW EXECUTE FUNCTION public.wecom_callback_facts_reject_mutation();

CREATE TRIGGER wecom_callback_receipts_no_truncate
    BEFORE TRUNCATE ON wecom_callback_receipts
    FOR EACH STATEMENT EXECUTE FUNCTION public.wecom_callback_facts_reject_mutation();

CREATE TRIGGER channel_acquisition_entrant_receipts_no_update_or_delete
    BEFORE UPDATE OR DELETE ON channel_acquisition_entrant_receipts
    FOR EACH ROW EXECUTE FUNCTION public.wecom_callback_facts_reject_mutation();

CREATE TRIGGER channel_acquisition_entrant_receipts_no_truncate
    BEFORE TRUNCATE ON channel_acquisition_entrant_receipts
    FOR EACH STATEMENT EXECUTE FUNCTION public.wecom_callback_facts_reject_mutation();

CREATE TRIGGER channel_acquisition_entrant_reconciliations_no_update_or_delete
    BEFORE UPDATE OR DELETE ON channel_acquisition_entrant_reconciliation_receipts
    FOR EACH ROW EXECUTE FUNCTION public.wecom_callback_facts_reject_mutation();

CREATE TRIGGER channel_acquisition_entrant_reconciliations_no_truncate
    BEFORE TRUNCATE ON channel_acquisition_entrant_reconciliation_receipts
    FOR EACH STATEMENT EXECUTE FUNCTION public.wecom_callback_facts_reject_mutation();
