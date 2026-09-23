-- Owner: AdminOps. Raw observations are operational; issue decisions and
-- immutable notification acceptance remain durable reliability evidence.
CREATE TABLE adminops_inspection_runs (
 id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 request_key TEXT NOT NULL UNIQUE,
 slot TIMESTAMPTZ NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('running','completed','partial_failed')),
 release_sha TEXT NOT NULL,
 started_at TIMESTAMPTZ NOT NULL,
 completed_at TIMESTAMPTZ
);
CREATE INDEX adminops_inspection_runs_recent ON adminops_inspection_runs(started_at DESC,id DESC);
CREATE TABLE adminops_inspection_results (
 run_id BIGINT NOT NULL REFERENCES adminops_inspection_runs(id) ON DELETE RESTRICT,
 check_id TEXT NOT NULL,
 owner TEXT NOT NULL,
 status TEXT NOT NULL CHECK(status IN ('ok','warning','critical','unknown','uncovered','stale')),
 code TEXT NOT NULL,
 observed_at TIMESTAMPTZ NOT NULL,
 metrics JSONB NOT NULL CHECK(jsonb_typeof(metrics)='object'),
 PRIMARY KEY(run_id,check_id)
);
CREATE INDEX adminops_inspection_results_retention ON adminops_inspection_results(observed_at,run_id,check_id);
CREATE TABLE adminops_inspection_issues (
 id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 fingerprint TEXT NOT NULL UNIQUE,
 check_id TEXT NOT NULL,
 code TEXT NOT NULL,
 status TEXT NOT NULL CHECK(status IN ('open','acknowledged','resolved')),
 severity TEXT NOT NULL CHECK(severity IN ('warning','critical','unknown','uncovered','stale')),
 version BIGINT NOT NULL DEFAULT 1,
 first_seen TIMESTAMPTZ NOT NULL,
 last_seen TIMESTAMPTZ NOT NULL,
 resolved_at TIMESTAMPTZ,
 critical_active BOOLEAN NOT NULL DEFAULT false,
 occurrences BIGINT NOT NULL DEFAULT 1
);
CREATE TABLE adminops_inspection_issue_actions (
 id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 issue_id BIGINT NOT NULL REFERENCES adminops_inspection_issues(id),
 request_key TEXT NOT NULL UNIQUE,
 actor_id BIGINT NOT NULL CHECK(actor_id>0),
 expected_version BIGINT NOT NULL,
 new_status TEXT NOT NULL CHECK(new_status IN ('open','acknowledged')),
 created_at TIMESTAMPTZ NOT NULL
);
CREATE TABLE adminops_inspection_reports (
 id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 hour_key TIMESTAMPTZ NOT NULL,
 notification_kind TEXT NOT NULL CHECK(notification_kind IN ('hourly','critical','recovery')),
 event_key TEXT NOT NULL UNIQUE,
 target_ref TEXT NOT NULL,
 source_digest TEXT NOT NULL UNIQUE,
 target_digest TEXT NOT NULL,
 payload_digest TEXT NOT NULL,
 policy_digest TEXT NOT NULL,
 content JSONB,
 content_bytes BYTEA,
 payload_pruned_at TIMESTAMPTZ,
 effect_id TEXT UNIQUE,
 effect_state TEXT NOT NULL,
 created_at TIMESTAMPTZ NOT NULL,
 updated_at TIMESTAMPTZ NOT NULL,
 CHECK ((content IS NOT NULL AND content_bytes IS NOT NULL AND payload_pruned_at IS NULL)
     OR (content IS NULL AND content_bytes IS NULL AND payload_pruned_at IS NOT NULL))
);
CREATE UNIQUE INDEX adminops_hourly_report_key ON adminops_inspection_reports(hour_key) WHERE notification_kind='hourly';
CREATE TABLE adminops_diagnostic_events (
 id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 component TEXT NOT NULL,
 release_sha TEXT NOT NULL,
 code TEXT NOT NULL,
 correlation_digest TEXT NOT NULL,
 route_template TEXT NOT NULL DEFAULT '',
 job_ref TEXT NOT NULL DEFAULT '',
 effect_ref TEXT NOT NULL DEFAULT '',
 occurred_at TIMESTAMPTZ NOT NULL,
 UNIQUE(component,code,correlation_digest)
);
CREATE INDEX adminops_diagnostic_events_recent ON adminops_diagnostic_events(occurred_at DESC,id DESC);
CREATE FUNCTION adminops_report_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP IN ('DELETE','TRUNCATE') THEN RAISE EXCEPTION 'report acceptance is immutable'; END IF;
 IF ROW(NEW.notification_kind,NEW.event_key,NEW.hour_key,NEW.target_ref,NEW.source_digest,NEW.target_digest,NEW.payload_digest,NEW.policy_digest,NEW.created_at)
 IS DISTINCT FROM ROW(OLD.notification_kind,OLD.event_key,OLD.hour_key,OLD.target_ref,OLD.source_digest,OLD.target_digest,OLD.payload_digest,OLD.policy_digest,OLD.created_at)
 OR (OLD.effect_id IS NOT NULL AND NEW.effect_id IS DISTINCT FROM OLD.effect_id)
 THEN RAISE EXCEPTION 'report identity and effect binding are immutable'; END IF;
 IF ROW(NEW.content,NEW.content_bytes,NEW.payload_pruned_at) IS DISTINCT FROM ROW(OLD.content,OLD.content_bytes,OLD.payload_pruned_at)
 AND NOT (OLD.payload_pruned_at IS NULL AND OLD.content IS NOT NULL AND OLD.content_bytes IS NOT NULL
   AND NEW.content IS NULL AND NEW.content_bytes IS NULL AND NEW.payload_pruned_at IS NOT NULL
   AND OLD.created_at <= clock_timestamp()-interval '720 hours'
   AND NEW.payload_pruned_at >= OLD.created_at+interval '720 hours')
 THEN RAISE EXCEPTION 'report payload can only expire once after 720 hours'; END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER adminops_report_immutable BEFORE UPDATE OR DELETE ON adminops_inspection_reports FOR EACH ROW EXECUTE FUNCTION adminops_report_guard();
