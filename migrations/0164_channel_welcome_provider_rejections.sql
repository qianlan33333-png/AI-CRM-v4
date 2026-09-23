-- Owner: internal/channel. Forward-only. Persist only a strictly parsed numeric
-- Provider rejection code for a completed welcome-write response. It never
-- stores a request, response body, errmsg, credential, welcome code, or
-- external identity. External Effects/River remains the only execution kernel.
ALTER TABLE channel_welcome_intents
    ADD COLUMN provider_error_code BIGINT;

ALTER TABLE channel_welcome_intents
    DROP CONSTRAINT channel_welcome_intents_result_reason_check;
ALTER TABLE channel_welcome_intents
    ADD CONSTRAINT channel_welcome_intents_result_reason_check CHECK(result_reason IS NULL OR result_reason IN (
        'state_unmatched','state_ambiguous','channel_unavailable','welcome_not_configured','welcome_material_unavailable',
        'deadline_missing','deadline_expired','grant_expired','material_invalid','provider_unavailable','outcome_unknown',
        'sent','final_failed','customer_name_unavailable','welcome_template_invalid','welcome_message_too_long','frozen_message_unavailable',
        'provider_rejected'
    ));

-- Existing intents predate the optional code and remain valid with NULL. A
-- nonzero code is allowed only for an explicit, final Provider rejection.
ALTER TABLE channel_welcome_intents
    ADD CONSTRAINT channel_welcome_intents_provider_error_code_check CHECK (
        provider_error_code IS NULL OR provider_error_code <> 0
    );
ALTER TABLE channel_welcome_intents
    ADD CONSTRAINT channel_welcome_intents_provider_rejection_code_check CHECK (
        ((result_reason = 'provider_rejected') IS TRUE) = (provider_error_code IS NOT NULL)
    );
