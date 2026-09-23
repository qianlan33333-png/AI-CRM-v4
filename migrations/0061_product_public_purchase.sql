-- Migration 0061. Owners: internal/payment (OAuth/session/channel) and internal/order (contact snapshot).
-- Existing payment facts were created by the Mini Program-only implementation.
ALTER TABLE payment_sessions
  ADD COLUMN payment_channel TEXT NOT NULL DEFAULT 'mini_program'
  CHECK (payment_channel IN ('mini_program','h5_official_account'));

ALTER TABLE payments
  ADD COLUMN payment_channel TEXT NOT NULL DEFAULT 'mini_program'
  CHECK (payment_channel IN ('mini_program','h5_official_account'));

CREATE TABLE payment_h5_oauth_states (
  state_digest BYTEA PRIMARY KEY CHECK (octet_length(state_digest)=32),
  return_path TEXT NOT NULL CHECK (return_path ~ '^/pay/[1-9][0-9]*$'),
  expires_at TIMESTAMPTZ NOT NULL,
  consumed_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL,
  CHECK (expires_at>created_at),
  CHECK (consumed_at IS NULL OR consumed_at>=created_at)
);

CREATE TABLE order_contact_snapshots (
  order_id BIGINT PRIMARY KEY REFERENCES orders(id) ON DELETE RESTRICT,
  phone_ciphertext BYTEA NOT NULL CHECK (octet_length(phone_ciphertext)>=28),
  key_version SMALLINT NOT NULL CHECK (key_version=1),
  created_at TIMESTAMPTZ NOT NULL
);

CREATE TRIGGER order_contact_snapshots_no_mutation
BEFORE UPDATE OR DELETE OR TRUNCATE ON order_contact_snapshots
FOR EACH STATEMENT EXECUTE FUNCTION order_immutable_facts_reject_mutation();
