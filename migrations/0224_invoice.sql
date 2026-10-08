-- Extend the existing EER registry for Invoice-owned issue/reversal effects.
ALTER TABLE external_effects DROP CONSTRAINT IF EXISTS external_effects_owner_check;
ALTER TABLE external_effects ADD CONSTRAINT external_effects_owner_check CHECK(owner IN ('outbound','payment','automation','adminops','segment','invoice'));
ALTER TABLE external_effects DROP CONSTRAINT IF EXISTS external_effects_owner_kind_shape;
ALTER TABLE external_effects DROP CONSTRAINT IF EXISTS external_effects_kind_check;
ALTER TABLE external_effects ADD CONSTRAINT external_effects_kind_check CHECK (kind IN (
  'outbound_message','automation_message','outbound_media','wecom_tag_catalog','wecom_tag_catalog_mutation','wecom_contact_description','group_message',
  'channel_acquisition_asset','channel_welcome_message','channel_entry_tag',
  'group_invitation_code','channel_acquisition_link_mutation','sidebar_jssdk_send','survey_completion',
  'customer_owner_handoff','customer_tag_command','commerce_product_push','ai_agent_generate','ai_audience_recommend',
  'wechat_pay_prepay_v1','wechat_pay_refund_v1','wechat_shop_refund_v1','alipay_wap_pay_v1','alipay_page_pay_v1','alipay_refund_v1',
  'wechat_pay_profit_sharing_receiver_v1','wechat_pay_profit_sharing_order_v1','wechat_pay_profit_sharing_unfreeze_v1',
  'feishu_ops_notification_v1','wechat_invoice_issue_v1','wechat_invoice_reverse_v1'
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
  (owner='invoice' AND kind IN ('wechat_invoice_issue_v1','wechat_invoice_reverse_v1')) OR
  (owner='automation' AND kind='ai_agent_generate') OR (owner='adminops' AND kind='feishu_ops_notification_v1') OR (owner='segment' AND kind='ai_audience_recommend')
);

-- Invoice owns only these tables. Order/Payment/Identity remain port readers.
CREATE TABLE invoice_applications (
 id TEXT PRIMARY KEY CHECK(id ~ '^inv_[a-f0-9]{28}$'),
 order_id BIGINT NOT NULL UNIQUE REFERENCES orders(id),
 customer_id BIGINT NOT NULL CHECK(customer_id>0), identity_id BIGINT NOT NULL CHECK(identity_id>0),
 invoice_id TEXT NOT NULL UNIQUE, state TEXT NOT NULL CHECK(state IN ('awaiting_title','queued','issuing','issued','failed','cancelled','reversing','reversed','reverse_failed')),
 card_state TEXT NOT NULL DEFAULT 'pending',
 snapshot JSONB NOT NULL CHECK(jsonb_typeof(snapshot)='object'), buyer JSONB NOT NULL DEFAULT '{}'::jsonb,
 refund_confirmed BOOLEAN NOT NULL DEFAULT FALSE, issue_effect TEXT NOT NULL DEFAULT '', reverse_effect TEXT NOT NULL DEFAULT '',
 blue_number TEXT NOT NULL DEFAULT '',red_number TEXT NOT NULL DEFAULT '', reverse_attempt INTEGER NOT NULL DEFAULT 0 CHECK(reverse_attempt BETWEEN 0 AND 4), issue_attempt INTEGER NOT NULL DEFAULT 0 CHECK(issue_attempt BETWEEN 0 AND 4), dispatch_attempt INTEGER NOT NULL DEFAULT 0 CHECK(dispatch_attempt>=0),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE invoice_operation_receipts (
 customer_id BIGINT NOT NULL, key_digest TEXT NOT NULL, order_id BIGINT NOT NULL, application_id TEXT NOT NULL REFERENCES invoice_applications(id),
 PRIMARY KEY(customer_id,key_digest)
);
CREATE TABLE invoice_callback_receipts (
 event_digest TEXT PRIMARY KEY,body_digest TEXT NOT NULL,application_id TEXT NOT NULL REFERENCES invoice_applications(id),created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE invoice_audit_events (
 id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY, application_id TEXT NOT NULL REFERENCES invoice_applications(id),
 event_type TEXT NOT NULL, evidence_digest TEXT NOT NULL, occurred_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX invoice_applications_customer ON invoice_applications(customer_id,created_at DESC);
