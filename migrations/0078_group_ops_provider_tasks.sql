-- PRD07: Group Ops owns provider task evidence and frozen, not-yet-accepted
-- per-group node intents. These are business dependency facts; River and EER
-- continue to own execution, lease, retry, and scheduling mechanics.
CREATE TABLE group_ops_execution_intents (
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
    material_source_snapshot JSONB NOT NULL CHECK (jsonb_typeof(material_source_snapshot) = 'object'),
    material_source_digest TEXT NOT NULL CHECK (material_source_digest ~ '^sha256:[0-9a-f]{64}$'),
    execution_key_digest BYTEA NOT NULL UNIQUE CHECK (octet_length(execution_key_digest) = 32),
    predecessor_intent_id BIGINT REFERENCES group_ops_execution_intents(id) ON DELETE RESTRICT,
    state TEXT NOT NULL CHECK (state IN ('waiting','ready_to_accept','accepted','halted')),
    external_effect_id BIGINT UNIQUE REFERENCES external_effects(id) ON DELETE RESTRICT,
    scheduled_for TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    UNIQUE(run_id,node_id,target_reference),
    CONSTRAINT group_ops_execution_intents_schedule CHECK (scheduled_for >= created_at),
    CONSTRAINT group_ops_execution_intents_timestamps CHECK (updated_at >= created_at),
    CONSTRAINT group_ops_execution_intents_binding CHECK (
      (state = 'accepted' AND external_effect_id IS NOT NULL) OR
      (state <> 'accepted' AND external_effect_id IS NULL)
    )
);
CREATE INDEX group_ops_execution_intents_predecessor_idx
  ON group_ops_execution_intents(predecessor_intent_id,state,id);
CREATE INDEX group_ops_execution_intents_run_target_idx
  ON group_ops_execution_intents(run_id,target_reference,node_position,id);

ALTER TABLE group_ops_executions
  ADD COLUMN material_source_snapshot JSONB NOT NULL DEFAULT '{"preparations":[],"schema_version":1,"sources":{"references":[],"schema_version":1}}'::jsonb,
  ADD COLUMN material_source_digest TEXT NOT NULL DEFAULT 'sha256:768811b26bb9b284090b10de4c82f7ed5c937e5d9046cfc0231f226cf6d56048'
    CHECK (material_source_digest ~ '^sha256:[0-9a-f]{64}$');

ALTER TABLE group_ops_operation_receipts DROP CONSTRAINT group_ops_operation_receipts_operation_check;
ALTER TABLE group_ops_operation_receipts ADD CONSTRAINT group_ops_operation_receipts_operation_check CHECK (operation IN (
  'plan_create','plan_update','plan_activate','plan_pause','plan_archive',
  'member_add','member_remove','group_asset_add','group_asset_remove',
  'node_add','node_update','node_remove','webhook_descriptor_put',
  'runtime_run_due','runtime_broadcast','runtime_webhook',
  'directory_groups_refresh','directory_members_refresh','execution_reconcile','execution_delivery_read'
));

CREATE TABLE group_ops_group_message_tasks (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    execution_id BIGINT NOT NULL UNIQUE REFERENCES group_ops_executions(id) ON DELETE RESTRICT,
    external_effect_id BIGINT NOT NULL UNIQUE REFERENCES external_effects(id) ON DELETE RESTRICT,
    msgid TEXT NOT NULL CHECK (length(msgid) BETWEEN 1 AND 256 AND msgid ~ '^[^[:space:]]+$'),
    sender_userid_snapshot TEXT NOT NULL CHECK (sender_userid_snapshot ~ '^[^[:space:]]{1,128}$'),
    chat_reference TEXT NOT NULL CHECK (chat_reference ~ '^[A-Za-z0-9._:-]{1,128}$'),
    task_evidence_digest TEXT NOT NULL CHECK (task_evidence_digest ~ '^sha256:[0-9a-f]{64}$'),
    delivery_status INTEGER,
    delivery_evidence_digest TEXT CHECK (delivery_evidence_digest IS NULL OR delivery_evidence_digest ~ '^sha256:[0-9a-f]{64}$'),
    accepted_at TIMESTAMPTZ NOT NULL,
    delivery_checked_at TIMESTAMPTZ,
    CONSTRAINT group_ops_group_message_tasks_delivery CHECK (
      (delivery_status IS NULL AND delivery_evidence_digest IS NULL AND delivery_checked_at IS NULL) OR
      (delivery_status IS NOT NULL AND delivery_evidence_digest IS NOT NULL AND delivery_checked_at IS NOT NULL)
    )
);
CREATE INDEX group_ops_group_message_tasks_msgid_idx ON group_ops_group_message_tasks(msgid);
