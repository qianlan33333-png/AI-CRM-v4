-- Owner: Outbound.  This registers the bounded contact-description effect
-- with the existing External Effects/River kernel.  It stores only the
-- envelope digests; the future dispatch receipt retains local customer/staff
-- references and never raw external_userid or description text.
ALTER TABLE external_effects DROP CONSTRAINT IF EXISTS external_effects_owner_kind_shape;
ALTER TABLE external_effects DROP CONSTRAINT IF EXISTS external_effects_kind_check;
ALTER TABLE external_effects ADD CONSTRAINT external_effects_kind_check CHECK (kind IN (
  'outbound_message','automation_message','outbound_media','wecom_tag_catalog','wecom_tag_catalog_mutation','wecom_contact_description','group_message',
  'channel_acquisition_asset','channel_welcome_message','channel_entry_tag',
  'channel_acquisition_link_mutation','sidebar_jssdk_send','survey_completion',
  'customer_owner_handoff','customer_tag_command','commerce_product_push','ai_agent_generate',
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
  (owner='automation' AND kind='ai_agent_generate')
);

-- The dispatch snapshot has only local customer/employee references and
-- irreversible digests. It deliberately excludes external_userid and the
-- human description, which are re-resolved only in the provider process.
CREATE TABLE outbound_wecom_contact_description_intents (
  id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  customer_id BIGINT NOT NULL REFERENCES customers(id) ON DELETE RESTRICT,
  employee_userid TEXT NOT NULL CHECK (employee_userid=btrim(employee_userid) AND char_length(employee_userid) BETWEEN 1 AND 1024 AND employee_userid !~ '[[:cntrl:] ]'),
  operation TEXT NOT NULL CHECK(operation IN ('write','readback')),
  relationship_digest BYTEA NOT NULL CHECK(octet_length(relationship_digest)=32),
  plan_revision BIGINT NOT NULL CHECK(plan_revision >= 1),
  source_ref_digest BYTEA NOT NULL UNIQUE CHECK(octet_length(source_ref_digest)=32),
  target_digest BYTEA NOT NULL CHECK(octet_length(target_digest)=32),
  observed_description_digest BYTEA NOT NULL CHECK(octet_length(observed_description_digest)=32),
  payload_digest BYTEA NOT NULL CHECK(octet_length(payload_digest)=32),
  receipt_key BYTEA NOT NULL UNIQUE CHECK(octet_length(receipt_key)=32),
  effect_ref TEXT NOT NULL UNIQUE CHECK(effect_ref ~ '^eer_[1-9][0-9]*$'),
  state TEXT NOT NULL CHECK(state IN ('queued','executed','outcome_unknown','retryable_failed','final_failed')),
  result_status TEXT NULL CHECK(result_status IS NULL OR result_status IN ('written','already_present','readback_checked','too_long','not_authorized','description_changed','description_unavailable','relationship_unavailable','target_changed','dispatch_changed','provider_rejected','provider_disabled')),
  -- Only a strictly parsed numeric Provider error from a completed, known
  -- outcome is retained. Provider message text may contain customer data and
  -- is deliberately never accepted into this audit row.
  provider_error_code BIGINT NULL CHECK(provider_error_code IS NULL OR provider_error_code BETWEEN -2147483648 AND 2147483647),
  readback_state TEXT NULL CHECK(readback_state IS NULL OR readback_state IN ('confirmed','failed','not_requested')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  UNIQUE(relationship_digest,plan_revision)
);
CREATE INDEX outbound_wecom_contact_description_intents_customer_idx ON outbound_wecom_contact_description_intents(customer_id,created_at DESC);
CREATE INDEX outbound_wecom_contact_description_intents_relationship_idx ON outbound_wecom_contact_description_intents(relationship_digest,plan_revision DESC);

-- A source run can replay an existing relationship plan. This association is
-- owned by Outbound and stores only local IDs, so sync enumeration and actual
-- effect outcomes remain independently observable without cross-domain writes.
CREATE TABLE outbound_wecom_contact_description_run_items (
  source_run_id BIGINT NOT NULL CHECK(source_run_id > 0),
  intent_id BIGINT NOT NULL REFERENCES outbound_wecom_contact_description_intents(id) ON DELETE RESTRICT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY(source_run_id,intent_id)
);
CREATE INDEX outbound_wecom_contact_description_run_items_intent_idx ON outbound_wecom_contact_description_run_items(intent_id);

CREATE FUNCTION outbound_wecom_contact_description_intent_completion_only() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.id <> OLD.id OR NEW.customer_id <> OLD.customer_id OR NEW.employee_userid <> OLD.employee_userid OR
     NEW.operation <> OLD.operation OR NEW.relationship_digest <> OLD.relationship_digest OR NEW.plan_revision <> OLD.plan_revision OR
     NEW.source_ref_digest <> OLD.source_ref_digest OR NEW.target_digest <> OLD.target_digest OR
     NEW.observed_description_digest <> OLD.observed_description_digest OR NEW.payload_digest <> OLD.payload_digest OR
     NEW.receipt_key <> OLD.receipt_key OR NEW.effect_ref <> OLD.effect_ref OR NEW.created_at <> OLD.created_at THEN
    RAISE EXCEPTION 'outbound contact description intent is immutable';
  END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER outbound_wecom_contact_description_intent_immutable
BEFORE UPDATE ON outbound_wecom_contact_description_intents
FOR EACH ROW EXECUTE FUNCTION outbound_wecom_contact_description_intent_completion_only();

CREATE FUNCTION outbound_wecom_contact_description_intent_no_delete() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'outbound contact description intent cannot be deleted';
END;
$$;
CREATE TRIGGER outbound_wecom_contact_description_intent_no_delete
BEFORE DELETE ON outbound_wecom_contact_description_intents
FOR EACH ROW EXECUTE FUNCTION outbound_wecom_contact_description_intent_no_delete();

CREATE FUNCTION outbound_wecom_contact_description_intent_no_truncate() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'outbound contact description intent cannot be truncated';
END;
$$;
CREATE TRIGGER outbound_wecom_contact_description_intent_no_truncate
BEFORE TRUNCATE ON outbound_wecom_contact_description_intents
FOR EACH STATEMENT EXECUTE FUNCTION outbound_wecom_contact_description_intent_no_truncate();
