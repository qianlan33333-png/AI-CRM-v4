-- Owner: internal/customer. Generic WeCom customer-tag commands are durable
-- business requests; raw external_userid and provider tag ids remain in WeCom
-- and Tag adapters. One line is one customer mark_tag add/remove request.
CREATE TABLE customer_tag_commands (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    actor_admin_user_id BIGINT NULL REFERENCES admin_users(id) ON DELETE RESTRICT,
    source TEXT NOT NULL CHECK (source ~ '^[a-z][a-z0-9_.]{0,63}$'),
    source_ref TEXT NOT NULL CHECK (source_ref = btrim(source_ref) AND char_length(source_ref) BETWEEN 1 AND 160 AND source_ref !~ '[[:cntrl:]]'),
    idempotency_key TEXT NOT NULL CHECK (idempotency_key = btrim(idempotency_key) AND char_length(idempotency_key) BETWEEN 1 AND 160 AND idempotency_key !~ '[[:cntrl:]]'),
    payload_digest BYTEA NOT NULL CHECK (octet_length(payload_digest)=32),
    state TEXT NOT NULL CHECK (state IN ('accepted','queued','attempted','executed','outcome_unknown','retryable_failed','final_failed','reconciled','cancelled','rejected','partial')),
    occurred_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(), updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT customer_tag_commands_source_idempotency_unique UNIQUE(source, source_ref, idempotency_key)
);
CREATE TABLE customer_tag_command_lines (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    command_id BIGINT NOT NULL REFERENCES customer_tag_commands(id) ON DELETE RESTRICT,
    customer_id BIGINT NOT NULL REFERENCES customers(id) ON DELETE RESTRICT,
    staff_id BIGINT NULL REFERENCES admin_users(id) ON DELETE RESTRICT,
    add_tag_ids BIGINT[] NOT NULL DEFAULT '{}' CHECK (cardinality(add_tag_ids) <= 100),
    remove_tag_ids BIGINT[] NOT NULL DEFAULT '{}' CHECK (cardinality(remove_tag_ids) <= 100),
    binding_digest TEXT NULL CHECK (binding_digest IS NULL OR binding_digest ~ '^sha256:[0-9a-f]{64}$'),
    target_digest TEXT NULL CHECK (target_digest IS NULL OR target_digest ~ '^sha256:[0-9a-f]{64}$'),
    source_ref_digest TEXT NULL UNIQUE CHECK (source_ref_digest IS NULL OR source_ref_digest ~ '^sha256:[0-9a-f]{64}$'),
    effect_ref TEXT NULL UNIQUE CHECK (effect_ref IS NULL OR effect_ref ~ '^eer_[1-9][0-9]*$'),
    accept_receipt_ref TEXT NULL UNIQUE CHECK (accept_receipt_ref IS NULL OR accept_receipt_ref ~ '^eerop_[1-9][0-9]*$'),
    queue_receipt_ref TEXT NULL CHECK (queue_receipt_ref IS NULL OR queue_receipt_ref ~ '^eerop_[1-9][0-9]*$'),
    state TEXT NOT NULL CHECK (state IN ('accepted','queued','attempted','executed','outcome_unknown','retryable_failed','final_failed','reconciled','cancelled','rejected','partial')),
    reject_reason TEXT NULL CHECK (reject_reason IS NULL OR reject_reason ~ '^[a-z][a-z0-9_]{0,63}$'),
    result_reason TEXT NULL CHECK (result_reason IS NULL OR result_reason ~ '^[a-z][a-z0-9_]{0,63}$'),
    result_digest TEXT NULL CHECK (result_digest IS NULL OR result_digest ~ '^sha256:[0-9a-f]{64}$'),
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    completion_generation BIGINT NOT NULL DEFAULT 0 CHECK (completion_generation >= 0),
    completion_fence BIGINT NOT NULL DEFAULT 0 CHECK (completion_fence >= 0),
    completed_at TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(), updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT customer_tag_command_lines_one_mutation CHECK (cardinality(add_tag_ids) > 0 OR cardinality(remove_tag_ids) > 0),
    CONSTRAINT customer_tag_command_lines_no_overlap CHECK (NOT (add_tag_ids && remove_tag_ids)),
    CONSTRAINT customer_tag_command_lines_unique_customer UNIQUE(command_id, customer_id)
);
CREATE INDEX customer_tag_command_lines_customer_idx ON customer_tag_command_lines(customer_id, created_at DESC, id DESC);
CREATE INDEX customer_tag_command_lines_effect_idx ON customer_tag_command_lines(effect_ref);

