-- Owner: Product. Forward-only; existing bindings retain their empty value.
-- The legacy product-panel test button shows the synthetic protocol delivery
-- identifier immediately after Product accepts the operation. This is an
-- Outbound-generated correlation value, never Provider delivery proof.
-- Existing Product test bindings and receipt snapshots predate that value, so
-- they deliberately remain readable with an empty value.
ALTER TABLE product_external_push_tests
    ADD COLUMN IF NOT EXISTS delivery_id TEXT NOT NULL DEFAULT ''
    CHECK (delivery_id = '' OR delivery_id ~ '^commerce_test_[0-9a-f]{32}$');
