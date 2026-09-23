-- Owner: internal/order. Freezes the Product version used by native checkout.
ALTER TABLE order_items
  ADD COLUMN product_version BIGINT
  CHECK (product_version IS NULL OR product_version > 0);
