-- Owner: internal/config. Extend the existing runtime snapshot allowlist.
-- This stores only the generation switch; no credentials or Provider calls.
ALTER TABLE config_runtime_release_values
    DROP CONSTRAINT config_runtime_release_values_setting_key_check;
ALTER TABLE config_runtime_release_values
    ADD CONSTRAINT config_runtime_release_values_setting_key_check CHECK (setting_key IN (
        'automation.operations.max_recipients_per_run', 'automation.operations.provider_mode',
        'ai_assistant.ui_enabled', 'ai_assistant.intake_enabled', 'ai_assistant.dispatch_enabled', 'ai_agent_generation.enabled',
        'wecom.enabled', 'wecom.corp_id', 'wecom.agent_id', 'wecom.callback_enabled', 'wecom.customer_sync_enabled',
        'message_archive.enabled', 'message_archive.page_limit', 'message_archive.page_budget',
        'sidebar.context_token_ttl_seconds', 'groupops.directory_read_enabled', 'groupops.dispatch_enabled',
        'effects.provider_enabled', 'survey.completion_provider_enabled', 'commerce.push.provider_enabled',
        'stability.worker_limit', 'wechat_pay.provider_enabled', 'wechat_pay.app_id', 'wechat_pay.app_scope',
        'wechat_pay.h5_oauth_enabled', 'wechat_pay.h5_app_id', 'wechat_pay.h5_app_scope',
        'wechat_pay.merchant_id', 'wechat_pay.merchant_serial', 'wechat_shop.provider_enabled', 'wechat_shop.app_id',
        'survey.oauth_enabled', 'survey.oauth_app_id', 'survey.oauth_open_platform_id', 'survey.oauth_scope'
    ));
