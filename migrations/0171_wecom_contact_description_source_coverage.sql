-- Owner: internal/wecom. Retain one safe, immutable-in-scope field-presence
-- fact for every distinct customer-corp-employee observation in a sync run.
-- It is deliberately separate from current owner observations because a later
-- directory run may replace the current relationship state while an earlier
-- run still has pending Outbound effects. Raw description text remains outside
-- this projection.
CREATE TABLE wecom_contact_description_source_observations (
    source_run_id BIGINT NOT NULL REFERENCES wecom_customer_sync_runs(id) ON DELETE RESTRICT,
    customer_id BIGINT NOT NULL REFERENCES customers(id) ON DELETE RESTRICT,
    corp_scope TEXT NOT NULL CHECK (left(corp_scope, 11) = 'wecom-corp:'),
    employee_id TEXT NOT NULL CHECK (
        employee_id = btrim(employee_id)
        AND char_length(employee_id) BETWEEN 1 AND 1024
        AND employee_id !~ '[[:cntrl:]]'
    ),
    description_projected BOOLEAN NOT NULL DEFAULT false,
    observed_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY(source_run_id, customer_id, corp_scope, employee_id)
);
CREATE INDEX wecom_contact_description_source_observations_coverage_idx
    ON wecom_contact_description_source_observations(source_run_id, description_projected);
