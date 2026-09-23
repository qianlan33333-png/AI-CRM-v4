-- Payment Owner: nullable identity only for inert historical money facts.
ALTER TABLE payments ADD COLUMN historical BOOLEAN NOT NULL DEFAULT false,
 ADD COLUMN source_status TEXT NOT NULL DEFAULT '' CHECK(length(source_status)<=80),
 ADD COLUMN history_reason TEXT NOT NULL DEFAULT '' CHECK(length(history_reason)<=120);
-- Identify prior imports through Payment-owned receipts only.
UPDATE payments p SET historical=true WHERE EXISTS(SELECT 1 FROM payment_operation_receipts r WHERE r.operation='history_import' AND r.result_kind='payment' AND r.result_id=p.id);
ALTER TABLE payments ALTER COLUMN payer_identity_id DROP NOT NULL,
 ALTER COLUMN payer_customer_id DROP NOT NULL, ALTER COLUMN beneficiary_customer_id DROP NOT NULL;
ALTER TABLE payments ADD CONSTRAINT payments_identity_origin_shape CHECK (
 (NOT historical AND payer_identity_id IS NOT NULL AND payer_customer_id IS NOT NULL AND beneficiary_customer_id IS NOT NULL AND source_status='' AND history_reason='') OR
 (historical AND external_effect_id IS NULL AND status IN ('paid','failed','cancelled') AND ((payer_identity_id IS NULL AND payer_customer_id IS NULL AND beneficiary_customer_id IS NULL) OR (payer_identity_id IS NOT NULL AND payer_customer_id IS NOT NULL)))
);
