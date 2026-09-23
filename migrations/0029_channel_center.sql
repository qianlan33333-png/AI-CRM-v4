-- Owner: internal/channel.
-- Retention: channel definitions, immutable configuration versions, staff
-- assignment snapshots and completed operation receipts are durable business
-- facts. Forward-only: disable the module instead of dropping these tables.
-- Channel commands append platform audit_events and outbox_events in the same
-- PostgreSQL Unit of Work; this migration does not create a second event bus.

CREATE TABLE channels (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    code TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('active', 'inactive', 'archived')),
    current_config_version BIGINT NOT NULL DEFAULT 1 CHECK (current_config_version > 0),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    archived_at TIMESTAMPTZ,
    CONSTRAINT channels_code_shape CHECK (
        code = btrim(code)
        AND char_length(code) BETWEEN 1 AND 200
        AND code ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{0,199}$'
    ),
    CONSTRAINT channels_archived_shape CHECK (
        (status = 'archived' AND archived_at IS NOT NULL)
        OR (status <> 'archived' AND archived_at IS NULL)
    ),
    CONSTRAINT channels_time_order CHECK (updated_at >= created_at),
    CONSTRAINT channels_id_config_unique UNIQUE (id, current_config_version)
);

CREATE UNIQUE INDEX channels_code_unique_ci ON channels (lower(code));
CREATE INDEX channels_status_id_idx ON channels (status, id);

CREATE TABLE channel_config_versions (
    channel_id BIGINT NOT NULL REFERENCES channels(id) ON DELETE RESTRICT,
    config_version BIGINT NOT NULL CHECK (config_version > 0),
    channel_type TEXT NOT NULL CHECK (channel_type IN ('qrcode', 'wecom_customer_acquisition')),
    carrier_type TEXT NOT NULL CHECK (carrier_type IN ('qrcode', 'link')),
    name TEXT NOT NULL CHECK (name = btrim(name) AND char_length(name) BETWEEN 1 AND 200),
    scene_value TEXT NOT NULL DEFAULT '' CHECK (scene_value = btrim(scene_value) AND char_length(scene_value) <= 10000),
    qrcode_url TEXT NOT NULL DEFAULT '' CHECK (char_length(qrcode_url) <= 10000),
    customer_channel TEXT NOT NULL DEFAULT '' CHECK (customer_channel = btrim(customer_channel) AND char_length(customer_channel) <= 10000),
    link_url TEXT NOT NULL DEFAULT '' CHECK (char_length(link_url) <= 10000),
    final_url TEXT NOT NULL DEFAULT '' CHECK (char_length(final_url) <= 10000),
    welcome_message TEXT NOT NULL DEFAULT '' CHECK (welcome_message = btrim(welcome_message) AND char_length(welcome_message) <= 10000),
    welcome_image_ids BIGINT[] NOT NULL DEFAULT '{}',
    welcome_miniprogram_ids BIGINT[] NOT NULL DEFAULT '{}',
    welcome_attachment_ids BIGINT[] NOT NULL DEFAULT '{}',
    welcome_group_invite_ids BIGINT[] NOT NULL DEFAULT '{}',
    auto_accept_friend BOOLEAN NOT NULL DEFAULT FALSE,
    entry_tag_id BIGINT CHECK (entry_tag_id IS NULL OR entry_tag_id > 0),
    entry_tag_name TEXT NOT NULL DEFAULT '' CHECK (entry_tag_name = btrim(entry_tag_name) AND char_length(entry_tag_name) <= 200),
    entry_tag_group_name TEXT NOT NULL DEFAULT '' CHECK (entry_tag_group_name = btrim(entry_tag_group_name) AND char_length(entry_tag_group_name) <= 200),
    assignment_mode TEXT NOT NULL CHECK (assignment_mode IN ('single_owner', 'multi_staff')),
    assignment_strategy TEXT NOT NULL CHECK (assignment_strategy IN ('ratio', 'cap_switch')),
    overflow_policy TEXT NOT NULL DEFAULT '' CHECK (
        overflow_policy = btrim(overflow_policy)
        AND char_length(overflow_policy) <= 128
        AND overflow_policy !~ '://'
    ),
    config_digest BYTEA NOT NULL CHECK (octet_length(config_digest) = 32),
    created_by BIGINT NOT NULL REFERENCES admin_users(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (channel_id, config_version),
    CONSTRAINT channel_config_type_carrier CHECK (
        (channel_type = 'qrcode' AND carrier_type = 'qrcode')
        OR (channel_type = 'wecom_customer_acquisition' AND carrier_type = 'link')
    ),
    CONSTRAINT channel_config_entry_tag_shape CHECK (
        (entry_tag_id IS NULL AND entry_tag_name = '' AND entry_tag_group_name = '')
        OR (entry_tag_id IS NOT NULL AND entry_tag_name <> '' AND entry_tag_group_name <> '')
    ),
    CONSTRAINT channel_config_media_limits CHECK (
        cardinality(welcome_image_ids) <= 12
        AND cardinality(welcome_miniprogram_ids) <= 12
        AND cardinality(welcome_attachment_ids) <= 12
        AND cardinality(welcome_group_invite_ids) <= 12
    )
);

ALTER TABLE channels
    ADD CONSTRAINT channels_current_config_fk
    FOREIGN KEY (id, current_config_version)
    REFERENCES channel_config_versions(channel_id, config_version)
    ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE channel_assignees (
    channel_id BIGINT NOT NULL,
    config_version BIGINT NOT NULL,
    staff_id BIGINT NOT NULL REFERENCES admin_users(id) ON DELETE RESTRICT,
    priority INTEGER NOT NULL CHECK (priority BETWEEN 1 AND 5),
    ratio_percent INTEGER CHECK (ratio_percent BETWEEN 1 AND 100),
    max_scans_24h INTEGER CHECK (max_scans_24h > 0),
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (channel_id, config_version, staff_id),
    CONSTRAINT channel_assignees_config_fk
        FOREIGN KEY (channel_id, config_version)
        REFERENCES channel_config_versions(channel_id, config_version) ON DELETE RESTRICT,
    CONSTRAINT channel_assignees_priority_unique UNIQUE (channel_id, config_version, priority),
    CONSTRAINT channel_assignees_strategy_value CHECK (
        (ratio_percent IS NOT NULL AND max_scans_24h IS NULL)
        OR (ratio_percent IS NULL AND max_scans_24h IS NOT NULL)
    )
);

CREATE INDEX channel_assignees_staff_idx ON channel_assignees (staff_id, channel_id);

CREATE TABLE channel_operation_receipts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    operation TEXT NOT NULL CHECK (operation IN ('create', 'update', 'set_status', 'archive', 'replace_assignees')),
    actor_admin_user_id BIGINT NOT NULL REFERENCES admin_users(id) ON DELETE RESTRICT,
    operation_key_digest BYTEA NOT NULL CHECK (octet_length(operation_key_digest) = 32),
    payload_digest BYTEA NOT NULL CHECK (octet_length(payload_digest) = 32),
    state TEXT NOT NULL DEFAULT 'in_progress' CHECK (state IN ('in_progress', 'completed')),
    channel_id BIGINT REFERENCES channels(id) ON DELETE RESTRICT,
    channel_version BIGINT CHECK (channel_version IS NULL OR channel_version > 0),
    created_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    CONSTRAINT channel_operation_receipts_key_unique UNIQUE (operation, actor_admin_user_id, operation_key_digest),
    CONSTRAINT channel_operation_receipts_completion_shape CHECK (
        (state = 'in_progress' AND channel_id IS NULL AND channel_version IS NULL AND completed_at IS NULL)
        OR (state = 'completed' AND channel_id IS NOT NULL AND channel_version IS NOT NULL AND completed_at IS NOT NULL)
    )
);

