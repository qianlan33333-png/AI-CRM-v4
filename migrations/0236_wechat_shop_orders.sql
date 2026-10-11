-- Owner: Order. External money uses source totals, native item math is unchanged.
ALTER TABLE orders DROP CONSTRAINT orders_record_origin_check;
ALTER TABLE orders ADD CONSTRAINT orders_record_origin_check CHECK(record_origin IN ('native','history','external'));
ALTER TABLE orders DROP CONSTRAINT orders_origin_effect_shape;
ALTER TABLE orders ADD CONSTRAINT orders_origin_effect_shape CHECK(
 (record_origin='native' AND effect_eligible AND source_row_digest IS NULL AND payer_customer_id IS NOT NULL AND beneficiary_customer_id IS NOT NULL)
 OR (record_origin='history' AND NOT effect_eligible AND octet_length(source_row_digest)=32)
 OR (record_origin='external' AND provider='wechat_shop' AND NOT effect_eligible AND octet_length(source_row_digest)=32));
ALTER TABLE orders DROP CONSTRAINT orders_amount_minor_check;
ALTER TABLE orders ADD CONSTRAINT orders_amount_minor_check CHECK(amount_minor>0 OR(record_origin='external' AND amount_minor=0));
ALTER TABLE order_items ADD COLUMN amount_basis text NOT NULL DEFAULT 'native' CHECK(amount_basis IN ('native','wechat_shop'));
ALTER TABLE order_items DROP CONSTRAINT order_items_amount_shape;
ALTER TABLE order_items DROP CONSTRAINT order_items_unit_amount_minor_check;
ALTER TABLE order_items DROP CONSTRAINT order_items_line_amount_minor_check;
ALTER TABLE order_items ADD CONSTRAINT order_items_amount_shape CHECK(
 (amount_basis='native' AND unit_amount_minor>0 AND line_amount_minor>0 AND line_amount_minor=unit_amount_minor*quantity)
 OR(amount_basis='wechat_shop' AND unit_amount_minor>=0 AND line_amount_minor>=0));
CREATE TABLE order_wechat_shop_facts (
 order_id bigint PRIMARY KEY REFERENCES orders(id),
 app_id text NOT NULL,
 paid_snapshot jsonb NOT NULL,
 paid_at timestamptz NOT NULL,
 activation_eligible boolean NOT NULL,
 dispatch_complete boolean NOT NULL DEFAULT false,
 contact_ciphertext bytea NOT NULL,
 contact_key_version smallint NOT NULL,
 source_updated_at timestamptz NOT NULL,
 immutable_digest bytea NOT NULL CHECK(octet_length(immutable_digest)=32)
);
CREATE TABLE order_wechat_shop_lines (
 order_id bigint NOT NULL REFERENCES orders(id),
 line_no integer NOT NULL,
 source_product_id text NOT NULL,
 source_sku_id text NOT NULL,
 PRIMARY KEY(order_id,line_no)
);
-- Owner: wechatshop. Activation cutoff and successful read progress, no queue state.
CREATE TABLE wechat_shop_order_progress (
 app_id text PRIMARY KEY,
 activated_at timestamptz NOT NULL,
 completed_until timestamptz NOT NULL
);
CREATE TABLE wechat_shop_callback_receipts (
 app_id text NOT NULL,
 event_digest bytea NOT NULL CHECK(octet_length(event_digest)=32),
 body_digest bytea NOT NULL CHECK(octet_length(body_digest)=32),
 received_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(app_id,event_digest)
);
