-- Owner: internal/channel. These rows bind an attributed callback to the
-- immutable channel configuration used for assignment and outbound intents.
-- No raw State, external_userid, phone or Provider request body is retained.

ALTER TABLE external_effects DROP CONSTRAINT IF EXISTS external_effects_owner_kind_shape;
ALTER TABLE external_effects DROP CONSTRAINT IF EXISTS external_effects_kind_check;
ALTER TABLE external_effects ADD CONSTRAINT external_effects_kind_check CHECK (kind IN (
  'outbound_message','outbound_media','wecom_tag_catalog','group_message','channel_acquisition_asset','channel_welcome_message','channel_entry_tag',
  'wechat_pay_prepay_v1','wechat_pay_refund_v1','wechat_shop_refund_v1'
));
ALTER TABLE external_effects ADD CONSTRAINT external_effects_owner_kind_shape CHECK (
  (owner='outbound' AND kind IN ('outbound_message','outbound_media','wecom_tag_catalog','group_message','channel_acquisition_asset','channel_welcome_message','channel_entry_tag')) OR
  (owner='payment' AND kind IN ('wechat_pay_prepay_v1','wechat_pay_refund_v1','wechat_shop_refund_v1'))
);

CREATE TABLE channel_entrant_assignments (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    callback_id TEXT NOT NULL UNIQUE CHECK(callback_id=btrim(callback_id) AND char_length(callback_id) BETWEEN 1 AND 512),
    channel_id BIGINT NOT NULL,
    config_version BIGINT NOT NULL,
    customer_id BIGINT NOT NULL REFERENCES customers(id) ON DELETE RESTRICT,
    staff_id BIGINT NOT NULL REFERENCES admin_users(id) ON DELETE RESTRICT,
    strategy TEXT NOT NULL CHECK(strategy IN ('ratio','cap_switch')),
    assignment_digest BYTEA NOT NULL CHECK(octet_length(assignment_digest)=32),
    assigned_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT channel_entrant_assignments_config_fk FOREIGN KEY(channel_id,config_version)
        REFERENCES channel_config_versions(channel_id,config_version) ON DELETE RESTRICT
);
CREATE INDEX channel_entrant_assignments_capacity_idx
    ON channel_entrant_assignments(channel_id,config_version,staff_id,assigned_at DESC);

CREATE TABLE channel_entrant_actions (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    callback_id TEXT NOT NULL CHECK(callback_id=btrim(callback_id) AND char_length(callback_id) BETWEEN 1 AND 512),
    assignment_id BIGINT NOT NULL REFERENCES channel_entrant_assignments(id) ON DELETE RESTRICT,
    channel_id BIGINT NOT NULL,
    config_version BIGINT NOT NULL,
    customer_id BIGINT NOT NULL REFERENCES customers(id) ON DELETE RESTRICT,
    staff_id BIGINT NOT NULL REFERENCES admin_users(id) ON DELETE RESTRICT,
    action_kind TEXT NOT NULL CHECK(action_kind IN ('welcome','entry_tag')),
    welcome_grant_ref TEXT CHECK(welcome_grant_ref IS NULL OR welcome_grant_ref ~ '^wgrant_[1-9a-f][0-9a-f]*$'),
    local_tag_id BIGINT CHECK(local_tag_id IS NULL OR local_tag_id>0),
    welcome_material_snapshot JSONB NOT NULL DEFAULT '{"schema_version":2,"node_kind":"message","attachments":[]}'::jsonb,
    source_ref_digest TEXT NOT NULL UNIQUE CHECK(source_ref_digest ~ '^sha256:[0-9a-f]{64}$'),
    effect_ref TEXT NOT NULL UNIQUE CHECK(effect_ref ~ '^eer_[1-9][0-9]*$'),
    accept_receipt_ref TEXT NOT NULL CHECK(accept_receipt_ref ~ '^eerop_[1-9][0-9]*$'),
    queue_receipt_ref TEXT NOT NULL CHECK(queue_receipt_ref ~ '^eerop_[1-9][0-9]*$'),
    state TEXT NOT NULL CHECK(state IN ('accepted','queued','attempted','executed','outcome_unknown','retryable_failed','final_failed','reconciled','cancelled')),
    result_digest TEXT NOT NULL DEFAULT '' CHECK(result_digest='' OR result_digest ~ '^sha256:[0-9a-f]{64}$'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE(callback_id,action_kind),
    CONSTRAINT channel_entrant_actions_config_fk FOREIGN KEY(channel_id,config_version)
        REFERENCES channel_config_versions(channel_id,config_version) ON DELETE RESTRICT,
    CONSTRAINT channel_entrant_actions_shape CHECK(
        (action_kind='welcome' AND welcome_grant_ref IS NOT NULL AND local_tag_id IS NULL)
        OR (action_kind='entry_tag' AND welcome_grant_ref IS NULL AND local_tag_id IS NOT NULL)
    )
);
CREATE INDEX channel_entrant_actions_timeline_idx ON channel_entrant_actions(channel_id,id DESC);

CREATE FUNCTION channel_entrant_assignment_guard() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN RAISE EXCEPTION 'channel entrant assignment is immutable'; END; $$;
CREATE TRIGGER channel_entrant_assignments_guard BEFORE UPDATE OR DELETE ON channel_entrant_assignments FOR EACH ROW EXECUTE FUNCTION channel_entrant_assignment_guard();
CREATE TRIGGER channel_entrant_assignments_no_truncate BEFORE TRUNCATE ON channel_entrant_assignments FOR EACH STATEMENT EXECUTE FUNCTION channel_entrant_assignment_guard();

CREATE FUNCTION channel_entrant_action_guard() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
  IF TG_OP IN ('DELETE','TRUNCATE') OR NEW.callback_id IS DISTINCT FROM OLD.callback_id OR NEW.assignment_id IS DISTINCT FROM OLD.assignment_id OR NEW.channel_id IS DISTINCT FROM OLD.channel_id OR NEW.config_version IS DISTINCT FROM OLD.config_version OR NEW.customer_id IS DISTINCT FROM OLD.customer_id OR NEW.staff_id IS DISTINCT FROM OLD.staff_id OR NEW.action_kind IS DISTINCT FROM OLD.action_kind OR NEW.welcome_grant_ref IS DISTINCT FROM OLD.welcome_grant_ref OR NEW.local_tag_id IS DISTINCT FROM OLD.local_tag_id OR NEW.welcome_material_snapshot IS DISTINCT FROM OLD.welcome_material_snapshot OR NEW.source_ref_digest IS DISTINCT FROM OLD.source_ref_digest OR NEW.effect_ref IS DISTINCT FROM OLD.effect_ref OR NEW.accept_receipt_ref IS DISTINCT FROM OLD.accept_receipt_ref OR NEW.queue_receipt_ref IS DISTINCT FROM OLD.queue_receipt_ref OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN RAISE EXCEPTION 'channel entrant action identity is immutable'; END IF;
  RETURN NEW;
END; $$;
CREATE TRIGGER channel_entrant_actions_guard BEFORE UPDATE OR DELETE ON channel_entrant_actions FOR EACH ROW EXECUTE FUNCTION channel_entrant_action_guard();
CREATE TRIGGER channel_entrant_actions_no_truncate BEFORE TRUNCATE ON channel_entrant_actions FOR EACH STATEMENT EXECUTE FUNCTION channel_entrant_action_guard();
