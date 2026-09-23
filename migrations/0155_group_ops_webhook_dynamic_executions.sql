-- Owner: Group Ops. Forward-only: a dynamic Webhook execution deliberately
-- has no plan node. Node-triggered executions retain their existing foreign
-- key and uniqueness invariant; NULL denotes only the immutable inbound
-- message frozen on a Webhook run.
ALTER TABLE group_ops_executions
    ALTER COLUMN node_id DROP NOT NULL;

ALTER TABLE group_ops_execution_intents
    ALTER COLUMN node_id DROP NOT NULL;

-- This digest is an owner-side second fence for callers that enter the
-- Runtime port directly. It records canonical typed input only, never the
-- raw body or HMAC, and makes same event/key with different content conflict.
ALTER TABLE group_ops_runs
    ADD COLUMN webhook_payload_digest TEXT
        CHECK (webhook_payload_digest IS NULL OR webhook_payload_digest ~ '^sha256:[0-9a-f]{64}$');

-- Existing replay rows predate the typed dynamic inbound contract. They must
-- remain non-replayable after upgrade: their legacy runtime source key could
-- include the plan revision and cannot safely authorize a v2 dynamic run.
-- New claims are written as version 2 by the Group Ops store.
ALTER TABLE group_ops_protocol_replays
    ADD COLUMN protocol_version SMALLINT NOT NULL DEFAULT 1
        CHECK (protocol_version IN (1, 2));

-- PostgreSQL unique constraints consider NULL values distinct. These partial
-- indexes retain the one dynamic intent/execution per run+bound target rule.
CREATE UNIQUE INDEX group_ops_executions_webhook_run_target_unique
    ON group_ops_executions(run_id, target_reference)
    WHERE node_id IS NULL;

CREATE UNIQUE INDEX group_ops_execution_intents_webhook_run_target_unique
    ON group_ops_execution_intents(run_id, target_reference)
    WHERE node_id IS NULL;
