-- A reviewed receiver recovery is a Payment-owned command receipt.  It is
-- deliberately separate from the original receiver acceptance receipt: the
-- old final-failed external effect remains immutable while a new effect
-- intent is accepted only after Payment verifies every prior attempt was
-- local and unexecuted.
ALTER TABLE payment_operation_receipts
  DROP CONSTRAINT payment_operation_receipts_operation_check;

ALTER TABLE payment_operation_receipts
  ADD CONSTRAINT payment_operation_receipts_operation_check
  CHECK(operation IN ('create','refund','callback','reconcile','history_import','receiver_recovery'));

ALTER TABLE payment_operation_receipts
  DROP CONSTRAINT payment_operation_receipts_result_kind_check;

ALTER TABLE payment_operation_receipts
  ADD CONSTRAINT payment_operation_receipts_result_kind_check
  CHECK(result_kind IN ('payment','refund','callback','reconcile','history','receiver'));
