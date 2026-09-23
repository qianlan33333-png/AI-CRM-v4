-- Owner: Payment. Retain historical final failures as an empty (unknown)
-- class: no provider response is reconstructed or inferred during migration.
-- Forward-only; Distribution consumes only the safe Payment projection.
ALTER TABLE payment_profit_sharing_receivers
  ADD COLUMN failure_class TEXT NOT NULL DEFAULT ''
  CHECK (failure_class IN ('', 'provider_permission_denied'));
