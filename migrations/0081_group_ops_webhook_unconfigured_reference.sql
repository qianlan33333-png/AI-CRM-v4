-- Owner: internal/groupops. Forward-only repair for the local webhook
-- descriptor projection. An empty reference explicitly means "not
-- configured" and is shared by every plan; only configured opaque keys need
-- global uniqueness.
ALTER TABLE group_ops_plan_webhook_descriptors
    DROP CONSTRAINT IF EXISTS group_ops_plan_webhook_descriptors_reference_key;

CREATE UNIQUE INDEX group_ops_plan_webhook_descriptors_configured_reference_unique
    ON group_ops_plan_webhook_descriptors(reference)
    WHERE reference <> '';
