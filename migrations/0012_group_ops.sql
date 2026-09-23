-- Owner: internal/groupops.
-- PR06 is a local Group Ops plan and durable execution projection.  It stores
-- only local admin/staff references and opaque group references.  Customer,
-- OneID, Audience, recipient, Provider credential and Provider response data
-- are intentionally absent.

CREATE TABLE group_ops_plans (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('draft','active','paused','archived')),
    revision BIGINT NOT NULL CHECK (revision > 0),
    created_by BIGINT NOT NULL REFERENCES admin_users(id),
    updated_by BIGINT NOT NULL REFERENCES admin_users(id),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT group_ops_plans_name CHECK (btrim(name) = name AND name <> '' AND char_length(name) <= 128),
    CONSTRAINT group_ops_plans_timestamps CHECK (updated_at >= created_at)
);
CREATE INDEX group_ops_plans_updated_idx ON group_ops_plans(updated_at DESC, id DESC);

CREATE TABLE group_ops_plan_members (
    plan_id BIGINT NOT NULL REFERENCES group_ops_plans(id) ON DELETE CASCADE,
    staff_id BIGINT NOT NULL REFERENCES admin_users(id) ON DELETE RESTRICT,
    PRIMARY KEY (plan_id, staff_id)
);

CREATE TABLE group_ops_plan_group_assets (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    plan_id BIGINT NOT NULL REFERENCES group_ops_plans(id) ON DELETE CASCADE,
    asset_reference TEXT NOT NULL,
    CONSTRAINT group_ops_plan_group_assets_reference CHECK (
        asset_reference ~ '^[A-Za-z0-9._:-]{1,128}$' AND position('://' IN asset_reference) = 0
    ),
    UNIQUE (plan_id, asset_reference)
);
CREATE INDEX group_ops_plan_group_assets_plan_idx ON group_ops_plan_group_assets(plan_id, asset_reference, id);

CREATE TABLE group_ops_plan_nodes (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    plan_id BIGINT NOT NULL REFERENCES group_ops_plans(id) ON DELETE CASCADE,
    position INTEGER NOT NULL CHECK (position > 0 AND position <= 1000),
    kind TEXT NOT NULL CHECK (kind IN ('message','delay')),
    message_text TEXT NOT NULL DEFAULT '',
    delay_minutes INTEGER NOT NULL DEFAULT 0,
    material_reference TEXT NOT NULL DEFAULT '',
    material_plan JSONB NOT NULL DEFAULT '{"references":[]}'::jsonb,
    CONSTRAINT group_ops_plan_nodes_material_plan CHECK (
        jsonb_typeof(material_plan) = 'object'
        AND material_plan ? 'references'
        AND jsonb_typeof(material_plan->'references') = 'array'
        AND jsonb_array_length(material_plan->'references') BETWEEN 0 AND 9
    ),
    CONSTRAINT group_ops_plan_nodes_material_reference CHECK (
        material_reference = '' OR material_reference ~ '^[A-Za-z0-9._:-]{1,128}$'
    ),
    CONSTRAINT group_ops_plan_nodes_content CHECK (
        (kind = 'message' AND delay_minutes = 0 AND btrim(message_text) = message_text
            AND char_length(message_text) <= 1000
            AND (message_text <> '' OR jsonb_array_length(material_plan->'references') > 0))
        OR (kind = 'delay' AND message_text = '' AND material_reference = ''
            AND jsonb_array_length(material_plan->'references') = 0 AND delay_minutes BETWEEN 1 AND 10080)
    )
);
CREATE UNIQUE INDEX group_ops_plan_nodes_position_unique ON group_ops_plan_nodes(plan_id, position);
CREATE INDEX group_ops_plan_nodes_plan_idx ON group_ops_plan_nodes(plan_id, position, id);

CREATE TABLE group_ops_plan_webhook_descriptors (
    plan_id BIGINT PRIMARY KEY REFERENCES group_ops_plans(id) ON DELETE CASCADE,
    reference TEXT NOT NULL DEFAULT '',
    CONSTRAINT group_ops_plan_webhook_reference CHECK (
        reference = '' OR (
            reference ~ '^[A-Za-z0-9._:-]{1,128}$' AND position('://' IN reference) = 0
            AND lower(reference) NOT LIKE '%secret%' AND lower(reference) NOT LIKE '%token%'
            AND lower(reference) NOT LIKE '%password%' AND lower(reference) NOT LIKE '%api_key%'
        )
    ),
    UNIQUE(reference)
);

