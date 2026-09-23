-- Owner: internal/order.
--
-- A native payment order keeps the checkout pricing and service-period facts
-- it was created with. These facts are deliberately not backfilled for
-- historical orders: their original checkout evidence is unavailable and
-- must remain unknown rather than guessed from current Product or Coupon data.

CREATE TABLE order_checkout_snapshots (
    order_id BIGINT PRIMARY KEY REFERENCES orders(id) ON DELETE RESTRICT,
    product_type TEXT NOT NULL CHECK (product_type IN ('standard_product','service_period')),
    product_id BIGINT NOT NULL CHECK (product_id>0),
    product_code TEXT NOT NULL CHECK (char_length(product_code) BETWEEN 1 AND 200),
    product_name TEXT NOT NULL CHECK (char_length(product_name) BETWEEN 1 AND 500),
    product_version BIGINT NOT NULL CHECK (product_version>0),
    service_period_duration_days INTEGER NOT NULL DEFAULT 0 CHECK (service_period_duration_days>=0),
    gross_amount_minor BIGINT NOT NULL CHECK (gross_amount_minor>0),
    discount_amount_minor BIGINT NOT NULL CHECK (discount_amount_minor>=0),
    payable_amount_minor BIGINT NOT NULL CHECK (payable_amount_minor>0 AND payable_amount_minor=gross_amount_minor-discount_amount_minor),
    currency TEXT NOT NULL CHECK (currency='CNY'),
    coupon_applied BOOLEAN NOT NULL,
    coupon_reservation_ref TEXT NOT NULL DEFAULT '' CHECK (char_length(coupon_reservation_ref)<=240),
    coupon_claim_id BIGINT,
    coupon_id BIGINT,
    coupon_rule_version BIGINT,
    reserved_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    CHECK (
        (product_type='standard_product' AND service_period_duration_days=0)
        OR (product_type='service_period' AND service_period_duration_days>0)
    ),
    CHECK (
        (coupon_applied=FALSE
            AND coupon_reservation_ref=''
            AND coupon_claim_id IS NULL
            AND coupon_id IS NULL
            AND coupon_rule_version IS NULL
            AND discount_amount_minor=0
            AND payable_amount_minor=gross_amount_minor)
        OR (coupon_applied=TRUE
            AND char_length(coupon_reservation_ref)>0
            AND coupon_claim_id IS NOT NULL AND coupon_claim_id>0
            AND coupon_id IS NOT NULL AND coupon_id>0
            AND coupon_rule_version IS NOT NULL AND coupon_rule_version>0
            AND discount_amount_minor>0 AND discount_amount_minor<gross_amount_minor)
    )
);

CREATE INDEX order_checkout_snapshots_product_idx
    ON order_checkout_snapshots(product_type, product_id, order_id);

-- A checkout snapshot is historical evidence, not an editable projection.
-- Reject even a syntactically valid rewrite so later catalog/coupon changes
-- cannot alter the amount, period, or reservation recorded for this order.
CREATE FUNCTION order_checkout_snapshots_reject_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'order checkout snapshots are immutable';
END;
$$;

CREATE TRIGGER order_checkout_snapshots_reject_mutation
BEFORE UPDATE OR DELETE ON order_checkout_snapshots
FOR EACH ROW EXECUTE FUNCTION order_checkout_snapshots_reject_mutation();
