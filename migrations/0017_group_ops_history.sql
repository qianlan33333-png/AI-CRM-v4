-- Owner: internal/groupops.
-- Frozen donor history is a separate, sealed v3 projection. It has no
-- foreign-key edge to current admin ownership, customers, OneID, runtime, or
-- Provider data: source staff IDs are archival facts, not active principals.

CREATE TABLE group_ops_v1_history_plans (
    plan_id BIGINT PRIMARY KEY CHECK (plan_id > 0),
    name TEXT NOT NULL CHECK (btrim(name) = name AND name <> '' AND char_length(name) <= 128),
    status TEXT NOT NULL CHECK (status = 'archived'),
    revision BIGINT NOT NULL CHECK (revision = 1),
    created_by BIGINT NOT NULL CHECK (created_by > 0),
    updated_by BIGINT NOT NULL CHECK (updated_by > 0),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL CHECK (updated_at >= created_at),
    source_plan_id BIGINT NOT NULL UNIQUE CHECK (source_plan_id > 0),
    source_code TEXT NOT NULL CHECK (btrim(source_code) = source_code AND source_code <> ''),
    plan_type TEXT NOT NULL CHECK (btrim(plan_type) = plan_type AND plan_type <> ''),
    original_status TEXT NOT NULL CHECK (btrim(original_status) = original_status AND original_status <> ''),
    owner_staff_id BIGINT CHECK (owner_staff_id IS NULL OR owner_staff_id > 0),
    archived_at TIMESTAMPTZ,
    UNIQUE (plan_id, source_plan_id)
);

CREATE TABLE group_ops_v1_history_directory (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    source_kind TEXT NOT NULL CHECK (source_kind IN ('group_chats', 'wecom_group_chat_snapshots')),
    source_id BIGINT CHECK (source_id IS NULL OR source_id > 0),
    chat_reference TEXT NOT NULL CHECK (btrim(chat_reference) = chat_reference AND chat_reference <> '' AND char_length(chat_reference) <= 128),
    display_name TEXT,
    owner_staff_id BIGINT CHECK (owner_staff_id IS NULL OR owner_staff_id > 0),
    owner_name TEXT,
    member_count INTEGER CHECK (member_count IS NULL OR member_count >= 0),
    internal_member_count INTEGER CHECK (internal_member_count IS NULL OR internal_member_count >= 0),
    external_member_count INTEGER CHECK (external_member_count IS NULL OR external_member_count >= 0),
    original_status TEXT NOT NULL CHECK (btrim(original_status) = original_status AND original_status <> ''),
    recorded_at TIMESTAMPTZ NOT NULL,
    CHECK (
        (source_kind = 'group_chats' AND source_id IS NOT NULL AND member_count IS NOT NULL
            AND internal_member_count IS NULL AND external_member_count IS NULL AND owner_name IS NULL)
        OR (source_kind = 'wecom_group_chat_snapshots' AND source_id IS NULL AND member_count IS NULL
            AND internal_member_count IS NOT NULL AND external_member_count IS NOT NULL)
    )
);
CREATE UNIQUE INDEX group_ops_v1_history_directory_source_id_idx ON group_ops_v1_history_directory(source_id) WHERE source_kind = 'group_chats';
CREATE UNIQUE INDEX group_ops_v1_history_directory_snapshot_idx ON group_ops_v1_history_directory(chat_reference) WHERE source_kind = 'wecom_group_chat_snapshots';

CREATE TABLE group_ops_v1_history_groups (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    source_group_id BIGINT NOT NULL UNIQUE CHECK (source_group_id > 0),
    source_plan_id BIGINT NOT NULL CHECK (source_plan_id > 0),
    plan_id BIGINT NOT NULL,
    chat_reference TEXT NOT NULL CHECK (btrim(chat_reference) = chat_reference AND chat_reference <> '' AND char_length(chat_reference) <= 128),
    display_name TEXT NOT NULL CHECK (btrim(display_name) = display_name AND display_name <> '' AND char_length(display_name) <= 128),
    owner_staff_id BIGINT CHECK (owner_staff_id IS NULL OR owner_staff_id > 0),
    internal_member_count INTEGER NOT NULL CHECK (internal_member_count >= 0),
    external_member_count INTEGER NOT NULL CHECK (external_member_count >= 0),
    original_status TEXT NOT NULL CHECK (btrim(original_status) = original_status AND original_status <> ''),
    created_at TIMESTAMPTZ NOT NULL,
    removed_at TIMESTAMPTZ,
    FOREIGN KEY (plan_id, source_plan_id) REFERENCES group_ops_v1_history_plans(plan_id, source_plan_id) ON DELETE RESTRICT
);
CREATE INDEX group_ops_v1_history_groups_plan_idx ON group_ops_v1_history_groups(plan_id, id);

CREATE TABLE group_ops_v1_history_nodes (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    source_node_id BIGINT NOT NULL UNIQUE CHECK (source_node_id > 0),
    source_plan_id BIGINT NOT NULL CHECK (source_plan_id > 0),
    plan_id BIGINT NOT NULL,
    day_index INTEGER NOT NULL CHECK (day_index >= 0),
    trigger_time TEXT NOT NULL CHECK (btrim(trigger_time) = trigger_time AND trigger_time <> ''),
    sort_order INTEGER NOT NULL CHECK (sort_order >= 0),
    original_status TEXT NOT NULL CHECK (btrim(original_status) = original_status AND original_status <> ''),
    content_package JSONB NOT NULL CHECK (jsonb_typeof(content_package) = 'object'),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL CHECK (updated_at >= created_at),
    FOREIGN KEY (plan_id, source_plan_id) REFERENCES group_ops_v1_history_plans(plan_id, source_plan_id) ON DELETE RESTRICT
);
CREATE INDEX group_ops_v1_history_nodes_plan_idx ON group_ops_v1_history_nodes(plan_id, sort_order, id);

CREATE FUNCTION aicrm_group_ops_history_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'Group Ops history is immutable; restore or import a verified source snapshot instead' USING ERRCODE = '55000';
END;
$$;

CREATE TRIGGER group_ops_v1_history_plans_immutable BEFORE UPDATE OR DELETE ON group_ops_v1_history_plans FOR EACH ROW EXECUTE FUNCTION aicrm_group_ops_history_immutable();
CREATE TRIGGER group_ops_v1_history_directory_immutable BEFORE UPDATE OR DELETE ON group_ops_v1_history_directory FOR EACH ROW EXECUTE FUNCTION aicrm_group_ops_history_immutable();
CREATE TRIGGER group_ops_v1_history_groups_immutable BEFORE UPDATE OR DELETE ON group_ops_v1_history_groups FOR EACH ROW EXECUTE FUNCTION aicrm_group_ops_history_immutable();
CREATE TRIGGER group_ops_v1_history_nodes_immutable BEFORE UPDATE OR DELETE ON group_ops_v1_history_nodes FOR EACH ROW EXECUTE FUNCTION aicrm_group_ops_history_immutable();
