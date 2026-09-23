-- Owner: internal/order.
--
-- Product validates this opaque buyer-completion snapshot before Payment
-- carries it into Order. Existing rows intentionally get an empty object:
-- their checkout did not record a target and must retain the historical
-- Product guidance fallback.

ALTER TABLE order_checkout_snapshots
    ADD COLUMN post_purchase_action JSONB NOT NULL DEFAULT '{}'::jsonb
    CHECK (octet_length(post_purchase_action::text) <= 8192);
