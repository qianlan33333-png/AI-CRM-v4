-- Owner: Segment. All customer references are canonical IDs resolved via ports.
CREATE TABLE segment_core_products (
    id SMALLINT PRIMARY KEY CHECK (id BETWEEN 1 AND 5),
    package_id BIGINT NOT NULL UNIQUE REFERENCES segment_audience_packages(id),
    name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 120),
    description TEXT NOT NULL CHECK (length(description) BETWEEN 1 AND 16000),
    ai_context TEXT NOT NULL DEFAULT '' CHECK (length(ai_context) <= 16000),
    product_reference TEXT NOT NULL DEFAULT '' CHECK (length(product_reference) <= 120),
    enabled BOOLEAN NOT NULL DEFAULT false,
    version BIGINT NOT NULL CHECK (version > 0),
    updated_at TIMESTAMPTZ NOT NULL
);
CREATE TABLE segment_core_prompt_versions (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    body TEXT NOT NULL CHECK (length(body) BETWEEN 1 AND 16000),
    created_by BIGINT NOT NULL CHECK (created_by > 0),
    created_at TIMESTAMPTZ NOT NULL
);
CREATE TABLE segment_core_prompt_state (
    singleton BOOLEAN PRIMARY KEY DEFAULT true CHECK (singleton),
    draft TEXT NOT NULL DEFAULT '' CHECK (length(draft) <= 16000),
    published_id BIGINT REFERENCES segment_core_prompt_versions(id),
    version BIGINT NOT NULL DEFAULT 1,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO segment_core_prompt_state(singleton) VALUES(true);
CREATE TABLE segment_core_assignments (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    customer_id BIGINT NOT NULL CHECK (customer_id > 0),
    core_product_id SMALLINT NOT NULL REFERENCES segment_core_products(id),
    package_id BIGINT NOT NULL REFERENCES segment_audience_packages(id),
    source TEXT NOT NULL CHECK (source IN ('ai','manual')),
    reason TEXT NOT NULL CHECK (length(reason) BETWEEN 1 AND 8000),
    evidence TEXT NOT NULL DEFAULT '' CHECK (length(evidence) <= 16000),
    prompt_version BIGINT REFERENCES segment_core_prompt_versions(id),
    entered_at TIMESTAMPTZ NOT NULL,
    ended_at TIMESTAMPTZ,
    end_reason TEXT NOT NULL DEFAULT '',
    CHECK (ended_at IS NULL OR ended_at >= entered_at)
);
CREATE UNIQUE INDEX segment_core_one_current_customer ON segment_core_assignments(customer_id) WHERE ended_at IS NULL;
CREATE INDEX segment_core_assignment_history ON segment_core_assignments(package_id,customer_id,id);
CREATE TABLE segment_core_purchases (
    customer_id BIGINT NOT NULL CHECK (customer_id > 0),
    core_product_id SMALLINT NOT NULL REFERENCES segment_core_products(id),
    recorded_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY(customer_id,core_product_id)
);
CREATE TABLE segment_core_pushes (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    source TEXT NOT NULL CHECK (length(source) BETWEEN 1 AND 120),
    push_id TEXT NOT NULL CHECK (length(push_id) BETWEEN 1 AND 128),
    customer_id BIGINT NOT NULL CHECK (customer_id > 0),
    package_id BIGINT NOT NULL REFERENCES segment_audience_packages(id),
    assignment_id BIGINT REFERENCES segment_core_assignments(id),
    materials JSONB NOT NULL CHECK (jsonb_typeof(materials)='array'),
    occurred_at TIMESTAMPTZ NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('reported','success','failed','unknown')),
    status_version BIGINT NOT NULL CHECK (status_version > 0),
    recorded_at TIMESTAMPTZ NOT NULL,
    UNIQUE(source,push_id,customer_id)
);
CREATE INDEX segment_core_push_member ON segment_core_pushes(package_id,customer_id,occurred_at DESC,id DESC);

ALTER TABLE segment_audience_audit_events DROP CONSTRAINT segment_audience_audit_events_resource_kind_check;
ALTER TABLE segment_audience_audit_events ADD CONSTRAINT segment_audience_audit_events_resource_kind_check
 CHECK(resource_kind IN ('group','package','configuration','refresh_run','snapshot','webhook_receipt','binding','sender_set','schedule','member_event_batch','member_exit_batch','core_operations','core_recommendation'));
ALTER TABLE segment_audience_outbox DROP CONSTRAINT segment_audience_outbox_aggregate_kind_check;
ALTER TABLE segment_audience_outbox ADD CONSTRAINT segment_audience_outbox_aggregate_kind_check
 CHECK(aggregate_kind IN ('group','package','configuration','refresh_run','snapshot','webhook_receipt','binding','sender_set','schedule','member_event_batch','member_exit_batch','core_operations','core_recommendation'));

CREATE TABLE segment_core_recommendations (
 id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 customer_id BIGINT NOT NULL CHECK(customer_id>0),
 expected_epoch BIGINT NOT NULL,
 actor_id BIGINT NOT NULL CHECK(actor_id>0),
 preview BOOLEAN NOT NULL,
 prompt_version BIGINT NOT NULL,
 products JSONB NOT NULL,
 dispatch JSONB NOT NULL,
 effect_id TEXT UNIQUE,
 state TEXT NOT NULL DEFAULT 'accepted',
 reason TEXT NOT NULL DEFAULT '',
 evidence TEXT NOT NULL DEFAULT '',
 chosen_product_id BIGINT,
 created_at TIMESTAMPTZ NOT NULL,
 completed_at TIMESTAMPTZ
);
CREATE INDEX segment_core_recommendation_customer_time ON segment_core_recommendations(customer_id,created_at DESC);