CREATE TABLE group_ops_operation_receipts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    operation TEXT NOT NULL CHECK (operation IN (
        'plan_create','plan_update','plan_activate','plan_pause','plan_archive',
        'member_add','member_remove','group_asset_add','group_asset_remove',
        'node_add','node_update','node_remove','webhook_descriptor_put',
        'runtime_run_due','runtime_broadcast','runtime_webhook',
        'directory_groups_refresh','directory_members_refresh','execution_reconcile'
    )),
    actor_scope TEXT NOT NULL,
    key_digest BYTEA NOT NULL CHECK (octet_length(key_digest) = 32),
    payload_digest BYTEA NOT NULL CHECK (octet_length(payload_digest) = 32),
    state TEXT NOT NULL CHECK (state IN ('in_progress','completed')),
    result_snapshot JSONB,
    created_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    CONSTRAINT group_ops_operation_receipts_actor CHECK (
        actor_scope ~ '^(admin:[1-9][0-9]*|service:[A-Za-z0-9._:-]{1,128}|webhook:[A-Za-z0-9._:-]{1,128})$'
    ),
    CONSTRAINT group_ops_operation_receipts_completion CHECK (
        (state = 'in_progress' AND result_snapshot IS NULL AND completed_at IS NULL)
        OR (state = 'completed' AND result_snapshot IS NOT NULL AND completed_at IS NOT NULL)
    ),
    UNIQUE(operation, actor_scope, key_digest)
);
CREATE INDEX group_ops_operation_receipts_plan_idx ON group_ops_operation_receipts(created_at DESC, id DESC);

CREATE TABLE group_ops_audit_events (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    event_type TEXT NOT NULL CHECK (event_type = 'group_ops.plan_updated'),
    idempotency_key TEXT NOT NULL UNIQUE CHECK (length(idempotency_key) BETWEEN 8 AND 200),
    actor_admin_user_id BIGINT NOT NULL REFERENCES admin_users(id),
    payload JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(payload) = 'object'),
    occurred_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);

CREATE TABLE group_ops_outbox (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    event_type TEXT NOT NULL CHECK (event_type = 'group_ops.plan_updated'),
    aggregate_id BIGINT NOT NULL REFERENCES group_ops_plans(id) ON DELETE RESTRICT,
    payload JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(payload) = 'object'),
    idempotency_key TEXT NOT NULL UNIQUE CHECK (length(idempotency_key) BETWEEN 8 AND 200),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    published_at TIMESTAMPTZ
);

-- The run is an immutable intake fact.  It is separate from EER so a
-- provider-disabled attempt still has a durable local receipt and summary.
CREATE TABLE group_ops_runs (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    plan_id BIGINT NOT NULL REFERENCES group_ops_plans(id) ON DELETE RESTRICT,
    trigger_kind TEXT NOT NULL CHECK (trigger_kind IN ('run_due','broadcast','webhook')),
    source_key_digest BYTEA NOT NULL CHECK (octet_length(source_key_digest) = 32),
    plan_revision BIGINT NOT NULL CHECK (plan_revision > 0),
    scheduled_for TIMESTAMPTZ NOT NULL,
    accepted_at TIMESTAMPTZ NOT NULL,
    accepted_by TEXT NOT NULL CHECK (accepted_by ~ '^(admin:[1-9][0-9]*|service:[A-Za-z0-9._:-]{1,128}|webhook:[A-Za-z0-9._:-]{1,128})$'),
    UNIQUE(plan_id, trigger_kind, source_key_digest)
);
CREATE INDEX group_ops_runs_plan_idx ON group_ops_runs(plan_id, accepted_at DESC, id DESC);

