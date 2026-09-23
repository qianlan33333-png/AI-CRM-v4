-- Owners: internal/wecom and internal/customer.
-- Forward-only: disabling the feature leaves observations and safe timeline
-- facts intact. No phone, external_userid, unionid or chat body is stored.

-- Owner: internal/wecom. Provider identifiers stay inside the WeCom domain
-- and are never returned by the Customer HTTP API.
CREATE TABLE wecom_customer_owner_observations (
    customer_id BIGINT NOT NULL REFERENCES customers(id) ON DELETE RESTRICT,
    corp_scope TEXT NOT NULL CHECK (left(corp_scope, 11) = 'wecom-corp:'),
    employee_id TEXT NOT NULL CHECK (
        employee_id = btrim(employee_id)
        AND char_length(employee_id) BETWEEN 1 AND 1024
        AND employee_id !~ '[[:cntrl:]]'
    ),
    relationship_status TEXT NOT NULL DEFAULT 'active' CHECK (relationship_status IN ('active', 'stale')),
    last_seen_run_id BIGINT NOT NULL REFERENCES wecom_customer_sync_runs(id) ON DELETE RESTRICT,
    observed_at TIMESTAMPTZ NOT NULL,
    stale_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (customer_id, corp_scope, employee_id),
    CONSTRAINT wecom_customer_owner_observations_stale CHECK (
        (relationship_status = 'stale' AND stale_at IS NOT NULL)
        OR (relationship_status = 'active' AND stale_at IS NULL)
    )
);
CREATE INDEX wecom_customer_owner_observations_customer_idx
    ON wecom_customer_owner_observations(customer_id, relationship_status, observed_at DESC);
CREATE INDEX wecom_customer_owner_observations_run_idx
    ON wecom_customer_owner_observations(last_seen_run_id, customer_id);

CREATE TABLE wecom_customer_tag_observations (
    customer_id BIGINT NOT NULL REFERENCES customers(id) ON DELETE RESTRICT,
    corp_scope TEXT NOT NULL CHECK (left(corp_scope, 11) = 'wecom-corp:'),
    employee_id TEXT NOT NULL CHECK (
        employee_id = btrim(employee_id)
        AND char_length(employee_id) BETWEEN 1 AND 1024
        AND employee_id !~ '[[:cntrl:]]'
    ),
    provider_tag_id TEXT NOT NULL CHECK (
        provider_tag_id = btrim(provider_tag_id)
        AND char_length(provider_tag_id) BETWEEN 1 AND 128
        AND provider_tag_id !~ '[[:cntrl:]]'
    ),
    provider_tag_type SMALLINT NOT NULL DEFAULT 1 CHECK (provider_tag_type BETWEEN 1 AND 2),
    observed_name TEXT NOT NULL DEFAULT '' CHECK (
        observed_name = btrim(observed_name)
        AND char_length(observed_name) <= 200
        AND observed_name !~ '[[:cntrl:]]'
    ),
    observation_status TEXT NOT NULL DEFAULT 'active' CHECK (observation_status IN ('active', 'stale')),
    last_seen_run_id BIGINT NOT NULL REFERENCES wecom_customer_sync_runs(id) ON DELETE RESTRICT,
    observed_at TIMESTAMPTZ NOT NULL,
    stale_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (customer_id, corp_scope, employee_id, provider_tag_id),
    CONSTRAINT wecom_customer_tag_observations_stale CHECK (
        (observation_status = 'stale' AND stale_at IS NOT NULL)
        OR (observation_status = 'active' AND stale_at IS NULL)
    )
);
CREATE INDEX wecom_customer_tag_observations_customer_idx
    ON wecom_customer_tag_observations(customer_id, observation_status, observed_at DESC);
CREATE INDEX wecom_customer_tag_observations_run_idx
    ON wecom_customer_tag_observations(last_seen_run_id, customer_id);

-- Owner: internal/customer. Only display-safe event summaries are accepted.
CREATE TABLE customer_timeline_projection (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    customer_id BIGINT NOT NULL REFERENCES customers(id) ON DELETE RESTRICT,
    source_domain TEXT NOT NULL CHECK (
        source_domain ~ '^[a-z][a-z0-9_]{0,63}$'
    ),
    source_event_id TEXT NOT NULL CHECK (
        source_event_id = btrim(source_event_id)
        AND char_length(source_event_id) BETWEEN 1 AND 160
        AND source_event_id !~ '[[:cntrl:]]'
    ),
    event_type TEXT NOT NULL CHECK (
        event_type ~ '^[a-z][a-z0-9_.]{0,95}$'
    ),
    title TEXT NOT NULL CHECK (
        title = btrim(title)
        AND char_length(title) BETWEEN 1 AND 200
        AND title !~ '[[:cntrl:]]'
    ),
    occurred_at TIMESTAMPTZ NOT NULL,
    projected_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT customer_timeline_projection_source_unique UNIQUE(source_domain, source_event_id)
);
CREATE INDEX customer_timeline_projection_page_idx
    ON customer_timeline_projection(customer_id, occurred_at DESC, id DESC);
CREATE INDEX customer_timeline_projection_type_idx
    ON customer_timeline_projection(customer_id, event_type, occurred_at DESC, id DESC);
