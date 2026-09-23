-- Owner: payment. Local UI abandonment is NOT a Provider terminal outcome.
CREATE TABLE payment_checkout_abandonments (
 payment_id bigint PRIMARY KEY REFERENCES payments(id),
 actor_scope text NOT NULL CHECK (length(actor_scope) BETWEEN 1 AND 200),
 evidence_digest bytea NOT NULL CHECK (octet_length(evidence_digest) = 32),
 evidence_kind text NOT NULL CHECK (evidence_kind = 'human_no_debit_invalid_legacy_order'),
 created_at timestamptz NOT NULL
);
