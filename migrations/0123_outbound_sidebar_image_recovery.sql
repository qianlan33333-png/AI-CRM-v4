-- Owner: outbound. Preserve unknown history; explicit abandonment is not failure.
ALTER TABLE outbound_sidebar_image_preparations DROP CONSTRAINT outbound_sidebar_image_preparations_state_check;
ALTER TABLE outbound_sidebar_image_preparations ADD CONSTRAINT outbound_sidebar_image_preparations_state_check
 CHECK(state IN ('accepted','queued','executed','retryable_failed','outcome_unknown','final_failed','reconciled'));
ALTER TABLE outbound_sidebar_image_events ADD COLUMN diagnostics JSONB NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE outbound_sidebar_image_events ADD COLUMN actor_admin_user_id BIGINT CHECK(actor_admin_user_id>0);
