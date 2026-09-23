-- Customer-owned local assignee and handoff command ledger. WeCom directory
-- observations remain read-only facts in their existing owner tables.
CREATE TABLE customer_local_owners (
    customer_id BIGINT PRIMARY KEY REFERENCES customers(id),
    staff_id BIGINT NOT NULL REFERENCES admin_users(id),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    source TEXT NOT NULL CHECK (source IN ('owner_handoff_local_only','owner_handoff_wecom_then_crm')),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);

CREATE TABLE customer_owner_handoff_previews (
    id TEXT PRIMARY KEY,
    actor_admin_user_id BIGINT NOT NULL REFERENCES admin_users(id),
    mode TEXT NOT NULL CHECK (mode IN ('local_only','wecom_then_crm')),
    source_staff_id BIGINT NOT NULL REFERENCES admin_users(id),
    target_staff_id BIGINT NOT NULL REFERENCES admin_users(id),
    corp_scope TEXT NOT NULL,
    request_digest BYTEA NOT NULL CHECK (octet_length(request_digest)=32),
    confirmation_phrase TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    executed_batch_id TEXT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CHECK (source_staff_id <> target_staff_id)
);

CREATE TABLE customer_owner_handoff_preview_rows (
    preview_id TEXT NOT NULL REFERENCES customer_owner_handoff_previews(id) ON DELETE CASCADE,
    line_no INTEGER NOT NULL CHECK (line_no > 0),
    customer_id BIGINT NOT NULL REFERENCES customers(id),
    expected_local_owner_staff_id BIGINT NULL REFERENCES admin_users(id),
    expected_local_owner_version BIGINT NULL CHECK (expected_local_owner_version > 0),
    relation_digest BYTEA NOT NULL CHECK (octet_length(relation_digest)=32),
    -- Provider identifiers are frozen encrypted in the Customer-owned command
    -- record. The effects envelope keeps only matching digests.
    source_userid_ciphertext BYTEA NULL,
    target_userid_ciphertext BYTEA NULL,
    external_identity_ciphertext BYTEA NULL,
    welcome_message_ciphertext BYTEA NULL,
    state TEXT NOT NULL CHECK (state IN ('ready','excluded','conflict','unresolved')),
    reason TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (preview_id,line_no),
    UNIQUE (preview_id,customer_id)
);

CREATE TABLE customer_owner_handoff_batches (
    id TEXT PRIMARY KEY,
    preview_id TEXT NOT NULL UNIQUE REFERENCES customer_owner_handoff_previews(id),
    actor_admin_user_id BIGINT NOT NULL REFERENCES admin_users(id),
    idempotency_key TEXT NOT NULL,
    request_digest BYTEA NOT NULL CHECK (octet_length(request_digest)=32),
    mode TEXT NOT NULL CHECK (mode IN ('local_only','wecom_then_crm')),
    source_staff_id BIGINT NOT NULL REFERENCES admin_users(id),
    target_staff_id BIGINT NOT NULL REFERENCES admin_users(id),
    corp_scope TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('accepted','executing','completed','needs_attention','failed')),
    transfer_result_cursor_ciphertext BYTEA NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (actor_admin_user_id,idempotency_key)
);

CREATE TABLE customer_owner_handoff_lines (
    batch_id TEXT NOT NULL REFERENCES customer_owner_handoff_batches(id) ON DELETE CASCADE,
    line_no INTEGER NOT NULL CHECK (line_no > 0),
    customer_id BIGINT NOT NULL REFERENCES customers(id),
    mode TEXT NOT NULL CHECK (mode IN ('local_only','wecom_then_crm')),
    source_staff_id BIGINT NOT NULL REFERENCES admin_users(id),
    target_staff_id BIGINT NOT NULL REFERENCES admin_users(id),
    expected_local_owner_version BIGINT NULL CHECK (expected_local_owner_version > 0),
    relation_digest BYTEA NOT NULL CHECK (octet_length(relation_digest)=32),
    source_userid_ciphertext BYTEA NULL,
    target_userid_ciphertext BYTEA NULL,
    external_identity_ciphertext BYTEA NULL,
    welcome_message_ciphertext BYTEA NULL,
    effect_id TEXT NULL,
    effect_receipt_id TEXT NULL,
    effect_attempt INTEGER NOT NULL DEFAULT 0 CHECK (effect_attempt >= 0),
    effect_generation BIGINT NOT NULL DEFAULT 0 CHECK (effect_generation >= 0),
    effect_fence BIGINT NOT NULL DEFAULT 0 CHECK (effect_fence >= 0),
    state TEXT NOT NULL CHECK (state IN ('local_updated','queued','provider_accepted','outcome_unknown','retryable_failed','final_failed','cas_conflict','observed','excluded','conflict','unresolved')),
    result_digest BYTEA NULL CHECK (result_digest IS NULL OR octet_length(result_digest)=32),
    transfer_status INTEGER NULL CHECK (transfer_status IS NULL OR transfer_status BETWEEN 1 AND 5),
    transfer_takeover_at TIMESTAMPTZ NULL,
    observed_at TIMESTAMPTZ NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (batch_id,line_no),
    UNIQUE (batch_id,customer_id)
);

