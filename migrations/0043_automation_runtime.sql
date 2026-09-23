-- Owner: Automation runtime. Segment, Customer and Agent identifiers are
-- opaque stable-port references; there are no cross-domain foreign keys.
CREATE TABLE automation_policies (
  id BIGSERIAL PRIMARY KEY, code TEXT NOT NULL UNIQUE CHECK(code ~ '^[a-z0-9][a-z0-9_-]{0,119}$'), name TEXT NOT NULL CHECK(length(btrim(name)) BETWEEN 1 AND 200),
  lifecycle TEXT NOT NULL CHECK(lifecycle IN ('paused','active','archived')) DEFAULT 'paused', version BIGINT NOT NULL DEFAULT 1 CHECK(version>0), current_version_id BIGINT,
  created_by BIGINT NOT NULL CHECK(created_by>0), updated_by BIGINT NOT NULL CHECK(updated_by>0), created_at TIMESTAMPTZ NOT NULL, updated_at TIMESTAMPTZ NOT NULL, archived_at TIMESTAMPTZ,
  CHECK((lifecycle='archived')=(archived_at IS NOT NULL))
);
CREATE TABLE automation_policy_versions (
  id BIGSERIAL PRIMARY KEY, policy_id BIGINT NOT NULL REFERENCES automation_policies(id) ON DELETE RESTRICT, version BIGINT NOT NULL CHECK(version>0),
  package_id BIGINT NOT NULL CHECK(package_id>0), trigger_kind TEXT NOT NULL CHECK(trigger_kind IN ('audience.member_entered.v1','customer.tag_applied.v1')),
  trigger_enabled BOOLEAN NOT NULL, action_kind TEXT NOT NULL CHECK(action_kind IN ('record','outbound_message')), action_config JSONB NOT NULL CHECK(jsonb_typeof(action_config)='object'),
  quiet_hours JSONB NOT NULL DEFAULT '{}'::jsonb CHECK(jsonb_typeof(quiet_hours)='object'), single_run_limit INTEGER NOT NULL CHECK(single_run_limit BETWEEN 1 AND 100000), approval_staff_id BIGINT CHECK(approval_staff_id IS NULL OR approval_staff_id>0),
  digest BYTEA NOT NULL CHECK(octet_length(digest)=32), created_by BIGINT NOT NULL CHECK(created_by>0), created_at TIMESTAMPTZ NOT NULL,
  UNIQUE(policy_id,version), UNIQUE(id,policy_id)
);
ALTER TABLE automation_policies ADD CONSTRAINT automation_policies_current_version_fk FOREIGN KEY(current_version_id,id) REFERENCES automation_policy_versions(id,policy_id) ON DELETE RESTRICT;

CREATE TABLE automation_enrollments (
  id BIGSERIAL PRIMARY KEY, policy_id BIGINT NOT NULL REFERENCES automation_policies(id) ON DELETE RESTRICT, policy_version_id BIGINT NOT NULL REFERENCES automation_policy_versions(id) ON DELETE RESTRICT,
  source_event_digest BYTEA NOT NULL CHECK(octet_length(source_event_digest)=32), customer_id BIGINT NOT NULL CHECK(customer_id>0), action_kind TEXT NOT NULL CHECK(action_kind IN ('record','outbound_message')),
  action_snapshot JSONB NOT NULL CHECK(jsonb_typeof(action_snapshot)='object'), action_digest BYTEA NOT NULL CHECK(octet_length(action_digest)=32), state TEXT NOT NULL CHECK(state IN ('recorded','pending','accepted','skipped','failed')),
  created_at TIMESTAMPTZ NOT NULL, UNIQUE(policy_version_id,source_event_digest,customer_id)
);

