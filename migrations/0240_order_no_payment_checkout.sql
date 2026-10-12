-- Owner: Order. A local free checkout has no Payment or refund record.
ALTER TABLE orders DROP CONSTRAINT orders_provider_check;
ALTER TABLE orders ADD CONSTRAINT orders_provider_check CHECK(provider IN ('wechat_pay','wechat_shop','alipay','external_history','no_payment'));
ALTER TABLE orders DROP CONSTRAINT orders_amount_minor_check;
ALTER TABLE orders ADD CONSTRAINT orders_amount_minor_check CHECK(
 amount_minor>0 OR (record_origin='external' AND amount_minor=0)
 OR (record_origin='native' AND provider='no_payment' AND amount_minor=0));
ALTER TABLE orders ADD CONSTRAINT orders_no_payment_shape CHECK(provider<>'no_payment' OR
 (record_origin='native' AND effect_eligible AND amount_minor=0 AND currency='CNY'
 AND provider_transaction_no='' AND refunded_minor=0 AND status NOT IN ('refunded','partially_refunded','payment_failed')));
ALTER TABLE order_items DROP CONSTRAINT order_items_amount_shape;
ALTER TABLE order_items ADD CONSTRAINT order_items_amount_shape CHECK(
 (amount_basis='native' AND unit_amount_minor>=0 AND line_amount_minor>=0 AND line_amount_minor=unit_amount_minor*quantity)
 OR (amount_basis='wechat_shop' AND unit_amount_minor>=0 AND line_amount_minor>=0));
CREATE FUNCTION order_items_guard_free_native() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.amount_basis='native' AND (NEW.unit_amount_minor=0 OR NEW.line_amount_minor=0)
 AND NOT EXISTS(SELECT 1 FROM orders WHERE id=NEW.order_id AND provider='no_payment' AND record_origin='native' AND amount_minor=0)
 THEN RAISE EXCEPTION 'zero native item requires local no-payment order'; END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER order_items_guard_free_native BEFORE INSERT OR UPDATE ON order_items
 FOR EACH ROW EXECUTE FUNCTION order_items_guard_free_native();
ALTER TABLE order_checkout_snapshots DROP CONSTRAINT order_checkout_snapshots_gross_amount_minor_check;
ALTER TABLE order_checkout_snapshots DROP CONSTRAINT order_checkout_snapshots_check;
ALTER TABLE order_checkout_snapshots ADD CONSTRAINT order_checkout_snapshots_amount_shape CHECK(
 (gross_amount_minor>0 AND payable_amount_minor>0 AND payable_amount_minor=gross_amount_minor-discount_amount_minor)
 OR (gross_amount_minor=0 AND payable_amount_minor=0 AND discount_amount_minor=0
 AND product_type='standard_product' AND NOT coupon_applied AND NOT profit_sharing_required));
CREATE FUNCTION order_checkout_guard_free() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.gross_amount_minor=0 AND NOT EXISTS(SELECT 1 FROM orders WHERE id=NEW.order_id AND provider='no_payment' AND record_origin='native' AND amount_minor=0)
 THEN RAISE EXCEPTION 'zero checkout requires local no-payment order'; END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER order_checkout_guard_free BEFORE INSERT ON order_checkout_snapshots
 FOR EACH ROW EXECUTE FUNCTION order_checkout_guard_free();
