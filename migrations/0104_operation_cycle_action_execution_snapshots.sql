-- OperationCycle owns the immutable execution material issued with an action.
-- A runner must never reconstruct a task from a later strategy/run revision.
CREATE TABLE operation_cycle_action_execution_snapshots (
  request_id TEXT PRIMARY KEY REFERENCES operation_cycle_action_requests(request_id) ON DELETE RESTRICT,
  execution JSONB NOT NULL,
  context_summary JSONB NOT NULL,
  execution_hash BYTEA NOT NULL,
  context_hash BYTEA NOT NULL,
  created_at TIMESTAMPTZ NOT NULL,
  CONSTRAINT operation_cycle_action_execution_snapshots_execution_object CHECK (jsonb_typeof(execution) = 'object'),
  CONSTRAINT operation_cycle_action_execution_snapshots_context_object CHECK (jsonb_typeof(context_summary) = 'object'),
  CONSTRAINT operation_cycle_action_execution_snapshots_execution_hash CHECK (octet_length(execution_hash) = 32),
  CONSTRAINT operation_cycle_action_execution_snapshots_context_hash CHECK (octet_length(context_hash) = 32)
);