CREATE FUNCTION channel_catalog_guard() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog AS $$
BEGIN
    IF TG_TABLE_NAME = 'channels' THEN
        IF TG_OP = 'DELETE' OR TG_OP = 'TRUNCATE' THEN
            RAISE EXCEPTION 'channels are archive-only';
        END IF;
        IF NEW.code IS DISTINCT FROM OLD.code THEN
            RAISE EXCEPTION 'channel code is immutable';
        END IF;
        IF OLD.status = 'archived' AND NEW.status <> 'archived' THEN
            RAISE EXCEPTION 'archived channel is terminal';
        END IF;
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'channel configuration and assignment snapshots are immutable';
END;
$$;

CREATE TRIGGER channels_guard
    BEFORE UPDATE OR DELETE ON channels
    FOR EACH ROW EXECUTE FUNCTION channel_catalog_guard();
CREATE TRIGGER channels_no_truncate
    BEFORE TRUNCATE ON channels
    FOR EACH STATEMENT EXECUTE FUNCTION channel_catalog_guard();
CREATE TRIGGER channel_config_versions_immutable
    BEFORE UPDATE OR DELETE ON channel_config_versions
    FOR EACH STATEMENT EXECUTE FUNCTION channel_catalog_guard();
CREATE TRIGGER channel_config_versions_no_truncate
    BEFORE TRUNCATE ON channel_config_versions
    FOR EACH STATEMENT EXECUTE FUNCTION channel_catalog_guard();
CREATE TRIGGER channel_assignees_immutable
    BEFORE UPDATE OR DELETE ON channel_assignees
    FOR EACH STATEMENT EXECUTE FUNCTION channel_catalog_guard();
CREATE TRIGGER channel_assignees_no_truncate
    BEFORE TRUNCATE ON channel_assignees
    FOR EACH STATEMENT EXECUTE FUNCTION channel_catalog_guard();

CREATE FUNCTION channel_receipt_guard() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog AS $$
BEGIN
    IF TG_OP = 'DELETE' OR TG_OP = 'TRUNCATE' OR OLD.state = 'completed' THEN
        RAISE EXCEPTION 'completed channel operation receipts are immutable';
    END IF;
    IF NEW.operation IS DISTINCT FROM OLD.operation
       OR NEW.actor_admin_user_id IS DISTINCT FROM OLD.actor_admin_user_id
       OR NEW.operation_key_digest IS DISTINCT FROM OLD.operation_key_digest
       OR NEW.payload_digest IS DISTINCT FROM OLD.payload_digest
       OR NEW.created_at IS DISTINCT FROM OLD.created_at
       OR NEW.state <> 'completed' THEN
        RAISE EXCEPTION 'invalid channel operation receipt transition';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER channel_operation_receipts_guard
    BEFORE UPDATE OR DELETE ON channel_operation_receipts
    FOR EACH ROW EXECUTE FUNCTION channel_receipt_guard();
CREATE TRIGGER channel_operation_receipts_no_truncate
    BEFORE TRUNCATE ON channel_operation_receipts
    FOR EACH STATEMENT EXECUTE FUNCTION channel_receipt_guard();
