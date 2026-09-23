-- Owner: internal/payment. A payment's mutable updated_at can move when a
-- later refund is reconciled, so qualification history needs a separately
-- sourced immutable paid-confirmation fact. Existing rows lack that proof and
-- deliberately remain NULL: Distribution fails closed rather than treating an
-- import/reconciliation timestamp as a provider payment confirmation.
ALTER TABLE payments ADD COLUMN paid_confirmed_at TIMESTAMPTZ;

CREATE INDEX payments_paid_confirmed_idx ON payments(paid_confirmed_at DESC,id DESC)
WHERE status='paid' AND paid_confirmed_at IS NOT NULL;
