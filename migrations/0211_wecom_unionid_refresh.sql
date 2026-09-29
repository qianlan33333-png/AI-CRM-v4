-- Owner: WeCom. Reuse the full directory's resumable run and item receipts for
-- a read-only UnionID refresh. This trigger must never submit Outbound intents.
ALTER TABLE wecom_customer_sync_runs DROP CONSTRAINT wecom_customer_sync_runs_trigger_type_check;
ALTER TABLE wecom_customer_sync_runs ADD CONSTRAINT wecom_customer_sync_runs_trigger_type_check
    CHECK (trigger_type IN ('initial','daily','manual','tag_refresh','unionid_refresh'));
