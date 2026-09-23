-- HXC dashboard owns immutable current-state projections and durable refresh receipts.
CREATE TABLE hxc_dashboard_versions (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    rule_version TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('published','superseded')),
    projection_as_of TIMESTAMPTZ NOT NULL,
    source_watermark TIMESTAMPTZ,
    source_digest BYTEA NOT NULL CHECK (octet_length(source_digest) = 32),
    projection_digest BYTEA NOT NULL CHECK (octet_length(projection_digest) = 32),
    total_count BIGINT NOT NULL CHECK (total_count >= 0),
    active_used_count BIGINT NOT NULL CHECK (active_used_count >= 0),
    active_unused_count BIGINT NOT NULL CHECK (active_unused_count >= 0),
    registered_no_active_membership_count BIGINT NOT NULL CHECK (registered_no_active_membership_count >= 0),
    matched_count BIGINT NOT NULL CHECK (matched_count >= 0),
    unmatched_count BIGINT NOT NULL CHECK (unmatched_count >= 0),
    conflict_count BIGINT NOT NULL CHECK (conflict_count >= 0),
    published_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (total_count = active_used_count + active_unused_count + registered_no_active_membership_count),
    CHECK (total_count = matched_count + unmatched_count + conflict_count)
);

CREATE UNIQUE INDEX hxc_dashboard_one_published_idx ON hxc_dashboard_versions ((status)) WHERE status = 'published';

CREATE TABLE hxc_dashboard_rows (
    projection_id BIGINT NOT NULL REFERENCES hxc_dashboard_versions(id) ON DELETE CASCADE,
    subject_digest BYTEA NOT NULL CHECK (octet_length(subject_digest) = 32),
    user_ref TEXT NOT NULL CHECK (user_ref ~ '^HXC-[0-9a-f]{12}$'),
    stage TEXT NOT NULL CHECK (stage IN ('active_used','active_unused','registered_no_active_membership')),
    subscription_tier TEXT NOT NULL,
    subscription_expires_at TIMESTAMPTZ,
    monthly_chat_quota BIGINT NOT NULL CHECK (monthly_chat_quota >= 0),
    current_period_used BIGINT NOT NULL CHECK (current_period_used >= 0),
    consultation_limit BIGINT NOT NULL CHECK (consultation_limit >= 0),
    consultation_used BIGINT NOT NULL CHECK (consultation_used >= 0),
    membership_attribution TEXT NOT NULL CHECK (membership_attribution IN ('user_id','unique_phone','none')),
    sessions_7d BIGINT NOT NULL CHECK (sessions_7d >= 0),
    sessions_30d BIGINT NOT NULL CHECK (sessions_30d >= 0),
    sessions_total BIGINT NOT NULL CHECK (sessions_total >= 0),
    user_messages_7d BIGINT NOT NULL CHECK (user_messages_7d >= 0),
    user_messages_30d BIGINT NOT NULL CHECK (user_messages_30d >= 0),
    user_messages_total BIGINT NOT NULL CHECK (user_messages_total >= 0),
    capability_usage JSONB NOT NULL DEFAULT '{}'::jsonb,
    last_used_at TIMESTAMPTZ,
    last_capability TEXT,
    business_stage TEXT,
    main_line_type TEXT,
    user_segment TEXT,
    focus_topics JSONB NOT NULL DEFAULT '[]'::jsonb,
    pain_tag TEXT,
    customer_id BIGINT REFERENCES customers(id),
    identity_state TEXT NOT NULL CHECK (identity_state IN ('matched','unmatched','conflict')),
    source_updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (projection_id, subject_digest)
);

CREATE INDEX hxc_dashboard_rows_stage_idx ON hxc_dashboard_rows (projection_id, stage, subject_digest);
CREATE INDEX hxc_dashboard_rows_tier_idx ON hxc_dashboard_rows (projection_id, subscription_tier, subject_digest);
CREATE INDEX hxc_dashboard_rows_capability_idx ON hxc_dashboard_rows (projection_id, last_capability, subject_digest);
CREATE INDEX hxc_dashboard_rows_business_idx ON hxc_dashboard_rows (projection_id, business_stage, subject_digest);
CREATE INDEX hxc_dashboard_rows_segment_idx ON hxc_dashboard_rows (projection_id, user_segment, subject_digest);
CREATE INDEX hxc_dashboard_rows_identity_idx ON hxc_dashboard_rows (projection_id, identity_state, subject_digest);
CREATE INDEX hxc_dashboard_rows_last_used_idx ON hxc_dashboard_rows (projection_id, last_used_at DESC NULLS LAST, subject_digest);

CREATE TABLE hxc_dashboard_refresh_runs (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    run_key TEXT NOT NULL UNIQUE,
    request_digest BYTEA NOT NULL CHECK (octet_length(request_digest) = 32),
    trigger TEXT NOT NULL CHECK (trigger IN ('manual','scheduled','initial')),
    status TEXT NOT NULL CHECK (status IN ('queued','running','publishing','succeeded','failed')),
    requested_by BIGINT,
    projection_id BIGINT REFERENCES hxc_dashboard_versions(id),
    source_count BIGINT NOT NULL DEFAULT 0 CHECK (source_count >= 0),
    processed_count BIGINT NOT NULL DEFAULT 0 CHECK (processed_count >= 0),
    error_code TEXT,
    version BIGINT NOT NULL DEFAULT 1,
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX hxc_dashboard_one_active_refresh_idx
    ON hxc_dashboard_refresh_runs ((true)) WHERE status IN ('queued','running','publishing');
