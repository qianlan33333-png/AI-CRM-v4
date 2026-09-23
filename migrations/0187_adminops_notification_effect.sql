-- Owner: External Effects. AdminOps freezes hourly report payloads, EER owns delivery.
ALTER TABLE external_effects DROP CONSTRAINT external_effects_owner_check;
ALTER TABLE external_effects ADD CONSTRAINT external_effects_owner_check CHECK(owner IN ('outbound','payment','automation','segment','adminops'));
ALTER TABLE external_effects DROP CONSTRAINT IF EXISTS external_effects_owner_kind_shape;
ALTER TABLE external_effects DROP CONSTRAINT IF EXISTS external_effects_kind_check;
ALTER TABLE external_effects ADD CONSTRAINT external_effects_kind_check CHECK (kind IN (
  'outbound_message','automation_message','outbound_media','wecom_tag_catalog','wecom_tag_catalog_mutation','wecom_contact_description','group_message',
  'channel_acquisition_asset','channel_welcome_message','channel_entry_tag',
  'channel_acquisition_link_mutation','sidebar_jssdk_send','survey_completion',
  'customer_owner_handoff','customer_tag_command','commerce_product_push','ai_agent_generate','ai_audience_recommend','feishu_ops_notification_v1',
  'wechat_pay_prepay_v1','wechat_pay_refund_v1','wechat_shop_refund_v1',
  'wechat_pay_profit_sharing_receiver_v1','wechat_pay_profit_sharing_order_v1','wechat_pay_profit_sharing_unfreeze_v1'
));
ALTER TABLE external_effects ADD CONSTRAINT external_effects_owner_kind_shape CHECK (
  (owner='outbound' AND kind IN (
    'outbound_message','automation_message','outbound_media','wecom_tag_catalog','wecom_tag_catalog_mutation','wecom_contact_description','group_message',
    'channel_acquisition_asset','channel_welcome_message','channel_entry_tag',
    'channel_acquisition_link_mutation','sidebar_jssdk_send','survey_completion',
    'customer_owner_handoff','customer_tag_command','commerce_product_push'
  )) OR
  (owner='payment' AND kind IN (
    'wechat_pay_prepay_v1','wechat_pay_refund_v1','wechat_shop_refund_v1',
    'wechat_pay_profit_sharing_receiver_v1','wechat_pay_profit_sharing_order_v1','wechat_pay_profit_sharing_unfreeze_v1'
  )) OR
  (owner='automation' AND kind='ai_agent_generate') OR
  (owner='segment' AND kind='ai_audience_recommend') OR
  (owner='adminops' AND kind='feishu_ops_notification_v1')
);


ALTER TABLE external_effect_jobs DROP CONSTRAINT external_effect_jobs_queue_check;
ALTER TABLE external_effect_jobs ADD CONSTRAINT external_effect_jobs_queue_check CHECK(queue IN ('outbound','outbound_welcome','outbound_excel','outbound_media','ops_notification'));
ALTER TABLE external_effects DROP CONSTRAINT external_effects_delivery_lane_check;
ALTER TABLE external_effects ADD CONSTRAINT external_effects_delivery_lane_check CHECK(delivery_lane IN ('','outbound_excel','outbound_media','ops_notification'));
ALTER TABLE external_effects ADD CONSTRAINT external_effects_ops_lane_shape CHECK((owner='adminops' AND delivery_lane='ops_notification') OR (owner<>'adminops' AND delivery_lane<>'ops_notification'));
