-- Config owns the closed runtime setting catalog. Invoice owns frozen card
-- material, while execution/attempt/queue state stays in External Effects.
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
        'wechat_invoice.enabled', 'wechat_invoice.service_merchant_id', 'wechat_invoice.merchant_serial',
        'wechat_invoice.seller_name', 'wechat_invoice.seller_tax_id', 'wechat_invoice.billing_person_id',
        'wechat_invoice.default_tax_code', 'wechat_invoice.default_tax_rate', 'wechat_invoice.default_goods_name',
        'wechat_invoice.key_file_id', 'wechat_invoice.logo_url', 'wechat_invoice.card_confirmed',
        'alipay.provider_enabled', 'alipay.app_id', 'alipay.production',
        'survey.oauth_enabled', 'survey.oauth_app_id', 'survey.oauth_open_platform_id', 'survey.oauth_scope'
    ));

ALTER TABLE external_effects DROP CONSTRAINT IF EXISTS external_effects_owner_kind_shape;
ALTER TABLE external_effects DROP CONSTRAINT IF EXISTS external_effects_kind_check;
ALTER TABLE external_effects ADD CONSTRAINT external_effects_kind_check CHECK (kind IN (
  'outbound_message','automation_message','outbound_media','wecom_tag_catalog','wecom_tag_catalog_mutation','wecom_contact_description','group_message',
  'channel_acquisition_asset','channel_welcome_message','channel_entry_tag',
  'group_invitation_code','channel_acquisition_link_mutation','sidebar_jssdk_send','survey_completion',
  'customer_owner_handoff','customer_tag_command','commerce_product_push','ai_agent_generate','ai_audience_recommend',
  'wechat_pay_prepay_v1','wechat_pay_refund_v1','wechat_shop_refund_v1','alipay_wap_pay_v1','alipay_page_pay_v1','alipay_refund_v1',
  'wechat_pay_profit_sharing_receiver_v1','wechat_pay_profit_sharing_order_v1','wechat_pay_profit_sharing_unfreeze_v1',
  'feishu_ops_notification_v1','wechat_invoice_issue_v1','wechat_invoice_reverse_v1','wechat_invoice_card_configure_v1'
));
ALTER TABLE external_effects ADD CONSTRAINT external_effects_owner_kind_shape CHECK (
  (owner='outbound' AND kind IN (
    'outbound_message','automation_message','outbound_media','wecom_tag_catalog','wecom_tag_catalog_mutation','wecom_contact_description','group_message',
    'channel_acquisition_asset','channel_welcome_message','channel_entry_tag',
    'group_invitation_code','channel_acquisition_link_mutation','sidebar_jssdk_send','survey_completion',
    'customer_owner_handoff','customer_tag_command','commerce_product_push'
  )) OR
  (owner='payment' AND kind IN (
    'wechat_pay_prepay_v1','wechat_pay_refund_v1','wechat_shop_refund_v1','alipay_wap_pay_v1','alipay_page_pay_v1','alipay_refund_v1',
    'wechat_pay_profit_sharing_receiver_v1','wechat_pay_profit_sharing_order_v1','wechat_pay_profit_sharing_unfreeze_v1'
  )) OR
  (owner='invoice' AND kind IN ('wechat_invoice_issue_v1','wechat_invoice_reverse_v1','wechat_invoice_card_configure_v1')) OR
  (owner='automation' AND kind='ai_agent_generate') OR (owner='adminops' AND kind='feishu_ops_notification_v1') OR (owner='segment' AND kind='ai_audience_recommend')
);


-- This is immutable setup material, not a queue or an independent state machine.
CREATE TABLE invoice_card_setup_intents (
 id BIGSERIAL PRIMARY KEY, merchant_id TEXT NOT NULL,
 effect_id TEXT NOT NULL UNIQUE, payload_digest TEXT NOT NULL,
 material JSONB NOT NULL CHECK(jsonb_typeof(material)='object'),
 actor_id BIGINT NOT NULL CHECK(actor_id>0),created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 card_id TEXT NOT NULL DEFAULT '',failure_code TEXT NOT NULL DEFAULT ''
);
CREATE INDEX invoice_card_setup_merchant ON invoice_card_setup_intents(merchant_id,id DESC);