CREATE TABLE automation_run_previews (
  id BIGSERIAL PRIMARY KEY, package_id BIGINT NOT NULL CHECK(package_id>0), package_version BIGINT NOT NULL CHECK(package_version>0), snapshot_id BIGINT NOT NULL CHECK(snapshot_id>0), configuration_version_id BIGINT NOT NULL CHECK(configuration_version_id>0),
  agent_id BIGINT NOT NULL CHECK(agent_id>0), agent_published_version BIGINT NOT NULL CHECK(agent_published_version>0), binding_version BIGINT NOT NULL CHECK(binding_version>0), sender_set_version BIGINT NOT NULL CHECK(sender_set_version>0),
  target_count BIGINT NOT NULL CHECK(target_count BETWEEN 0 AND 100000), skipped_count BIGINT NOT NULL CHECK(skipped_count BETWEEN 0 AND target_count), preview_digest BYTEA NOT NULL CHECK(octet_length(preview_digest)=32),
  created_by BIGINT NOT NULL CHECK(created_by>0), created_at TIMESTAMPTZ NOT NULL, expires_at TIMESTAMPTZ NOT NULL CHECK(expires_at>created_at), UNIQUE(preview_digest)
);
CREATE TABLE automation_runs (
  id BIGSERIAL PRIMARY KEY, policy_id BIGINT CHECK(policy_id IS NULL OR policy_id>0), policy_version BIGINT CHECK(policy_version IS NULL OR policy_version>0), package_id BIGINT NOT NULL CHECK(package_id>0), package_version BIGINT NOT NULL CHECK(package_version>0), snapshot_id BIGINT NOT NULL CHECK(snapshot_id>0),
  agent_id BIGINT NOT NULL CHECK(agent_id>0), agent_published_version BIGINT NOT NULL CHECK(agent_published_version>0), binding_version BIGINT NOT NULL CHECK(binding_version>0), sender_set_version BIGINT NOT NULL CHECK(sender_set_version>0), preview_digest BYTEA NOT NULL CHECK(octet_length(preview_digest)=32),
  state TEXT NOT NULL CHECK(state IN ('preparing','ready','executing','completed','partial_failed','outcome_unknown','cancelled')), target_count BIGINT NOT NULL CHECK(target_count BETWEEN 0 AND 100000), skipped_count BIGINT NOT NULL CHECK(skipped_count BETWEEN 0 AND target_count),
  created_by BIGINT NOT NULL CHECK(created_by>0), created_at TIMESTAMPTZ NOT NULL, updated_at TIMESTAMPTZ NOT NULL, completed_at TIMESTAMPTZ
);
CREATE TABLE automation_run_recipients (
  id BIGSERIAL PRIMARY KEY, run_id BIGINT NOT NULL REFERENCES automation_runs(id) ON DELETE RESTRICT, customer_id BIGINT NOT NULL CHECK(customer_id>0), sender_staff_id BIGINT NOT NULL CHECK(sender_staff_id>0),
  state TEXT NOT NULL CHECK(state IN ('skipped','accepted','attempted','provider_accepted','delivery_proven','retryable_failed','final_failed','outcome_unknown','reconciled','cancelled')),
  skip_code TEXT CHECK(skip_code IS NULL OR length(skip_code)<=100), effect_id TEXT CHECK(effect_id IS NULL OR length(effect_id)<=200), created_at TIMESTAMPTZ NOT NULL, updated_at TIMESTAMPTZ NOT NULL,
  UNIQUE(run_id,customer_id)
);
CREATE INDEX automation_run_recipients_page_idx ON automation_run_recipients(run_id,id);

CREATE TABLE automation_runtime_operation_receipts (
  id BIGSERIAL PRIMARY KEY, operation TEXT NOT NULL, actor_scope TEXT NOT NULL, key_digest BYTEA NOT NULL CHECK(octet_length(key_digest)=32), payload_digest BYTEA NOT NULL CHECK(octet_length(payload_digest)=32),
  state TEXT NOT NULL CHECK(state IN ('reserved','completed')), result_snapshot JSONB, created_at TIMESTAMPTZ NOT NULL, completed_at TIMESTAMPTZ, UNIQUE(operation,actor_scope,key_digest), CHECK((state='completed')=(completed_at IS NOT NULL AND result_snapshot IS NOT NULL))
);
CREATE TABLE automation_runtime_audit_events (id BIGSERIAL PRIMARY KEY, resource_kind TEXT NOT NULL CHECK(resource_kind IN ('policy','enrollment','preview','run','recipient')), resource_id BIGINT NOT NULL CHECK(resource_id>0), operation TEXT NOT NULL, actor_id BIGINT NOT NULL CHECK(actor_id>0), occurred_at TIMESTAMPTZ NOT NULL, payload_digest BYTEA NOT NULL CHECK(octet_length(payload_digest)=32));
CREATE TABLE automation_runtime_outbox (id BIGSERIAL PRIMARY KEY,event_type TEXT NOT NULL,aggregate_kind TEXT NOT NULL CHECK(aggregate_kind IN ('policy','enrollment','preview','run','recipient')),aggregate_id BIGINT NOT NULL CHECK(aggregate_id>0),payload JSONB NOT NULL CHECK(jsonb_typeof(payload)='object'),idempotency_digest BYTEA NOT NULL CHECK(octet_length(idempotency_digest)=32),occurred_at TIMESTAMPTZ NOT NULL,UNIQUE(event_type,idempotency_digest));
CREATE TRIGGER automation_policy_versions_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON automation_policy_versions FOR EACH STATEMENT EXECUTE FUNCTION automation_append_only();
CREATE TRIGGER automation_enrollments_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON automation_enrollments FOR EACH STATEMENT EXECUTE FUNCTION automation_append_only();
CREATE TRIGGER automation_run_previews_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON automation_run_previews FOR EACH STATEMENT EXECUTE FUNCTION automation_append_only();
CREATE TRIGGER automation_runtime_audit_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON automation_runtime_audit_events FOR EACH STATEMENT EXECUTE FUNCTION automation_append_only();
CREATE TRIGGER automation_runtime_outbox_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON automation_runtime_outbox FOR EACH STATEMENT EXECUTE FUNCTION automation_append_only();
