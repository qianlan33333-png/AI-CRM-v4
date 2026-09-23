-- Owner: internal/order. Freeze the server-issued Referral activity context
-- alongside the immutable checkout snapshot. The opaque value is hashed by
-- Order before persistence; Referral resolves it through its stable Port.
ALTER TABLE order_checkout_snapshots
  ADD COLUMN referral_activity_context_digest BYTEA NOT NULL DEFAULT decode(repeat('00', 32), 'hex'),
  ADD COLUMN promotion_context_digest BYTEA NOT NULL DEFAULT decode(repeat('00', 32), 'hex');

ALTER TABLE order_checkout_snapshots
  ADD CONSTRAINT order_checkout_snapshots_referral_activity_context_digest_check
  CHECK (octet_length(referral_activity_context_digest)=32);

ALTER TABLE order_checkout_snapshots
  ADD CONSTRAINT order_checkout_snapshots_promotion_context_digest_check
  CHECK (octet_length(promotion_context_digest)=32);