CREATE TABLE group_ops_executions (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    run_id BIGINT NOT NULL REFERENCES group_ops_runs(id) ON DELETE RESTRICT,
    plan_id BIGINT NOT NULL REFERENCES group_ops_plans(id) ON DELETE RESTRICT,
    node_id BIGINT NOT NULL REFERENCES group_ops_plan_nodes(id) ON DELETE RESTRICT,
    plan_revision BIGINT NOT NULL CHECK (plan_revision > 0),
    node_position INTEGER NOT NULL CHECK (node_position > 0),
    target_reference TEXT NOT NULL CHECK (target_reference ~ '^[A-Za-z0-9._:-]{1,128}$'),
    sender_userid_snapshot TEXT NOT NULL CHECK (sender_userid_snapshot ~ '^[^[:space:]]{1,128}$'),
    target_digest TEXT NOT NULL CHECK (target_digest ~ '^sha256:[0-9a-f]{64}$'),
    content_snapshot JSONB NOT NULL CHECK (jsonb_typeof(content_snapshot) = 'object'),
    content_digest TEXT NOT NULL CHECK (content_digest ~ '^sha256:[0-9a-f]{64}$'),
    material_snapshot JSONB NOT NULL CHECK (jsonb_typeof(material_snapshot) = 'object'),
    material_digest TEXT NOT NULL CHECK (material_digest ~ '^sha256:[0-9a-f]{64}$'),
    execution_key_digest BYTEA NOT NULL UNIQUE CHECK (octet_length(execution_key_digest) = 32),
    external_effect_id BIGINT NOT NULL UNIQUE REFERENCES external_effects(id) ON DELETE RESTRICT,
    state TEXT NOT NULL DEFAULT 'accepted' CHECK (state IN ('accepted','provider_accepted','delivery_proven','outcome_unknown','reconciled','final_failed')),
    provider_accepted BOOLEAN NOT NULL DEFAULT FALSE,
    delivery_proven BOOLEAN NOT NULL DEFAULT FALSE,
    provider_receipt_digest TEXT CHECK (provider_receipt_digest IS NULL OR provider_receipt_digest ~ '^sha256:[0-9a-f]{64}$'),
    reconciliation_evidence_digest TEXT CHECK (reconciliation_evidence_digest IS NULL OR reconciliation_evidence_digest ~ '^sha256:[0-9a-f]{64}$'),
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    scheduled_for TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT group_ops_executions_flags CHECK (
        (delivery_proven = FALSE OR provider_accepted = TRUE)
        AND (state <> 'accepted' OR (provider_accepted = FALSE AND delivery_proven = FALSE AND attempt_count = 0))
    ),
    CONSTRAINT group_ops_executions_schedule CHECK (scheduled_for >= created_at),
    CONSTRAINT group_ops_executions_timestamps CHECK (updated_at >= created_at),
    UNIQUE(run_id, node_id, target_reference)
);
CREATE INDEX group_ops_executions_plan_idx ON group_ops_executions(plan_id, created_at DESC, id DESC);
CREATE INDEX group_ops_executions_state_idx ON group_ops_executions(state, updated_at DESC, id DESC);

CREATE TABLE group_ops_directory_groups (
    chat_reference TEXT PRIMARY KEY CHECK (chat_reference ~ '^[A-Za-z0-9._:-]{1,128}$'),
    owner_staff_id BIGINT NOT NULL REFERENCES admin_users(id) ON DELETE RESTRICT,
    display_name TEXT NOT NULL CHECK (btrim(display_name) = display_name AND char_length(display_name) BETWEEN 1 AND 128),
    member_count INTEGER NOT NULL CHECK (member_count >= 0),
    source_digest TEXT NOT NULL CHECK (source_digest ~ '^sha256:[0-9a-f]{64}$'),
    refreshed_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX group_ops_directory_groups_owner_idx ON group_ops_directory_groups(owner_staff_id, refreshed_at DESC, chat_reference);

CREATE TABLE group_ops_directory_refresh_receipts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    refresh_kind TEXT NOT NULL CHECK (refresh_kind IN ('members','operation_members','groups')),
    actor_id BIGINT NOT NULL REFERENCES admin_users(id),
    owner_staff_id BIGINT REFERENCES admin_users(id),
    key_digest BYTEA NOT NULL CHECK (octet_length(key_digest) = 32),
    snapshot_digest TEXT NOT NULL CHECK (snapshot_digest ~ '^sha256:[0-9a-f]{64}$'),
    item_count INTEGER NOT NULL CHECK (item_count >= 0),
    provider_read_executed BOOLEAN NOT NULL DEFAULT FALSE,
    refreshed_at TIMESTAMPTZ NOT NULL,
    UNIQUE(refresh_kind, actor_id, key_digest)
);

-- Protocol replay records are opaque event IDs only; webhook bodies and
-- signatures are never retained in this owner table.
CREATE TABLE group_ops_protocol_replays (
    client_id TEXT NOT NULL CHECK (client_id = 'aicrm-webhook-group-ops'),
    resource_reference TEXT NOT NULL CHECK (resource_reference ~ '^[A-Za-z0-9._:-]{1,128}$'),
    event_id_digest BYTEA NOT NULL CHECK (octet_length(event_id_digest) = 32),
    payload_digest BYTEA NOT NULL CHECK (octet_length(payload_digest) = 32),
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (client_id, event_id_digest)
);
