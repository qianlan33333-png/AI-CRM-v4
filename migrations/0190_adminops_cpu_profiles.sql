-- Owner: AdminOps. Minimal permanent command/audit receipts, never profile bytes.
-- Raw diagnostic artifacts live only in the registered 720-hour process root.
CREATE TABLE adminops_cpu_profile_receipts (
 id TEXT PRIMARY KEY CHECK(id ~ '^[a-f0-9]{32}$'),
 request_digest TEXT NOT NULL UNIQUE CHECK(request_digest ~ '^[a-f0-9]{64}$'),
 actor_id BIGINT NOT NULL CHECK(actor_id>0),
 release_sha TEXT NOT NULL CHECK(release_sha ~ '^[a-f0-9]{40}$' OR release_sha='unknown'),
 state TEXT NOT NULL CHECK(state IN ('sampling','completed','failed')),
 failure_code TEXT NOT NULL DEFAULT '' CHECK(failure_code IN ('','capture_failed','profile_busy','profile_too_large','profile_capacity')),
 artifact_sha256 TEXT NOT NULL DEFAULT '' CHECK(artifact_sha256='' OR artifact_sha256 ~ '^[a-f0-9]{64}$'),
 artifact_bytes BIGINT NOT NULL DEFAULT 0 CHECK(artifact_bytes BETWEEN 0 AND 2097152),
 accepted_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 expires_at TIMESTAMPTZ NOT NULL,
 completed_at TIMESTAMPTZ,
 CHECK(expires_at=accepted_at+interval '720 hours'),
 CHECK((state='sampling' AND completed_at IS NULL AND artifact_bytes=0 AND artifact_sha256='' AND failure_code='')
    OR (state='completed' AND completed_at IS NOT NULL AND artifact_bytes>0 AND artifact_sha256<>'' AND failure_code='')
    OR (state='failed' AND completed_at IS NOT NULL AND artifact_bytes=0 AND artifact_sha256='' AND failure_code<>''))
);
CREATE INDEX adminops_cpu_profiles_recent ON adminops_cpu_profile_receipts(accepted_at DESC,id);
CREATE FUNCTION adminops_cpu_profile_receipt_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP IN ('DELETE','TRUNCATE') THEN RAISE EXCEPTION 'CPU profile receipt is permanent'; END IF;
 IF OLD.state<>'sampling' OR NEW.state NOT IN ('completed','failed') OR
 ROW(NEW.id,NEW.request_digest,NEW.actor_id,NEW.release_sha,NEW.accepted_at,NEW.expires_at)
 IS DISTINCT FROM ROW(OLD.id,OLD.request_digest,OLD.actor_id,OLD.release_sha,OLD.accepted_at,OLD.expires_at)
 THEN RAISE EXCEPTION 'CPU profile receipt identity and terminal result are immutable'; END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER adminops_cpu_profile_receipt_immutable BEFORE UPDATE OR DELETE ON adminops_cpu_profile_receipts
FOR EACH ROW EXECUTE FUNCTION adminops_cpu_profile_receipt_guard();
CREATE TRIGGER adminops_cpu_profile_receipt_no_truncate BEFORE TRUNCATE ON adminops_cpu_profile_receipts
FOR EACH STATEMENT EXECUTE FUNCTION adminops_cpu_profile_receipt_guard();
