-- Owner: access. Enforces the mutually exclusive administrator role model.
-- This migration is deliberately fail-closed for initialized databases: the
-- release-window reconciliation command must settle any historical ambiguity
-- before this file is applied.
DO $$
DECLARE
    user_count BIGINT;
    role_anomaly_count BIGINT;
    active_super_count BIGINT;
    inactive_super_count BIGINT;
BEGIN
    SELECT COUNT(*) INTO user_count FROM admin_users;
    SELECT COUNT(*) INTO role_anomaly_count FROM (
        SELECT users.id
          FROM admin_users users
          LEFT JOIN admin_user_roles roles ON roles.admin_user_id = users.id
         GROUP BY users.id
        HAVING COUNT(roles.admin_user_id) <> 1
    ) anomalies;
    SELECT COUNT(*) INTO active_super_count
      FROM admin_user_roles roles
      JOIN admin_users users ON users.id = roles.admin_user_id
     WHERE roles.role_code = 'super_admin' AND users.is_active;
    SELECT COUNT(*) INTO inactive_super_count
      FROM admin_user_roles roles
      JOIN admin_users users ON users.id = roles.admin_user_id
     WHERE roles.role_code = 'super_admin' AND NOT users.is_active;

    IF user_count > 0 AND (role_anomaly_count <> 0 OR active_super_count <> 1 OR inactive_super_count <> 0) THEN
        RAISE EXCEPTION 'access governance precondition failed: reconcile roles and exactly one active super administrator before migration 0151';
    END IF;
END $$;

ALTER TABLE admin_user_roles
    ADD CONSTRAINT ux_admin_user_roles_one_role UNIQUE (admin_user_id);

-- This index is immediate rather than deferred so direct SQL cannot create two
-- super administrators through concurrent transactions. Transfer first lowers
-- the old owner, then raises the target in the same Access UOW.
CREATE UNIQUE INDEX ux_admin_user_roles_one_super_admin
    ON admin_user_roles (role_code) WHERE role_code = 'super_admin';

CREATE TABLE access_super_admin_control (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    admin_user_id BIGINT NOT NULL UNIQUE REFERENCES admin_users(id) ON DELETE RESTRICT,
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO access_super_admin_control (singleton, admin_user_id, version, updated_at)
SELECT TRUE, roles.admin_user_id, 1, CURRENT_TIMESTAMP
  FROM admin_user_roles roles
  JOIN admin_users users ON users.id = roles.admin_user_id
 WHERE roles.role_code = 'super_admin' AND users.is_active
   AND EXISTS (SELECT 1 FROM admin_users)
ON CONFLICT (singleton) DO NOTHING;

CREATE TABLE admin_access_governance_receipts (
    actor_admin_user_id BIGINT NOT NULL REFERENCES admin_users(id),
    idempotency_key TEXT NOT NULL,
    action TEXT NOT NULL,
    target_admin_user_id BIGINT NOT NULL REFERENCES admin_users(id),
    payload_digest BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (actor_admin_user_id, idempotency_key),
    CONSTRAINT ck_admin_access_governance_receipt_key CHECK (length(idempotency_key) BETWEEN 1 AND 200),
    CONSTRAINT ck_admin_access_governance_receipt_digest CHECK (octet_length(payload_digest) = 32)
);

ALTER TABLE admin_access_audit DROP CONSTRAINT ck_admin_access_audit_action;
ALTER TABLE admin_access_audit ADD CONSTRAINT ck_admin_access_audit_action CHECK (
    action IN ('bootstrap', 'create', 'disable', 'bind_wecom_userid', 'change_roles', 'reset_password', 'set_login_enabled',
               'provision_admin', 'provision_viewer', 'enable_login', 'disable_login', 'set_role', 'transfer_super_admin')
);

CREATE OR REPLACE FUNCTION access_assert_super_admin_control() RETURNS TRIGGER AS $$
DECLARE
    control access_super_admin_control%ROWTYPE;
    role_count BIGINT;
    role_value TEXT;
    is_enabled BOOLEAN;
BEGIN
    SELECT * INTO control FROM access_super_admin_control WHERE singleton = TRUE;
    IF NOT FOUND THEN
        -- The only legal no-control state is a genuinely empty pre-bootstrap
        -- database. Bootstrap creates account, role and control in one UOW.
        IF EXISTS (SELECT 1 FROM admin_users) THEN
            RAISE EXCEPTION 'access governance control missing for initialized access data';
        END IF;
        RETURN NULL;
    END IF;
    SELECT COUNT(*), MIN(role_code), BOOL_AND(users.is_active)
      INTO role_count, role_value, is_enabled
      FROM admin_user_roles roles
      JOIN admin_users users ON users.id = roles.admin_user_id
     WHERE roles.admin_user_id = control.admin_user_id;
    IF role_count <> 1 OR role_value <> 'super_admin' OR is_enabled IS DISTINCT FROM TRUE THEN
        RAISE EXCEPTION 'access governance control owner must be the one active super administrator';
    END IF;
    IF EXISTS (
        SELECT users.id
          FROM admin_users users
          LEFT JOIN admin_user_roles roles ON roles.admin_user_id = users.id
         GROUP BY users.id
        HAVING COUNT(roles.admin_user_id) <> 1
    ) THEN
        RAISE EXCEPTION 'access governance requires exactly one role for every administrator';
    END IF;
    IF (SELECT COUNT(*) FROM admin_user_roles WHERE role_code = 'super_admin') <> 1 THEN
        RAISE EXCEPTION 'access governance requires exactly one super administrator';
    END IF;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

CREATE CONSTRAINT TRIGGER access_super_admin_control_after_roles
AFTER INSERT OR UPDATE OR DELETE ON admin_user_roles
DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION access_assert_super_admin_control();
CREATE CONSTRAINT TRIGGER access_super_admin_control_after_users
AFTER INSERT OR UPDATE OR DELETE ON admin_users
DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION access_assert_super_admin_control();
CREATE CONSTRAINT TRIGGER access_super_admin_control_after_control
AFTER INSERT OR UPDATE OR DELETE ON access_super_admin_control
DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION access_assert_super_admin_control();
