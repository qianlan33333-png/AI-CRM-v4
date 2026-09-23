-- Owner: internal/externaleffects. Survey submits opaque completion intents
-- through the registered Outbound owner; this migration extends the shared
-- EER kind registry only. It does not create a Survey execution queue.
--
-- Keep every owner/kind pair admitted by 0057. 0066 later widens the job
-- queue constraint to include outbound_welcome; this migration deliberately
-- does not replace or narrow that independent constraint.
ALTER TABLE external_effects DROP CONSTRAINT IF EXISTS external_effects_owner_kind_shape;
ALTER TABLE external_effects DROP CONSTRAINT IF EXISTS external_effects_kind_check;

ALTER TABLE external_effects ADD CONSTRAINT external_effects_kind_check CHECK (kind IN (
  'outbound_message','automation_message','outbound_media','wecom_tag_catalog','group_message',
  'channel_acquisition_asset','channel_welcome_message','channel_entry_tag',
  'channel_acquisition_link_mutation','sidebar_jssdk_send','survey_completion',
  'wechat_pay_prepay_v1','wechat_pay_refund_v1','wechat_shop_refund_v1'
));

ALTER TABLE external_effects ADD CONSTRAINT external_effects_owner_kind_shape CHECK (
  (owner='outbound' AND kind IN (
    'outbound_message','automation_message','outbound_media','wecom_tag_catalog','group_message',
    'channel_acquisition_asset','channel_welcome_message','channel_entry_tag',
    'channel_acquisition_link_mutation','sidebar_jssdk_send','survey_completion'
  )) OR
  (owner='payment' AND kind IN (
    'wechat_pay_prepay_v1','wechat_pay_refund_v1','wechat_shop_refund_v1'
  ))
);
