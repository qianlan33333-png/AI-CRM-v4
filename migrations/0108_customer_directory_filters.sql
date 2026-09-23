-- Owners remain unchanged: Customer owns local assignees; WeCom owns the
-- completed observation projection. These indexes support the bounded,
-- exact-ID directory filters without joining identities or provider tables.
CREATE INDEX customer_local_owners_staff_customer_idx
    ON customer_local_owners(staff_id, customer_id);

CREATE INDEX wecom_customer_tag_observations_filter_idx
    ON wecom_customer_tag_observations(provider_tag_id, customer_id)
    WHERE provider_tag_type = 1 AND observation_status = 'active';
