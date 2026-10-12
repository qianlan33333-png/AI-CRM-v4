-- Order owns the immutable purchase snapshot; no dependency on a current catalog row.
ALTER TABLE order_items ADD COLUMN variant_snapshot jsonb;
ALTER TABLE order_items ADD CONSTRAINT order_item_variant_object CHECK(variant_snapshot IS NULL OR jsonb_typeof(variant_snapshot)='object');
