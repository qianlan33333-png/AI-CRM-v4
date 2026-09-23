-- PR07 owns only local agent/fixed-script configuration.  No customer,
-- audience, provider or execution records are stored here.
CREATE TABLE automation_agents (
  id BIGSERIAL PRIMARY KEY,
  agent_name TEXT NOT NULL,
  agent_code TEXT NOT NULL UNIQUE,
  automation_type TEXT NOT NULL CHECK (automation_type IN ('agent','fixed_script')),
  status TEXT NOT NULL CHECK (status IN ('active','paused','archived')) DEFAULT 'paused',
  execution_enabled BOOLEAN NOT NULL DEFAULT false,
  CHECK ((status = 'active') = execution_enabled),
  draft_role_prompt TEXT NOT NULL DEFAULT '', draft_task_prompt TEXT NOT NULL DEFAULT '',
  published_role_prompt TEXT NOT NULL DEFAULT '', published_task_prompt TEXT NOT NULL DEFAULT '',
  draft_version BIGINT NOT NULL DEFAULT 1 CHECK (draft_version > 0),
  published_version BIGINT NOT NULL DEFAULT 0 CHECK (published_version >= 0),
  fixed_content_package JSONB NOT NULL DEFAULT '{}'::jsonb,
  legacy_configuration JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_by BIGINT NOT NULL, updated_by BIGINT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(), updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  archived_at TIMESTAMPTZ
);
CREATE INDEX automation_agents_visible_idx ON automation_agents(automation_type, updated_at DESC, id DESC) WHERE archived_at IS NULL;
CREATE TABLE automation_operation_receipts (
  id BIGSERIAL PRIMARY KEY, operation TEXT NOT NULL, actor_scope TEXT NOT NULL,
  key_digest BYTEA NOT NULL, payload_digest BYTEA NOT NULL, state TEXT NOT NULL CHECK (state IN ('reserved','completed')),
  result_snapshot JSONB, created_at TIMESTAMPTZ NOT NULL, completed_at TIMESTAMPTZ,
  UNIQUE(operation, actor_scope, key_digest)
);
CREATE TABLE automation_audit_events (
  id BIGSERIAL PRIMARY KEY, agent_id BIGINT NOT NULL REFERENCES automation_agents(id) ON DELETE RESTRICT,
  operation TEXT NOT NULL, actor_id BIGINT NOT NULL, occurred_at TIMESTAMPTZ NOT NULL, payload_digest BYTEA NOT NULL
);
CREATE TABLE automation_outbox (
  id BIGSERIAL PRIMARY KEY, event_type TEXT NOT NULL, agent_id BIGINT NOT NULL REFERENCES automation_agents(id) ON DELETE RESTRICT,
  payload JSONB NOT NULL, idempotency_digest BYTEA NOT NULL, occurred_at TIMESTAMPTZ NOT NULL
);
CREATE OR REPLACE FUNCTION automation_append_only() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'automation audit/outbox is append-only'; END $$;
CREATE TRIGGER automation_audit_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON automation_audit_events FOR EACH STATEMENT EXECUTE FUNCTION automation_append_only();
CREATE TRIGGER automation_outbox_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON automation_outbox FOR EACH STATEMENT EXECUTE FUNCTION automation_append_only();
