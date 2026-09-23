-- External Effects manual controls used to carry an administrator projection
-- only. Payment's scheduled commission due check is a distinct authenticated
-- system action, not a disguised employee. Retain the human projection and
-- permit exactly that named system subject for queued profit-sharing cancels.

ALTER TABLE external_effect_operation_receipts
  DROP CONSTRAINT external_effect_manual_control_requires_actor;

ALTER TABLE external_effect_operation_receipts
  ADD COLUMN actor_kind TEXT,
  ADD COLUMN actor_ref TEXT,
  ADD CONSTRAINT external_effect_operation_receipts_actor_shape CHECK (
    (actor_admin_user_id IS NULL AND actor_kind IS NULL AND actor_ref IS NULL) OR
    (actor_admin_user_id IS NOT NULL AND actor_admin_user_id > 0 AND actor_kind IS NULL AND actor_ref IS NULL) OR
    (actor_admin_user_id IS NULL AND actor_kind = 'system' AND actor_ref = 'system:payment_profit_sharing_due')
  ),
  ADD CONSTRAINT external_effect_manual_control_requires_principal CHECK (
    operation NOT IN ('retry','cancel','reconcile') OR
    (actor_admin_user_id IS NOT NULL AND actor_admin_user_id > 0 AND actor_kind IS NULL AND actor_ref IS NULL) OR
    (operation = 'cancel' AND actor_admin_user_id IS NULL AND actor_kind = 'system' AND actor_ref = 'system:payment_profit_sharing_due')
  );
