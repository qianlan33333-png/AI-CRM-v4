-- Owner: internal/groupops.
-- Provider-verified presentation projection for the already-authorized
-- operation-member set. It neither grants Access roles nor creates customer
-- or identity records.
CREATE TABLE group_ops_operation_member_directory (
    staff_id BIGINT PRIMARY KEY REFERENCES admin_users(id) ON DELETE RESTRICT,
    sender_userid TEXT NOT NULL UNIQUE,
    display_name TEXT NOT NULL,
    name_source TEXT NOT NULL CHECK (name_source IN ('wecom_profile','local_fallback')),
    profile_read_state TEXT NOT NULL CHECK (profile_read_state IN ('ready','unavailable')),
    profile_read_error_code TEXT NOT NULL DEFAULT '',
    source_digest TEXT NOT NULL,
    active BOOLEAN NOT NULL DEFAULT TRUE,
    profile_refreshed_at TIMESTAMPTZ,
    refreshed_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT group_ops_operation_member_directory_sender CHECK (
        sender_userid ~ '^[A-Za-z0-9._:-]{1,128}$'
    ),
    CONSTRAINT group_ops_operation_member_directory_display_name CHECK (
        btrim(display_name) = display_name
        AND char_length(display_name) BETWEEN 1 AND 160
        AND position(E'\n' IN display_name) = 0
        AND position(E'\r' IN display_name) = 0
    ),
    CONSTRAINT group_ops_operation_member_directory_error_code CHECK (
        profile_read_error_code = ''
        OR profile_read_error_code ~ '^[a-z0-9_]{1,64}$'
    ),
    CONSTRAINT group_ops_operation_member_directory_digest CHECK (
        source_digest ~ '^sha256:[0-9a-f]{64}$'
    ),
    CONSTRAINT group_ops_operation_member_directory_profile_time CHECK (
        (name_source = 'wecom_profile' AND profile_refreshed_at IS NOT NULL)
        OR (name_source = 'local_fallback')
    )
);
CREATE INDEX group_ops_operation_member_directory_active_idx
    ON group_ops_operation_member_directory(active, refreshed_at DESC, staff_id);
