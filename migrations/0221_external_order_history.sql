-- Owner: Order. External source facts never represent a Payment or refund effect.
ALTER TABLE orders DROP CONSTRAINT orders_provider_check;
ALTER TABLE orders ADD CONSTRAINT orders_provider_check CHECK (provider IN ('wechat_pay','wechat_shop','alipay','external_history'));
ALTER TABLE orders ADD CONSTRAINT orders_external_history_shape CHECK (provider<>'external_history' OR (record_origin='history' AND NOT effect_eligible AND payer_customer_id IS NOT NULL AND beneficiary_customer_id IS NOT NULL AND provider_transaction_no=''));
CREATE TABLE order_external_history_facts (
 order_id BIGINT PRIMARY KEY REFERENCES orders(id) ON DELETE RESTRICT,
 source_paid_at TIMESTAMPTZ,
 imported_at TIMESTAMPTZ NOT NULL,
 source_status TEXT NOT NULL,
 source_after_sales TEXT NOT NULL DEFAULT ''
);
CREATE TRIGGER order_external_history_immutable BEFORE UPDATE OR DELETE OR TRUNCATE ON order_external_history_facts FOR EACH STATEMENT EXECUTE FUNCTION order_immutable_facts_reject_mutation();
