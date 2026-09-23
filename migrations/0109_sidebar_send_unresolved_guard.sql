-- Owners: internal/outbound and internal/sidebar.
-- Existing releases can already contain more than one unresolved row for the
-- same resource, so this is intentionally a lookup index rather than a
-- uniqueness assertion. New acceptance is serialized by the Outbound UoW and
-- conservatively replays an existing unresolved intent.
CREATE INDEX IF NOT EXISTS outbound_sidebar_send_intents_unresolved_lookup
    ON outbound_sidebar_send_intents(customer_id, employee_digest, resource_kind, resource_id, id)
    WHERE state IN ('accepted','queued','outcome_unknown');
