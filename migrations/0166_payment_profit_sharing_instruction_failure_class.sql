-- Owner: Payment. Only an exact, verified CLOSED split detail may add one of
-- these finite diagnostic classes. Historical and unknown provider outcomes
-- remain empty; no Provider body, message, account or transaction identifier
-- is reconstructed into this column.
ALTER TABLE payment_profit_sharing_instructions
  ADD COLUMN failure_class TEXT NOT NULL DEFAULT ''
  CHECK (failure_class IN (
    '',
    'receiver_account_abnormal',
    'receiver_relation_removed',
    'receiver_high_risk',
    'receiver_real_name_unverified',
    'merchant_permission_revoked',
    'receiver_receipt_limit',
    'payer_account_abnormal',
    'invalid_split_request'
  ));
