-- Owners: internal/config and internal/messagearchive, independently.
-- Only classified process observations become eligible after 720 hours.
-- Runtime releases/receipts/audits and archive messages/msgid/cursor remain
-- permanent. No business table is rewritten and no trigger is disabled.

CREATE INDEX config_runtime_usage_used_at_id_idx
    ON config_runtime_usage(used_at,id);
CREATE INDEX message_archive_sync_runs_finished_retention_idx
    ON message_archive_sync_runs(finished_at,id)
    WHERE status IN ('succeeded','failed') AND finished_at IS NOT NULL;

-- Preserve the original statement-level protection for UPDATE (including
-- no-op updates) and TRUNCATE. Replace DELETE's unconditional prohibition
-- with an age guard for every affected row in the same migration transaction.
DROP TRIGGER config_runtime_usage_append_only ON config_runtime_usage;
CREATE TRIGGER config_runtime_usage_append_only
    BEFORE UPDATE OR TRUNCATE ON config_runtime_usage
    FOR EACH STATEMENT EXECUTE FUNCTION config_runtime_usage_reject_mutation();

CREATE FUNCTION config_runtime_usage_guard_expired_delete()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.used_at >= statement_timestamp()-interval '720 hours' THEN
        RAISE EXCEPTION 'config_runtime_usage within 720 hours is protected';
    END IF;
    RETURN OLD;
END;
$$;
CREATE TRIGGER config_runtime_usage_expired_delete
    BEFORE DELETE ON config_runtime_usage
    FOR EACH ROW EXECUTE FUNCTION config_runtime_usage_guard_expired_delete();