CREATE TRIGGER adminops_report_no_truncate BEFORE TRUNCATE ON adminops_inspection_reports FOR EACH STATEMENT EXECUTE FUNCTION adminops_report_guard();

CREATE FUNCTION adminops_issue_actions_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'issue actions are immutable'; END; $$;
CREATE TRIGGER adminops_issue_actions_guard BEFORE UPDATE OR DELETE OR TRUNCATE ON adminops_inspection_issue_actions FOR EACH STATEMENT EXECUTE FUNCTION adminops_issue_actions_immutable();

-- Permanent acceptance/audit evidence: River jobs and inspection details may
-- be cleaned after 720h, but an old manual command must never execute again.
-- IDs deliberately have no foreign keys to those disposable process records.
CREATE TABLE adminops_inspection_commands (
 request_digest TEXT PRIMARY KEY CHECK(request_digest ~ '^sha256:[a-f0-9]{64}$'),
 actor_id BIGINT NOT NULL CHECK(actor_id>0),
 job_id BIGINT NOT NULL UNIQUE CHECK(job_id>0),
 accepted_at TIMESTAMPTZ NOT NULL,
 run_id BIGINT CHECK(run_id>0),
 completed_at TIMESTAMPTZ,
 CHECK((run_id IS NULL)=(completed_at IS NULL))
);
CREATE INDEX adminops_inspection_commands_hour ON adminops_inspection_commands(accepted_at,actor_id);
CREATE FUNCTION adminops_inspection_command_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP <> 'UPDATE' THEN RAISE EXCEPTION 'inspection command receipts are permanent'; END IF;
 IF (NEW.request_digest,NEW.actor_id,NEW.job_id,NEW.accepted_at) IS DISTINCT FROM
    (OLD.request_digest,OLD.actor_id,OLD.job_id,OLD.accepted_at)
    OR (OLD.completed_at IS NOT NULL AND (NEW.run_id,NEW.completed_at) IS DISTINCT FROM (OLD.run_id,OLD.completed_at))
    OR (NEW.completed_at IS NOT NULL AND NEW.completed_at < NEW.accepted_at) THEN
   RAISE EXCEPTION 'inspection command acceptance is immutable';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER adminops_inspection_command_immutable BEFORE UPDATE OR DELETE ON adminops_inspection_commands
 FOR EACH ROW EXECUTE FUNCTION adminops_inspection_command_guard();
CREATE TRIGGER adminops_inspection_command_no_truncate BEFORE TRUNCATE ON adminops_inspection_commands
 FOR EACH STATEMENT EXECUTE FUNCTION adminops_inspection_command_guard();
