CREATE TABLE hxc_registration_coverage (
 projection_id BIGINT NOT NULL REFERENCES hxc_dashboard_versions(id),
 customer_id BIGINT NOT NULL REFERENCES customers(id),
 state TEXT NOT NULL CHECK(state IN ('registered','unregistered','unknown')),
 PRIMARY KEY(projection_id,customer_id)
);
COMMENT ON TABLE hxc_registration_coverage IS 'Version-bound Identity Owner coverage of a complete trusted HXC snapshot. Missing rows are unknown.';
