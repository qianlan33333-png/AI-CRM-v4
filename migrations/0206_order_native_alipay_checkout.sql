-- Owner: internal/order. Alipay web checkout creates native orders through
-- the same transaction and idempotency contract as WeChat checkout.
ALTER TABLE orders DROP CONSTRAINT orders_origin_effect_shape;
ALTER TABLE orders ADD CONSTRAINT orders_origin_effect_shape CHECK (
  (record_origin='native' AND effect_eligible=TRUE AND source_row_digest IS NULL
    AND payer_customer_id IS NOT NULL AND beneficiary_customer_id IS NOT NULL)
  OR (record_origin='history' AND effect_eligible=FALSE AND octet_length(source_row_digest)=32)
);
