-- Owner: internal/outbound with internal/externaleffects. Tag catalog writes
-- use a distinct effect kind from catalog observation reads so an ambiguous
-- create can remain outcome_unknown without being retried or name-matched.
ALTER TABLE external_effects DROP CONSTRAINT IF EXISTS external_effects_owner_kind_shape;
ALTER TABLE external_effects DROP CONSTRAINT IF EXISTS external_effects_kind_check;
ALTER TABLE external_effects ADD CONSTRAINT external_effects_kind_check CHECK (kind IN (
  'outbound_message','automation_message','outbound_media','wecom_tag_catalog','wecom_tag_catalog_mutation','group_message',
  'channel_acquisition_asset','channel_welcome_message','channel_entry_tag',
  'channel_acquisition_link_mutation','sidebar_jssdk_send','survey_completion','customer_owner_handoff','customer_tag_command','commerce_product_push',
  'wechat_pay_prepay_v1','wechat_pay_refund_v1','wechat_shop_refund_v1'
));
ALTER TABLE external_effects ADD CONSTRAINT external_effects_owner_kind_shape CHECK (
  (owner='outbound' AND kind IN (
    'outbound_message','automation_message','outbound_media','wecom_tag_catalog','wecom_tag_catalog_mutation','group_message',
    'channel_acquisition_asset','channel_welcome_message','channel_entry_tag',
    'channel_acquisition_link_mutation','sidebar_jssdk_send','survey_completion','customer_owner_handoff','customer_tag_command','commerce_product_push'
  )) OR
  (owner='payment' AND kind IN ('wechat_pay_prepay_v1','wechat_pay_refund_v1','wechat_shop_refund_v1'))
);