-- One transfer_customer protocol request is a bounded, Customer-owned
-- sub-batch. Its mapping freezes the exact rows for its one EER receipt;
-- multiple lines intentionally share the same effect_id.
CREATE TABLE customer_owner_handoff_effects (
    batch_id TEXT NOT NULL REFERENCES customer_owner_handoff_batches(id) ON DELETE CASCADE,
    subbatch_ordinal BIGINT NOT NULL CHECK (subbatch_ordinal > 0),
    source_ref_digest TEXT NOT NULL CHECK (length(source_ref_digest)=71),
    target_ref_digest TEXT NOT NULL CHECK (length(target_ref_digest)=71),
    payload_digest TEXT NOT NULL CHECK (length(payload_digest)=71),
    policy_digest TEXT NOT NULL CHECK (length(policy_digest)=71),
    effect_id TEXT NOT NULL UNIQUE,
    effect_receipt_id TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (batch_id,subbatch_ordinal),
    UNIQUE (batch_id,effect_id)
);

CREATE TABLE customer_owner_handoff_effect_lines (
    batch_id TEXT NOT NULL,
    subbatch_ordinal BIGINT NOT NULL,
    line_no INTEGER NOT NULL CHECK (line_no > 0),
    PRIMARY KEY (batch_id,line_no),
    FOREIGN KEY (batch_id,subbatch_ordinal)
        REFERENCES customer_owner_handoff_effects(batch_id,subbatch_ordinal)
        ON DELETE CASCADE,
    FOREIGN KEY (batch_id,line_no)
        REFERENCES customer_owner_handoff_lines(batch_id,line_no)
        ON DELETE CASCADE
);

ALTER TABLE customer_owner_handoff_previews
    ADD CONSTRAINT customer_owner_handoff_previews_batch_fk
    FOREIGN KEY (executed_batch_id) REFERENCES customer_owner_handoff_batches(id);

