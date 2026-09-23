-- Owner: outbound. Generic temporary Provider media credentials and refresh rounds.
ALTER TABLE external_effect_jobs DROP CONSTRAINT IF EXISTS external_effect_jobs_queue_check;
ALTER TABLE external_effect_jobs ADD CONSTRAINT external_effect_jobs_queue_check
 CHECK(queue IN ('outbound','outbound_welcome','outbound_excel','outbound_media'));

-- The original bounded lane is part of the effect's durable dispatch policy.
-- Retries must not fall back to the general outbound queue.
ALTER TABLE external_effects ADD COLUMN delivery_lane TEXT NOT NULL DEFAULT ''
 CHECK(delivery_lane IN ('','outbound_excel','outbound_media'));

CREATE TABLE outbound_material_preparations (
 id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 source_ref TEXT NOT NULL,
 source_type TEXT NOT NULL CHECK(source_type IN ('image','file','video','voice')),
 scope_digest TEXT NOT NULL,
	cache_key_digest TEXT NOT NULL,
 content_digest BYTEA NOT NULL CHECK(octet_length(content_digest)=32),
 file_name TEXT NOT NULL CHECK(length(file_name) BETWEEN 1 AND 255),
 media_type TEXT NOT NULL,
	size_bytes BIGINT NOT NULL CHECK(size_bytes>0),
 source_version BIGINT NOT NULL CHECK(source_version>0),
 refresh_date DATE,
	refresh_round_id BIGINT,
	operation_key_digest TEXT,
	operation_command_digest TEXT,
	actor_admin_user_id BIGINT CHECK(actor_admin_user_id>0),
 effect_id TEXT UNIQUE,
 state TEXT NOT NULL CHECK(state IN ('accepted','queued','executed','retryable_failed','outcome_unknown','final_failed','reconciled','cancelled')),
 media_id TEXT,
 provider_created_at TIMESTAMPTZ,
 expires_at TIMESTAMPTZ,
 is_current BOOLEAN NOT NULL DEFAULT FALSE,
 failure_code TEXT NOT NULL DEFAULT '',
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 CHECK ((state='executed' AND media_id IS NOT NULL AND provider_created_at IS NOT NULL AND expires_at=provider_created_at+interval '72 hours') OR state<>'executed')
);
CREATE UNIQUE INDEX outbound_material_current_resource ON outbound_material_preparations(scope_digest,cache_key_digest) WHERE is_current;
-- An unknown upload blocks later dates and operator keys until reconciliation.
CREATE UNIQUE INDEX outbound_material_unresolved_snapshot ON outbound_material_preparations(scope_digest,cache_key_digest) WHERE state IN ('accepted','queued','retryable_failed','outcome_unknown');
CREATE INDEX outbound_material_source_history ON outbound_material_preparations(scope_digest,source_ref,id DESC);
CREATE UNIQUE INDEX outbound_material_operation_receipt ON outbound_material_preparations(scope_digest,operation_key_digest) WHERE operation_key_digest IS NOT NULL;

CREATE TABLE outbound_material_events (
 id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 preparation_id BIGINT NOT NULL REFERENCES outbound_material_preparations(id),
 operation TEXT NOT NULL,
 evidence_digest TEXT NOT NULL,
 failure_code TEXT NOT NULL DEFAULT '',
 occurred_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(preparation_id,operation,evidence_digest)
);
CREATE TABLE outbound_material_outbox (
 id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 preparation_id BIGINT NOT NULL REFERENCES outbound_material_preparations(id),
 event_type TEXT NOT NULL,
 payload JSONB NOT NULL,
 evidence_digest TEXT NOT NULL,
 occurred_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(preparation_id,event_type,evidence_digest)
);

CREATE TABLE outbound_material_refresh_rounds (
 id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 local_date DATE NOT NULL,
	round_kind TEXT NOT NULL CHECK(round_kind IN ('daily','manual')),
	operation_key_digest TEXT NOT NULL UNIQUE,
	actor_admin_user_id BIGINT CHECK(actor_admin_user_id>0),
	state TEXT NOT NULL CHECK(state IN ('queued','running','waiting','completed','completed_with_failures')),
 cursor TEXT NOT NULL DEFAULT '',
 total BIGINT NOT NULL DEFAULT 0,
 queued BIGINT NOT NULL DEFAULT 0,
 succeeded BIGINT NOT NULL DEFAULT 0,
 failed BIGINT NOT NULL DEFAULT 0,
 unknown_count BIGINT NOT NULL DEFAULT 0,
 river_job_id BIGINT UNIQUE,
 started_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 completed_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX outbound_material_one_daily_round ON outbound_material_refresh_rounds(local_date) WHERE round_kind='daily';
ALTER TABLE outbound_material_preparations ADD CONSTRAINT outbound_material_refresh_round_fk FOREIGN KEY(refresh_round_id) REFERENCES outbound_material_refresh_rounds(id);

CREATE TABLE outbound_material_refresh_items (
 round_id BIGINT NOT NULL REFERENCES outbound_material_refresh_rounds(id),
 cache_key_digest TEXT NOT NULL,
 preparation_effect_id TEXT,
 state TEXT NOT NULL CHECK(state IN ('queued','executed','retryable_failed','outcome_unknown','final_failed')),
 failure_code TEXT NOT NULL DEFAULT '',
 source_count BIGINT NOT NULL DEFAULT 1 CHECK(source_count>0),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(round_id,cache_key_digest)
);
CREATE INDEX outbound_material_refresh_effect ON outbound_material_refresh_items(preparation_effect_id) WHERE preparation_effect_id IS NOT NULL;
