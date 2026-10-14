ALTER TABLE external_effects DROP CONSTRAINT IF EXISTS external_effects_owner_kind_shape;
ALTER TABLE external_effects DROP CONSTRAINT IF EXISTS external_effects_kind_check;
ALTER TABLE external_effects ADD CONSTRAINT external_effects_kind_check CHECK (kind IN (
  'outbound_message','outbound_message_control','automation_message','outbound_media','wecom_tag_catalog','wecom_tag_catalog_mutation','wecom_contact_description','group_message',
  'channel_acquisition_asset','channel_welcome_message','channel_entry_tag',
  'group_invitation_code','channel_acquisition_link_mutation','sidebar_jssdk_send','survey_completion',
  'customer_owner_handoff','customer_tag_command','commerce_product_push','ai_agent_generate','ai_audience_recommend',
  'wechat_pay_prepay_v1','wechat_pay_refund_v1','wechat_shop_refund_v1','alipay_wap_pay_v1','alipay_page_pay_v1','alipay_refund_v1',
  'wechat_pay_profit_sharing_receiver_v1','wechat_pay_profit_sharing_order_v1','wechat_pay_profit_sharing_unfreeze_v1',
  'feishu_ops_notification_v1','wechat_invoice_issue_v1','wechat_invoice_reverse_v1','wechat_invoice_card_configure_v1'
));
ALTER TABLE external_effects ADD CONSTRAINT external_effects_owner_kind_shape CHECK (
  (owner='outbound' AND kind IN (
    'outbound_message','outbound_message_control','automation_message','outbound_media','wecom_tag_catalog','wecom_tag_catalog_mutation','wecom_contact_description','group_message',
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


CREATE TABLE outbound_private_message_controls (
 id BIGSERIAL PRIMARY KEY,
 payload_reference TEXT NOT NULL REFERENCES outbound_private_message_receipts(payload_reference),
 action TEXT NOT NULL CHECK(action IN ('stop','reconcile')),
 effect_id TEXT NOT NULL UNIQUE, source_digest TEXT NOT NULL UNIQUE,
 actor_id BIGINT NOT NULL CHECK(actor_id>0), request_key TEXT NOT NULL,
 result_state TEXT NOT NULL DEFAULT '' CHECK(result_state IN ('','stopped','delivered','pending')),
 receipt_digest TEXT NOT NULL DEFAULT '', sent_at TIMESTAMPTZ, stopped_at TIMESTAMPTZ,
 observed_at TIMESTAMPTZ, created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(payload_reference,action,request_key),
 CHECK((result_state='delivered')=(sent_at IS NOT NULL)),
 CHECK((result_state='stopped')=(stopped_at IS NOT NULL))
);
CREATE UNIQUE INDEX outbound_private_message_single_stop ON outbound_private_message_controls(payload_reference) WHERE action='stop';