-- A history import is a read-only projection of one protected source
-- snapshot. It deliberately has no effect, provider, or local-owner binding:
-- replay and verification can only append/read this Customer-owned ledger.
CREATE TABLE customer_owner_handoff_history_runs (
    run_key TEXT PRIMARY KEY,
    snapshot_digest BYTEA NOT NULL CHECK (octet_length(snapshot_digest)=32),
    source_system TEXT NOT NULL,
    input_count BIGINT NOT NULL CHECK (input_count >= 0),
    status TEXT NOT NULL CHECK (status IN ('applied','reconciled')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    reconciled_at TIMESTAMPTZ NULL
);

CREATE TABLE customer_owner_handoff_history_imports (
    run_key TEXT NOT NULL REFERENCES customer_owner_handoff_history_runs(run_key),
    source_batch_id TEXT NOT NULL,
    source_line_id TEXT NOT NULL,
    mode TEXT NOT NULL CHECK (mode IN ('local_only','wecom_then_crm')),
    source_state TEXT NOT NULL,
    source_occurred_at TIMESTAMPTZ NOT NULL,
    source_digest BYTEA NOT NULL CHECK (octet_length(source_digest)=32),
    source_subject_digest BYTEA NOT NULL CHECK (octet_length(source_subject_digest)=32),
    customer_id BIGINT NULL REFERENCES customers(id),
    source_staff_id BIGINT NULL REFERENCES admin_users(id),
    target_staff_id BIGINT NULL REFERENCES admin_users(id),
    source_staff_ref_digest BYTEA NULL CHECK (source_staff_ref_digest IS NULL OR octet_length(source_staff_ref_digest)=32),
    target_staff_ref_digest BYTEA NULL CHECK (target_staff_ref_digest IS NULL OR octet_length(target_staff_ref_digest)=32),
    source_result_digest BYTEA NOT NULL CHECK (octet_length(source_result_digest)=32),
    resolution_digest BYTEA NOT NULL CHECK (octet_length(resolution_digest)=32),
    imported_state TEXT NOT NULL CHECK (imported_state IN ('observed','pending_mapping','conflict','invalid')),
    imported_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (source_batch_id,source_line_id)
);
CREATE INDEX ix_customer_owner_handoff_history_imports_run
    ON customer_owner_handoff_history_imports(run_key, imported_state);
ALTER TABLE customer_owner_handoff_preview_rows
    ADD COLUMN source_userid_digest BYTEA NULL CHECK (source_userid_digest IS NULL OR octet_length(source_userid_digest)=32),
    ADD COLUMN target_userid_digest BYTEA NULL CHECK (target_userid_digest IS NULL OR octet_length(target_userid_digest)=32),
    ADD COLUMN external_identity_digest BYTEA NULL CHECK (external_identity_digest IS NULL OR octet_length(external_identity_digest)=32),
    ADD COLUMN payload_digest BYTEA NULL CHECK (payload_digest IS NULL OR octet_length(payload_digest)=32),
    ADD COLUMN policy_digest BYTEA NULL CHECK (policy_digest IS NULL OR octet_length(policy_digest)=32);

ALTER TABLE customer_owner_handoff_lines
    ADD COLUMN source_userid_digest BYTEA NULL CHECK (source_userid_digest IS NULL OR octet_length(source_userid_digest)=32),
    ADD COLUMN target_userid_digest BYTEA NULL CHECK (target_userid_digest IS NULL OR octet_length(target_userid_digest)=32),
    ADD COLUMN external_identity_digest BYTEA NULL CHECK (external_identity_digest IS NULL OR octet_length(external_identity_digest)=32),
    ADD COLUMN payload_digest BYTEA NULL CHECK (payload_digest IS NULL OR octet_length(payload_digest)=32),
    ADD COLUMN policy_digest BYTEA NULL CHECK (policy_digest IS NULL OR octet_length(policy_digest)=32),
    ADD COLUMN transfer_success_message TEXT NOT NULL DEFAULT '';

-- Extend the existing EER kind registry only. Customer never writes the EER
-- tables directly; its transaction accepts this opaque Outbound-owned effect.
ALTER TABLE external_effects DROP CONSTRAINT IF EXISTS external_effects_owner_kind_shape;
ALTER TABLE external_effects DROP CONSTRAINT IF EXISTS external_effects_kind_check;
ALTER TABLE external_effects ADD CONSTRAINT external_effects_kind_check CHECK (kind IN (
  'outbound_message','automation_message','outbound_media','wecom_tag_catalog','group_message',
  'channel_acquisition_asset','channel_welcome_message','channel_entry_tag',
  'channel_acquisition_link_mutation','sidebar_jssdk_send','survey_completion','customer_owner_handoff','customer_tag_command','commerce_product_push',
  'wechat_pay_prepay_v1','wechat_pay_refund_v1','wechat_shop_refund_v1'
));
ALTER TABLE external_effects ADD CONSTRAINT external_effects_owner_kind_shape CHECK (
  (owner='outbound' AND kind IN (
    'outbound_message','automation_message','outbound_media','wecom_tag_catalog','group_message',
    'channel_acquisition_asset','channel_welcome_message','channel_entry_tag',
    'channel_acquisition_link_mutation','sidebar_jssdk_send','survey_completion','customer_owner_handoff','customer_tag_command','commerce_product_push'
  )) OR
  (owner='payment' AND kind IN ('wechat_pay_prepay_v1','wechat_pay_refund_v1','wechat_shop_refund_v1'))
);
