-- Owner: internal/distribution. A provider-confirmed CLOSED split leaves the
-- merchant's commission obligation intact unless a separate refund or
-- qualification fact has already cancelled it. This explicit exception kind
-- records that state without storing a Provider response or identity value.

ALTER TABLE distribution_exceptions
  DROP CONSTRAINT distribution_exceptions_kind_check;

ALTER TABLE distribution_exceptions
  ADD CONSTRAINT distribution_exceptions_kind_check
  CHECK (kind IN (
    'settlement_unknown',
    'settlement_not_paid',
    'settlement_deadline',
    'settlement_deadline_imminent',
    'receiver_unavailable',
    'qualification_revoked_after_paid',
    'buyer_refund_after_paid',
    'unfreeze_final_failed',
    'merchant_liability',
    'recovery'
  ));
