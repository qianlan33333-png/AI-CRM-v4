-- Register Alipay WAP/page-pay and refund effects and provider facts.
ALTER TABLE payment_provider_intents DROP CONSTRAINT IF EXISTS payment_provider_intents_effect_kind_check;
ALTER TABLE payment_provider_intents ADD CONSTRAINT payment_provider_intents_effect_kind_check CHECK(effect_kind IN ('wechat_pay_prepay_v1','wechat_pay_refund_v1','wechat_shop_refund_v1','alipay_wap_pay_v1','alipay_page_pay_v1','alipay_refund_v1'));

ALTER TABLE payment_callback_receipts DROP CONSTRAINT IF EXISTS payment_callback_receipts_provider_check;
ALTER TABLE payment_callback_receipts ADD CONSTRAINT payment_callback_receipts_provider_check CHECK(provider IN ('wechat_pay','wechat_shop','alipay'));

ALTER TABLE external_effects DROP CONSTRAINT IF EXISTS external_effects_owner_kind_shape;
ALTER TABLE external_effects DROP CONSTRAINT IF EXISTS external_effects_kind_check;
ALTER TABLE external_effects ADD CONSTRAINT external_effects_kind_check CHECK (kind IN (
  'outbound_message','automation_message','outbound_media','wecom_tag_catalog','wecom_tag_catalog_mutation','wecom_contact_description','group_message',
  'channel_acquisition_asset','channel_welcome_message','channel_entry_tag','group_invitation_code','channel_acquisition_link_mutation','sidebar_jssdk_send','survey_completion',
  'customer_owner_handoff','customer_tag_command','commerce_product_push','ai_agent_generate','ai_audience_recommend','feishu_ops_notification_v1',
  'wechat_pay_prepay_v1','wechat_pay_refund_v1','wechat_shop_refund_v1','alipay_wap_pay_v1','alipay_page_pay_v1','alipay_refund_v1',
  'wechat_pay_profit_sharing_receiver_v1','wechat_pay_profit_sharing_order_v1','wechat_pay_profit_sharing_unfreeze_v1'
));
ALTER TABLE external_effects ADD CONSTRAINT external_effects_owner_kind_shape CHECK (
  (owner='outbound' AND kind IN ('outbound_message','automation_message','outbound_media','wecom_tag_catalog','wecom_tag_catalog_mutation','wecom_contact_description','group_message','channel_acquisition_asset','channel_welcome_message','channel_entry_tag','group_invitation_code','channel_acquisition_link_mutation','sidebar_jssdk_send','survey_completion','customer_owner_handoff','customer_tag_command','commerce_product_push')) OR
  (owner='payment' AND kind IN ('wechat_pay_prepay_v1','wechat_pay_refund_v1','wechat_shop_refund_v1','alipay_wap_pay_v1','alipay_page_pay_v1','alipay_refund_v1','wechat_pay_profit_sharing_receiver_v1','wechat_pay_profit_sharing_order_v1','wechat_pay_profit_sharing_unfreeze_v1')) OR
  (owner='automation' AND kind='ai_agent_generate') OR (owner='adminops' AND kind='feishu_ops_notification_v1') OR (owner='segment' AND kind='ai_audience_recommend')
);
