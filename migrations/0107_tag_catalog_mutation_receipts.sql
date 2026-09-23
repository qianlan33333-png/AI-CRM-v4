-- Owner: internal/tag. These are local dispatch snapshots and outcome facts;
-- provider IDs remain Tag-owned and do not become customer identity keys.
CREATE TABLE tag_catalog_mutation_receipts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    operation TEXT NOT NULL CHECK (operation IN ('group_create','group_update','group_archive','tag_create','tag_update','tag_archive')),
    -- The authenticated actor is an audit fact. Tag intentionally does not
    -- add a cross-domain foreign key to Access-owned tables.
    actor_admin_user_id BIGINT NOT NULL CHECK (actor_admin_user_id > 0),
    group_id BIGINT NULL REFERENCES tag_groups(id) ON DELETE RESTRICT,
    tag_id BIGINT NULL REFERENCES tag_catalog_tags(id) ON DELETE RESTRICT,
    group_name TEXT NOT NULL DEFAULT '' CHECK (char_length(group_name)<=200 AND group_name=btrim(group_name)),
    tag_name TEXT NOT NULL DEFAULT '' CHECK (char_length(tag_name)<=200 AND tag_name=btrim(tag_name)),
    provider_group_id TEXT NOT NULL DEFAULT '' CHECK (char_length(provider_group_id)<=128 AND provider_group_id=btrim(provider_group_id)),
    provider_tag_id TEXT NOT NULL DEFAULT '' CHECK (char_length(provider_tag_id)<=128 AND provider_tag_id=btrim(provider_tag_id)),
    source_ref_digest TEXT NULL UNIQUE CHECK (source_ref_digest IS NULL OR source_ref_digest ~ '^sha256:[0-9a-f]{64}$'),
    effect_ref TEXT NULL UNIQUE CHECK (effect_ref IS NULL OR effect_ref ~ '^eer_[1-9][0-9]*$'),
    accept_receipt_ref TEXT NULL UNIQUE CHECK (accept_receipt_ref IS NULL OR accept_receipt_ref ~ '^eerop_[1-9][0-9]*$'),
    queue_receipt_ref TEXT NULL CHECK (queue_receipt_ref IS NULL OR queue_receipt_ref ~ '^eerop_[1-9][0-9]*$'),
    state TEXT NOT NULL CHECK (state IN ('reserved','queued','executed','outcome_unknown','retryable_failed','final_failed','reconciled','cancelled')),
    result_digest TEXT NULL CHECK (result_digest IS NULL OR result_digest ~ '^sha256:[0-9a-f]{64}$'),
    readback_at TIMESTAMPTZ NULL,
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count>=0),
    completion_generation BIGINT NOT NULL DEFAULT 0 CHECK (completion_generation>=0),
    completion_fence BIGINT NOT NULL DEFAULT 0 CHECK (completion_fence>=0),
    completed_at TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT tag_catalog_mutation_receipts_shape CHECK (
      (operation='group_create' AND group_id IS NOT NULL AND tag_id IS NOT NULL AND group_name<>'' AND tag_name<>'' AND provider_group_id='' AND provider_tag_id='') OR
      (operation='tag_create' AND group_id IS NOT NULL AND tag_id IS NOT NULL AND group_name='' AND tag_name<>'' AND provider_group_id<>'' AND provider_tag_id='') OR
      (operation='group_update' AND group_id IS NOT NULL AND tag_id IS NULL AND group_name<>'' AND tag_name='' AND provider_group_id<>'' AND provider_tag_id='') OR
      (operation='tag_update' AND group_id IS NULL AND tag_id IS NOT NULL AND group_name='' AND tag_name<>'' AND provider_group_id='' AND provider_tag_id<>'' ) OR
      (operation='group_archive' AND group_id IS NOT NULL AND tag_id IS NULL AND group_name='' AND tag_name='' AND provider_group_id<>'' AND provider_tag_id='') OR
      (operation='tag_archive' AND group_id IS NULL AND tag_id IS NOT NULL AND group_name='' AND tag_name='' AND provider_group_id='' AND provider_tag_id<>'')
    ),
    CONSTRAINT tag_catalog_mutation_receipts_effect_shape CHECK (
      -- Reservation allocates the immutable source digest before Outbound
      -- accepts the EER effect. It has no effect or queue receipt yet.
      (state='reserved' AND effect_ref IS NULL AND accept_receipt_ref IS NULL AND queue_receipt_ref IS NULL)
      OR (state<>'reserved' AND source_ref_digest IS NOT NULL AND effect_ref IS NOT NULL AND accept_receipt_ref IS NOT NULL AND queue_receipt_ref IS NOT NULL)
    )
);
CREATE INDEX tag_catalog_mutation_receipts_group_idx ON tag_catalog_mutation_receipts(group_id,id DESC) WHERE group_id IS NOT NULL;
CREATE INDEX tag_catalog_mutation_receipts_tag_idx ON tag_catalog_mutation_receipts(tag_id,id DESC) WHERE tag_id IS NOT NULL;
