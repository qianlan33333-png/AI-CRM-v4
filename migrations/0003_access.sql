-- Owner: access
-- Retention: users and audits are durable; sessions and rate-limit rows are
-- operational security state. Forward-only: rollback disables the feature and
-- never drops employee or audit records.
-- Local employee authentication is isolated from customer identities and WeCom OAuth.

CREATE TABLE admin_users (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    username TEXT NOT NULL,
    password_hash TEXT NOT NULL,
    display_name TEXT NOT NULL,
    wecom_userid TEXT,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    session_version BIGINT NOT NULL DEFAULT 1,
    last_login_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT ck_admin_users_username CHECK (
        username = lower(btrim(username)) AND length(username) BETWEEN 3 AND 120
    ),
    CONSTRAINT ck_admin_users_password_hash CHECK (password_hash LIKE '$argon2id$%'),
    CONSTRAINT ck_admin_users_display_name CHECK (length(btrim(display_name)) BETWEEN 1 AND 160),
    CONSTRAINT ck_admin_users_wecom_userid CHECK (
        wecom_userid IS NULL OR (wecom_userid = btrim(wecom_userid) AND length(wecom_userid) BETWEEN 1 AND 128)
    ),
    CONSTRAINT ck_admin_users_session_version CHECK (session_version > 0),
    CONSTRAINT ux_admin_users_username UNIQUE (username)
);

CREATE UNIQUE INDEX ux_admin_users_wecom_userid
    ON admin_users (wecom_userid) WHERE wecom_userid IS NOT NULL;

CREATE TABLE admin_user_roles (
    admin_user_id BIGINT NOT NULL REFERENCES admin_users(id) ON DELETE CASCADE,
    role_code TEXT NOT NULL CHECK (role_code IN ('super_admin', 'admin', 'viewer')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (admin_user_id, role_code)
);

CREATE TABLE admin_sessions (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    token_digest BYTEA NOT NULL,
    csrf_token_digest BYTEA NOT NULL,
    admin_user_id BIGINT NOT NULL REFERENCES admin_users(id),
    session_version BIGINT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    revoked_reason TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT ux_admin_sessions_token_digest UNIQUE (token_digest),
    CONSTRAINT ck_admin_sessions_token_sha256 CHECK (octet_length(token_digest) = 32),
    CONSTRAINT ck_admin_sessions_csrf_sha256 CHECK (octet_length(csrf_token_digest) = 32),
    CONSTRAINT ck_admin_sessions_version CHECK (session_version > 0),
    CONSTRAINT ck_admin_sessions_expiry CHECK (expires_at > created_at)
);

CREATE INDEX ix_admin_sessions_user_active
    ON admin_sessions (admin_user_id, expires_at) WHERE revoked_at IS NULL;

CREATE TABLE admin_login_audit (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    admin_user_id BIGINT REFERENCES admin_users(id),
    identifier_digest BYTEA NOT NULL,
    remote_digest BYTEA NOT NULL,
    outcome TEXT NOT NULL CHECK (outcome IN ('succeeded', 'invalid_credentials', 'disabled', 'rate_limited')),
    reason TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT ck_admin_login_audit_identifier_sha256 CHECK (octet_length(identifier_digest) = 32),
    CONSTRAINT ck_admin_login_audit_remote_sha256 CHECK (octet_length(remote_digest) = 32)
);

CREATE INDEX ix_admin_login_audit_user_created
    ON admin_login_audit (admin_user_id, created_at DESC, id DESC);

CREATE TABLE admin_access_audit (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    actor_admin_user_id BIGINT NOT NULL REFERENCES admin_users(id),
    target_admin_user_id BIGINT NOT NULL REFERENCES admin_users(id),
    action TEXT NOT NULL CHECK (action IN ('bootstrap', 'create', 'disable', 'bind_wecom_userid', 'change_roles', 'reset_password')),
    details JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX ix_admin_access_audit_target_created
    ON admin_access_audit (target_admin_user_id, created_at DESC, id DESC);

-- This is authoritative state, not an in-process cache. Rows are locked during
-- login decisions so concurrent failures cannot bypass the threshold.
CREATE TABLE admin_login_rate_limits (
    key_digest BYTEA PRIMARY KEY,
    window_started_at TIMESTAMPTZ NOT NULL,
    failure_count INTEGER NOT NULL DEFAULT 0,
    blocked_until TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT ck_admin_login_rate_limit_digest CHECK (octet_length(key_digest) = 32),
    CONSTRAINT ck_admin_login_rate_limit_count CHECK (failure_count >= 0)
);

CREATE INDEX ix_admin_login_rate_limits_cleanup
    ON admin_login_rate_limits (updated_at);
