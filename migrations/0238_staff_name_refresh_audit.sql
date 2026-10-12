-- Owner: Access. Allow the existing employee-name refresh command to commit
-- its audit in the same transaction as the names. Preserve all employee,
-- permission, session and audit records. Forward-only; old binaries remain
-- compatible with this expanded action constraint.
ALTER TABLE admin_access_audit DROP CONSTRAINT ck_admin_access_audit_action;
ALTER TABLE admin_access_audit ADD CONSTRAINT ck_admin_access_audit_action CHECK (
    action IN ('bootstrap', 'create', 'disable', 'bind_wecom_userid', 'change_roles', 'reset_password', 'set_login_enabled',
               'provision_admin', 'provision_viewer', 'enable_login', 'disable_login', 'set_role', 'transfer_super_admin',
               'refresh_staff_name')
);
