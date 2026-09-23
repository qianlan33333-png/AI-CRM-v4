-- Owner: internal/survey.
-- Preserve the safe execution facts supplied by the External Effects completion
-- sink. NULL means this pre-0074/legacy receipt did not record the fact; it
-- is not a negative provider result. Forward-only and no historical rewrite.
ALTER TABLE survey_external_operation_receipts
    ADD COLUMN provider_call_attempted BOOLEAN,
    ADD COLUMN provider_real_call_executed BOOLEAN,
    ADD COLUMN provider_result_received BOOLEAN,
    ADD COLUMN provider_attempt_number INTEGER CHECK (provider_attempt_number IS NULL OR provider_attempt_number > 0);
