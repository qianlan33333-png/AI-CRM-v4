-- Owner: access. Separates a provider-projected business staff record from
-- explicit CRM login permission while preserving admin_users.id as every
-- existing Staff FK. No customer identity is created or changed.

ALTER TABLE admin_users
    ADD COLUMN login_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    ADD COLUMN legacy_login_reactivation_pending BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN access_granted_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP;

-- Before this migration is_active was the only access switch. Preserve that
-- observable login state while recording old accounts as explicitly granted.
-- A historically disabled account may be re-enabled once through the governed
-- login command; that command restores employee availability and clears the
-- bridge flag, rather than leaving a UI-enabled account rejected by Auth.
UPDATE admin_users
   SET login_enabled = is_active,
       legacy_login_reactivation_pending = NOT is_active,
       access_granted_at = created_at;

-- 0151's deferred users trigger observes the backfill. Flush its old
-- assertion before altering this table again; otherwise PostgreSQL rejects
-- the following ALTER TABLE for pending trigger events. The old assertion is
-- still valid at this point because 0151 already required one active super.
SET CONSTRAINTS access_super_admin_control_after_users IMMEDIATE;

ALTER TABLE admin_users
    ALTER COLUMN access_granted_at DROP NOT NULL,
    ALTER COLUMN access_granted_at DROP DEFAULT;

ALTER TABLE admin_users
    ADD CONSTRAINT ck_admin_users_login_requires_access_grant
    CHECK (access_granted_at IS NOT NULL OR login_enabled = FALSE);

-- 0151 owns the deferred control assertion. Replace it after the new columns
-- exist so direct SQL and normal Access UOWs both reject an ungranted or
-- login-disabled super administrator at commit.
CREATE OR REPLACE FUNCTION access_assert_super_admin_control() RETURNS TRIGGER AS $$
DECLARE
    control access_super_admin_control%ROWTYPE;
    role_count BIGINT;
    role_value TEXT;
    is_enabled BOOLEAN;
BEGIN
    SELECT * INTO control FROM access_super_admin_control WHERE singleton = TRUE;
    IF NOT FOUND THEN
        IF EXISTS (SELECT 1 FROM admin_users) THEN
            RAISE EXCEPTION 'access governance control missing for initialized access data';
        END IF;
        RETURN NULL;
    END IF;
    SELECT COUNT(*), MIN(role_code), BOOL_AND(users.is_active AND users.login_enabled AND users.access_granted_at IS NOT NULL)
      INTO role_count, role_value, is_enabled
      FROM admin_user_roles roles
      JOIN admin_users users ON users.id = roles.admin_user_id
     WHERE roles.admin_user_id = control.admin_user_id;
    IF role_count <> 1 OR role_value <> 'super_admin' OR is_enabled IS DISTINCT FROM TRUE THEN
        RAISE EXCEPTION 'access governance control owner must be the one active granted login-enabled super administrator';
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
