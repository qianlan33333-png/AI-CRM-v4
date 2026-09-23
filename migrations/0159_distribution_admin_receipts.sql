-- Owner: internal/distribution. Administrative state changes use the same
-- immutable idempotency receipt boundary as public Distribution commands.
ALTER TABLE distribution_operation_receipts
    DROP CONSTRAINT distribution_operation_receipts_operation_check;

ALTER TABLE distribution_operation_receipts
    ADD CONSTRAINT distribution_operation_receipts_operation_check CHECK (operation IN (
        'register','policy','credential','attribution','paid_event','settlement',
        'exception_reconcile','recovery','merchant_liability',
        'distributor_disable','distributor_enable'
    ));
