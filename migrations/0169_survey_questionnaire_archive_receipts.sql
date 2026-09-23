-- Owner: internal/survey. Forward-only.
-- Archive retains definitions, submissions, receipts and audit facts. This only
-- permits the owner lifecycle's idempotent archive receipt; it neither deletes
-- history nor emits an external effect.

ALTER TABLE survey_operation_receipts
    DROP CONSTRAINT IF EXISTS survey_operation_receipts_operation_check;

ALTER TABLE survey_operation_receipts
    ADD CONSTRAINT survey_operation_receipts_operation_check
    CHECK (operation IN (
        'definition_create',
        'definition_update',
        'definition_duplicate',
        'definition_publish',
        'definition_disable',
        'definition_enable',
        'definition_delete',
        'definition_archive'
    ));
