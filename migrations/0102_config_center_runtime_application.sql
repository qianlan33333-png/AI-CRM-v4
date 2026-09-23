-- Owner: internal/config.  Extends the existing immutable runtime-release
-- catalog; no secret material or deployment environment value is stored.

ALTER TABLE config_runtime_release_values
    DROP CONSTRAINT config_runtime_release_values_setting_key_check;
-- The compatibility recovery is a single, audited Config command. It creates
-- no deployment value and exists only to prepare the prior binary's one-key
-- runtime catalog before that binary is started.
ALTER TABLE config_runtime_release_command_receipts
    DROP CONSTRAINT config_runtime_release_command_receipts_action_check;
ALTER TABLE config_runtime_release_command_receipts
    ADD CONSTRAINT config_runtime_release_command_receipts_action_check CHECK (action IN (
        'runtime_release.create', 'runtime_release.validate', 'runtime_release.publish',
        'runtime_release.rollback', 'runtime_release.legacy_binary_recovery'
    ));
ALTER TABLE config_runtime_release_audits
    DROP CONSTRAINT config_runtime_release_audits_action_check;
ALTER TABLE config_runtime_release_audits
    ADD CONSTRAINT config_runtime_release_audits_action_check CHECK (action IN (
        'created', 'validated', 'validation_failed', 'published', 'superseded',
        'rolled_back', 'legacy_binary_recovery_published'
    ));
ALTER TABLE config_outbox
    DROP CONSTRAINT config_outbox_event_type_check;
ALTER TABLE config_outbox
    ADD CONSTRAINT config_outbox_event_type_check CHECK (event_type IN (
        'setting.updated', 'runtime_release.published', 'runtime_release.rolled_back',
        'runtime_release.legacy_binary_recovery_published'
    ));

ALTER TABLE config_runtime_release_values
    ADD CONSTRAINT config_runtime_release_values_setting_key_check CHECK (setting_key IN (
        'automation.operations.max_recipients_per_run', 'automation.operations.provider_mode',
        'ai_assistant.ui_enabled', 'ai_assistant.intake_enabled', 'ai_assistant.dispatch_enabled',
        'wecom.enabled', 'wecom.corp_id', 'wecom.agent_id', 'wecom.callback_enabled', 'wecom.customer_sync_enabled',
        'message_archive.enabled', 'message_archive.page_limit', 'message_archive.page_budget',
        'sidebar.context_token_ttl_seconds', 'groupops.directory_read_enabled', 'groupops.dispatch_enabled',
        'effects.provider_enabled', 'survey.completion_provider_enabled', 'commerce.push.provider_enabled',
        'stability.worker_limit', 'wechat_pay.provider_enabled', 'wechat_pay.app_id', 'wechat_pay.app_scope',
        'wechat_pay.h5_oauth_enabled', 'wechat_pay.h5_app_id', 'wechat_pay.h5_app_scope',
        'wechat_pay.merchant_id', 'wechat_pay.merchant_serial', 'wechat_shop.provider_enabled', 'wechat_shop.app_id',
        'survey.oauth_enabled', 'survey.oauth_app_id', 'survey.oauth_open_platform_id', 'survey.oauth_scope'
    ));

-- A role writes this fact only after it read the immutable active snapshot at
-- composition time.  It proves that exact role/revision/sha, not that a
-- Provider accepted or delivered any external effect.
CREATE TABLE config_runtime_applications (
    revision BIGINT NOT NULL CHECK(revision >= 0),
    source TEXT NOT NULL CHECK(source IN ('environment_default','published')),
    role TEXT NOT NULL CHECK(role IN ('api','worker','effects-worker')),
    release_sha TEXT NOT NULL CHECK(length(release_sha) BETWEEN 1 AND 200),
    snapshot_checksum TEXT NOT NULL CHECK(length(snapshot_checksum) = 64),
    applied_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY(revision, source, role, release_sha, snapshot_checksum)
);
CREATE INDEX config_runtime_applications_current_idx ON config_runtime_applications(revision, role, applied_at DESC);

CREATE FUNCTION config_runtime_applications_reject_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'config_runtime_applications are append-only'; END;
$$;
CREATE TRIGGER config_runtime_applications_guard BEFORE UPDATE ON config_runtime_applications
FOR EACH ROW EXECUTE FUNCTION config_runtime_applications_reject_mutation();
CREATE FUNCTION config_runtime_applications_reject_delete() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'config_runtime_applications are append-only'; END;
$$;
CREATE TRIGGER config_runtime_applications_append_only BEFORE DELETE OR TRUNCATE ON config_runtime_applications
FOR EACH STATEMENT EXECUTE FUNCTION config_runtime_applications_reject_delete();