-- Owner: internal/wecom. This shared complete-observation version row covers
-- both a full-directory page and a single-contact tag refresh for one
-- customer/employee, including the valid empty set. It serializes replacement
-- of the active tag set without adding another worker or queue.
CREATE TABLE wecom_customer_tag_refresh_watermarks (
    customer_id BIGINT NOT NULL REFERENCES customers(id) ON DELETE RESTRICT,
    corp_scope TEXT NOT NULL CHECK (left(corp_scope, 11) = 'wecom-corp:'),
    employee_id TEXT NOT NULL CHECK (employee_id = btrim(employee_id) AND char_length(employee_id) BETWEEN 1 AND 1024 AND employee_id !~ '[[:cntrl:]]'),
    last_seen_run_id BIGINT NOT NULL REFERENCES wecom_customer_sync_runs(id) ON DELETE RESTRICT,
    observed_at TIMESTAMPTZ NOT NULL,
    observation_version BIGINT NOT NULL DEFAULT 1 CHECK (observation_version > 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY(customer_id, corp_scope, employee_id)
);
CREATE INDEX wecom_customer_tag_refresh_watermarks_run_idx ON wecom_customer_tag_refresh_watermarks(last_seen_run_id, observed_at);


-- A single-contact observation read is not a full directory reconciliation.
-- It keeps the existing run provenance foreign key while a later completed
-- full sync remains authoritative for stale reconciliation.
ALTER TABLE wecom_customer_sync_runs DROP CONSTRAINT IF EXISTS wecom_customer_sync_runs_trigger_type_check;
ALTER TABLE wecom_customer_sync_runs ADD CONSTRAINT wecom_customer_sync_runs_trigger_type_check
    CHECK (trigger_type IN ('initial','daily','manual','tag_refresh'));

-- Owner coordination: new Channel entry_tag accepts the Customer-owned command
-- in the callback UoW. A failed trusted freeze has no external effect, but must
-- remain a visible, immutable Channel action fact and must not roll back the
-- valid entrant assignment or its other actions.
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

ALTER TABLE IF EXISTS channel_entrant_actions ALTER COLUMN effect_ref DROP NOT NULL;
ALTER TABLE IF EXISTS channel_entrant_actions ALTER COLUMN accept_receipt_ref DROP NOT NULL;
ALTER TABLE IF EXISTS channel_entrant_actions ALTER COLUMN queue_receipt_ref DROP NOT NULL;
ALTER TABLE IF EXISTS channel_entrant_actions ADD COLUMN IF NOT EXISTS result_reason TEXT NULL CHECK (result_reason IS NULL OR result_reason ~ '^[a-z][a-z0-9_]{0,63}$');
ALTER TABLE IF EXISTS channel_entrant_actions DROP CONSTRAINT IF EXISTS channel_entrant_actions_state_check;
ALTER TABLE IF EXISTS channel_entrant_actions ADD CONSTRAINT channel_entrant_actions_state_check CHECK (state IN ('accepted','queued','attempted','executed','outcome_unknown','retryable_failed','final_failed','reconciled','cancelled','rejected'));
ALTER TABLE IF EXISTS channel_entrant_actions DROP CONSTRAINT IF EXISTS channel_entrant_actions_shape;
ALTER TABLE IF EXISTS channel_entrant_actions ADD CONSTRAINT channel_entrant_actions_shape CHECK (
  (action_kind='welcome' AND welcome_grant_ref IS NOT NULL AND local_tag_id IS NULL)
  OR (action_kind='entry_tag' AND welcome_grant_ref IS NULL AND local_tag_id IS NOT NULL)
);
ALTER TABLE IF EXISTS channel_entrant_actions ADD CONSTRAINT channel_entrant_actions_effect_shape CHECK (
  (state='rejected' AND effect_ref IS NULL AND accept_receipt_ref IS NULL AND queue_receipt_ref IS NULL)
  OR (state<>'rejected' AND effect_ref IS NOT NULL AND accept_receipt_ref IS NOT NULL AND queue_receipt_ref IS NOT NULL)
);

-- Owner: internal/customer. Imported legacy effect records are immutable
-- history receipts only. They deliberately do not point at current commands,
-- external effects, River jobs, or raw Provider identifiers.
CREATE TABLE customer_tag_history_import_batches (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    source_system TEXT NOT NULL CHECK (source_system = 'v2_external_effect_job'),
    snapshot_digest TEXT NOT NULL CHECK (snapshot_digest ~ '^sha256:[0-9a-f]{64}$'),
    snapshot_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT customer_tag_history_import_batches_source_snapshot UNIQUE(source_system, snapshot_digest)
);
CREATE TABLE customer_tag_history_receipts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    batch_id BIGINT NOT NULL REFERENCES customer_tag_history_import_batches(id) ON DELETE RESTRICT,
    source_system TEXT NOT NULL CHECK (source_system = 'v2_external_effect_job'),
    source_job_id BIGINT NOT NULL CHECK (source_job_id > 0),
    source_digest TEXT NOT NULL CHECK (source_digest ~ '^sha256:[0-9a-f]{64}$'),
    effect_type TEXT NOT NULL CHECK (effect_type IN ('wecom.contact.tag.mark','wecom.contact.tag.unmark')),
    operation TEXT NOT NULL CHECK (operation IN ('tag_mark','tag_unmark')),
    source_state TEXT NOT NULL CHECK (source_state ~ '^[a-z][a-z0-9_]{0,63}$'),
    resolution TEXT NOT NULL CHECK (resolution IN ('imported','pending','conflict','excluded','failed')),
    reason TEXT NULL CHECK (reason IS NULL OR reason ~ '^[a-z][a-z0-9_]{0,63}$'),
    customer_id BIGINT NULL REFERENCES customers(id) ON DELETE RESTRICT,
    staff_id BIGINT NULL REFERENCES admin_users(id) ON DELETE RESTRICT,
    add_tag_ids BIGINT[] NOT NULL DEFAULT '{}' CHECK (cardinality(add_tag_ids) <= 100),
    remove_tag_ids BIGINT[] NOT NULL DEFAULT '{}' CHECK (cardinality(remove_tag_ids) <= 100),
    occurred_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ NULL,
    imported_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT customer_tag_history_receipts_source_unique UNIQUE(source_system, source_job_id),
    CONSTRAINT customer_tag_history_receipts_mutation_shape CHECK (
       (effect_type='wecom.contact.tag.mark' AND operation='tag_mark' AND cardinality(remove_tag_ids)=0)
       OR (effect_type='wecom.contact.tag.unmark' AND operation='tag_unmark' AND cardinality(add_tag_ids)=0)
       OR (effect_type='wecom.contact.tag.mark' AND operation='tag_unmark' AND cardinality(add_tag_ids)=0 AND cardinality(remove_tag_ids)=0)
       OR (effect_type='wecom.contact.tag.unmark' AND operation='tag_mark' AND cardinality(add_tag_ids)=0 AND cardinality(remove_tag_ids)=0)
    ),
    CONSTRAINT customer_tag_history_receipts_resolution_shape CHECK (
       (resolution='imported' AND customer_id IS NOT NULL AND cardinality(add_tag_ids)+cardinality(remove_tag_ids)>0)
       OR (resolution<>'imported')
    )
);
CREATE INDEX customer_tag_history_receipts_customer_idx ON customer_tag_history_receipts(customer_id, occurred_at DESC, id DESC) WHERE customer_id IS NOT NULL;
