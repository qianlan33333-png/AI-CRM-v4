-- Owner: AdminOps. Permanent minimal governance facts, not diagnostic payloads.
CREATE TABLE adminops_governance_releases (
 sequence BIGINT PRIMARY KEY CHECK(sequence>0),
 release_sha TEXT NOT NULL CHECK(release_sha ~ '^[a-f0-9]{40}$'),
 succeeded_at TIMESTAMPTZ NOT NULL,
 receipt_digest TEXT NOT NULL CHECK(receipt_digest ~ '^[a-f0-9]{64}$'),
 revoked BOOLEAN NOT NULL DEFAULT false,
 recorded_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX adminops_governance_releases_time ON adminops_governance_releases(succeeded_at,sequence) WHERE NOT revoked;
CREATE TABLE adminops_incident_episodes (
 id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 issue_id BIGINT NOT NULL REFERENCES adminops_inspection_issues(id),
 fingerprint TEXT NOT NULL CHECK(fingerprint ~ '^sha256:[a-f0-9]{64}$'),
 check_id TEXT NOT NULL CHECK(check_id ~ '^[a-z][a-z0-9_.-]{0,95}$'),
 code TEXT NOT NULL CHECK(code ~ '^[a-z][a-z0-9_.-]{0,95}$'),
 detected_release TEXT NOT NULL CHECK(detected_release ~ '^[a-f0-9]{40}$' OR detected_release='unknown'),
 detection_origin TEXT NOT NULL CHECK(detection_origin IN ('new_observation','first_observed_existing_issue')),
 initial_status TEXT NOT NULL CHECK(initial_status IN ('warning','critical','unknown','uncovered','stale')),
 latest_status TEXT NOT NULL CHECK(latest_status IN ('warning','critical','unknown','uncovered','stale','ok')),
 detected_at TIMESTAMPTZ NOT NULL,
 last_observed_at TIMESTAMPTZ NOT NULL,
 recovered_at TIMESTAMPTZ,
 version BIGINT NOT NULL DEFAULT 1 CHECK(version>0),
 classification TEXT NOT NULL DEFAULT 'unclassified' CHECK(classification IN ('unclassified','confirmed_defect','expected_rejection','legacy_backlog','observation_gap','non_defect')),
 escaped_defect TEXT NOT NULL DEFAULT 'unclassified' CHECK(escaped_defect IN ('unclassified','yes','no')),
 change_failure TEXT NOT NULL DEFAULT 'unclassified' CHECK(change_failure IN ('unclassified','yes','no')),
 caused_by_release_sequence BIGINT REFERENCES adminops_governance_releases(sequence),
 root_cause TEXT NOT NULL DEFAULT 'unknown' CHECK(root_cause IN ('unknown','code','configuration','dependency','data','infrastructure','expected_behavior','instrumentation')),
 remediation TEXT NOT NULL DEFAULT 'unknown' CHECK(remediation IN ('unknown','code_fix','configuration_fix','rollback','hotfix','data_reconciliation','dependency_recovery','no_action')),
 fault_started_at TIMESTAMPTZ,
 fault_start_basis TEXT NOT NULL DEFAULT 'unknown' CHECK(fault_start_basis IN ('unknown','monitor_evidence','provider_receipt','operator_confirmed')),
 effort_minutes INTEGER CHECK(effort_minutes BETWEEN 0 AND 525600),
 attributed_at TIMESTAMPTZ,
 CHECK(last_observed_at>=detected_at),
 CHECK(recovered_at IS NULL OR recovered_at>=last_observed_at),
 CHECK((recovered_at IS NULL AND latest_status<>'ok') OR (recovered_at IS NOT NULL AND latest_status='ok')),
 CHECK((fault_started_at IS NULL AND fault_start_basis='unknown') OR (fault_started_at IS NOT NULL AND fault_start_basis<>'unknown' AND fault_started_at<=detected_at)),
 CHECK(classification='confirmed_defect' OR (escaped_defect='unclassified' AND change_failure='unclassified' AND fault_started_at IS NULL)),
 CHECK((change_failure='yes' AND caused_by_release_sequence IS NOT NULL) OR (change_failure<>'yes' AND caused_by_release_sequence IS NULL))
);
CREATE UNIQUE INDEX adminops_incident_one_open ON adminops_incident_episodes(issue_id) WHERE recovered_at IS NULL;
CREATE INDEX adminops_incident_cohort ON adminops_incident_episodes(detected_at,id);
CREATE INDEX adminops_incident_recurrence ON adminops_incident_episodes(fingerprint,detected_at) WHERE classification='confirmed_defect';
CREATE TABLE adminops_incident_attributions (
 id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 episode_id BIGINT NOT NULL REFERENCES adminops_incident_episodes(id),
 actor_id BIGINT NOT NULL CHECK(actor_id>0),
 request_digest TEXT NOT NULL UNIQUE CHECK(request_digest ~ '^sha256:[a-f0-9]{64}$'),
 payload_digest TEXT NOT NULL CHECK(payload_digest ~ '^sha256:[a-f0-9]{64}$'),
 expected_version BIGINT NOT NULL CHECK(expected_version>0),
 result_version BIGINT NOT NULL CHECK(result_version=expected_version+1),
 attribution JSONB NOT NULL CHECK(jsonb_typeof(attribution)='object'),
 created_at TIMESTAMPTZ NOT NULL
);
CREATE FUNCTION adminops_governance_fact_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP IN ('DELETE','TRUNCATE') THEN RAISE EXCEPTION 'governance facts are permanent'; END IF;
 IF TG_TABLE_NAME='adminops_incident_attributions' THEN RAISE EXCEPTION 'governance audit is immutable'; END IF;
 IF TG_TABLE_NAME='adminops_governance_releases' THEN
  IF OLD.revoked OR NOT NEW.revoked OR ROW(NEW.sequence,NEW.release_sha,NEW.succeeded_at,NEW.receipt_digest,NEW.recorded_at)
    IS DISTINCT FROM ROW(OLD.sequence,OLD.release_sha,OLD.succeeded_at,OLD.receipt_digest,OLD.recorded_at)
  THEN RAISE EXCEPTION 'release facts only permit authoritative revocation'; END IF;
 ELSE
  IF ROW(NEW.id,NEW.issue_id,NEW.fingerprint,NEW.check_id,NEW.code,NEW.detected_release,NEW.detection_origin,NEW.initial_status,NEW.detected_at)
    IS DISTINCT FROM ROW(OLD.id,OLD.issue_id,OLD.fingerprint,OLD.check_id,OLD.code,OLD.detected_release,OLD.detection_origin,OLD.initial_status,OLD.detected_at)
    OR NEW.last_observed_at<OLD.last_observed_at OR (OLD.recovered_at IS NOT NULL AND ROW(NEW.recovered_at,NEW.last_observed_at,NEW.latest_status) IS DISTINCT FROM ROW(OLD.recovered_at,OLD.last_observed_at,OLD.latest_status))
    OR NEW.version<>OLD.version+1
  THEN RAISE EXCEPTION 'incident observed identity and recovery are immutable'; END IF;
 END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER adminops_incident_guard BEFORE UPDATE OR DELETE ON adminops_incident_episodes FOR EACH ROW EXECUTE FUNCTION adminops_governance_fact_guard();
CREATE TRIGGER adminops_incident_no_truncate BEFORE TRUNCATE ON adminops_incident_episodes FOR EACH STATEMENT EXECUTE FUNCTION adminops_governance_fact_guard();
CREATE TRIGGER adminops_attribution_guard BEFORE UPDATE OR DELETE ON adminops_incident_attributions FOR EACH ROW EXECUTE FUNCTION adminops_governance_fact_guard();
CREATE TRIGGER adminops_attribution_no_truncate BEFORE TRUNCATE ON adminops_incident_attributions FOR EACH STATEMENT EXECUTE FUNCTION adminops_governance_fact_guard();
CREATE TRIGGER adminops_release_fact_guard BEFORE UPDATE OR DELETE ON adminops_governance_releases FOR EACH ROW EXECUTE FUNCTION adminops_governance_fact_guard();
CREATE TRIGGER adminops_release_fact_no_truncate BEFORE TRUNCATE ON adminops_governance_releases FOR EACH STATEMENT EXECUTE FUNCTION adminops_governance_fact_guard();
