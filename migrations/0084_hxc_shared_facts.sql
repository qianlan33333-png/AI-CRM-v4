-- Owner: hxcdashboard.
-- Extends immutable published HXC projections with the legacy membership and
-- usage facts required by Product and Segment. Existing generations predate
-- these source reads and remain explicitly unavailable.

ALTER TABLE hxc_dashboard_versions
    ADD COLUMN shared_facts_available BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE hxc_dashboard_rows
    ADD COLUMN formally_logged_in BOOLEAN,
    ADD COLUMN formal_login_at TIMESTAMPTZ,
    ADD COLUMN has_token_usage BOOLEAN,
    ADD COLUMN learning_plan_found BOOLEAN,
    ADD COLUMN learning_plan_status TEXT,
    ADD COLUMN learning_plan_current BIGINT CHECK (learning_plan_current >= 0),
    ADD COLUMN learning_plan_total BIGINT CHECK (learning_plan_total >= 0),
    ADD COLUMN card_open_count_7d BIGINT CHECK (card_open_count_7d >= 0),
    ADD COLUMN card_last_opened_at TIMESTAMPTZ,
    ADD COLUMN membership_record_found BOOLEAN,
    ADD COLUMN is_member BOOLEAN,
    ADD COLUMN membership_source TEXT,
    ADD COLUMN membership_status TEXT,
    ADD COLUMN membership_expires_at TIMESTAMPTZ;

CREATE INDEX hxc_dashboard_rows_customer_shared_facts_idx
    ON hxc_dashboard_rows (projection_id, customer_id)
    WHERE customer_id IS NOT NULL;
