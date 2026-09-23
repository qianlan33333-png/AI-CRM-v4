-- Owner: access. Extends the existing auditable local-admin control plane for
-- the frozen AdminOps compatibility route; it does not touch OneID, customers,
-- Provider credentials, or external effects.
ALTER TABLE admin_access_audit
    DROP CONSTRAINT admin_access_audit_action_check;

ALTER TABLE admin_access_audit
    ADD CONSTRAINT ck_admin_access_audit_action CHECK (
        action IN ('bootstrap', 'create', 'disable', 'bind_wecom_userid',
                   'change_roles', 'reset_password', 'set_login_enabled')
    );

-- The frozen AdminOps page sends a browser Idempotency-Key. Keep the receipt
-- in Access-owned storage so request replay, payload drift, audit, and any
-- session fencing commit or roll back together.
CREATE TABLE admin_access_login_compat_receipts (
    actor_admin_user_id BIGINT NOT NULL REFERENCES admin_users(id),
    idempotency_key TEXT NOT NULL,
    payload_digest BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (actor_admin_user_id, idempotency_key),
    CONSTRAINT ck_admin_access_login_compat_receipt_key
        CHECK (length(idempotency_key) BETWEEN 1 AND 200),
    CONSTRAINT ck_admin_access_login_compat_receipt_digest
        CHECK (octet_length(payload_digest) = 32)
);
