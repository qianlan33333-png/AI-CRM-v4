-- Owner: payment. A reviewed, expired, never-delivered checkout may be replaced
-- explicitly by its user. This is NOT a Provider terminal state or effect retry.
CREATE TABLE payment_checkout_restart_permissions (
 payment_id bigint PRIMARY KEY REFERENCES payment_checkout_abandonments(payment_id),
 actor_scope text NOT NULL CHECK(length(actor_scope) BETWEEN 1 AND 200),
 evidence_digest bytea NOT NULL CHECK(octet_length(evidence_digest)=32),
 attempt_completed_at timestamptz NOT NULL,
 approved_at timestamptz NOT NULL,
 CHECK(approved_at >= attempt_completed_at + interval '130 minutes')
);
