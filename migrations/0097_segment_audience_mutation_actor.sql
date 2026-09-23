-- Owner: Segment/Audience.
-- Machine Open Platform subjects remain distinct from administrative staff IDs.
-- This is additive: legacy positive administrative IDs are retained only as a
-- nullable compatibility projection, while the canonical actor is kind/ref.

DO $$
BEGIN
  IF EXISTS (
    SELECT 1 FROM segment_audience_operation_receipts
    WHERE actor_scope !~ '^admin:[1-9][0-9]*$'
  ) THEN
    RAISE EXCEPTION 'segment receipt has noncanonical legacy actor scope';
  END IF;
END
$$;

ALTER TABLE segment_audience_groups
  ALTER COLUMN created_by DROP NOT NULL,
  ALTER COLUMN updated_by DROP NOT NULL,
  ADD COLUMN created_actor_kind TEXT,
  ADD COLUMN created_actor_ref TEXT,
  ADD COLUMN updated_actor_kind TEXT,
  ADD COLUMN updated_actor_ref TEXT;
UPDATE segment_audience_groups
  SET created_actor_kind='admin', created_actor_ref='admin:' || created_by::text,
      updated_actor_kind='admin', updated_actor_ref='admin:' || updated_by::text;
ALTER TABLE segment_audience_groups
  ALTER COLUMN created_actor_kind SET NOT NULL,
  ALTER COLUMN created_actor_ref SET NOT NULL,
  ALTER COLUMN updated_actor_kind SET NOT NULL,
  ALTER COLUMN updated_actor_ref SET NOT NULL,
  ADD CONSTRAINT segment_audience_groups_created_actor_check CHECK (
    (created_actor_kind='admin' AND created_by IS NOT NULL AND created_actor_ref=('admin:' || created_by::text)) OR
    (created_actor_kind='machine' AND created_by IS NULL AND created_actor_ref ~ '^machine:[A-Za-z0-9._-]{1,160}$')
  ),
  ADD CONSTRAINT segment_audience_groups_updated_actor_check CHECK (
    (updated_actor_kind='admin' AND updated_by IS NOT NULL AND updated_actor_ref=('admin:' || updated_by::text)) OR
    (updated_actor_kind='machine' AND updated_by IS NULL AND updated_actor_ref ~ '^machine:[A-Za-z0-9._-]{1,160}$')
  );

ALTER TABLE segment_audience_packages
  ALTER COLUMN created_by DROP NOT NULL,
  ALTER COLUMN updated_by DROP NOT NULL,
  ADD COLUMN created_actor_kind TEXT,
  ADD COLUMN created_actor_ref TEXT,
  ADD COLUMN updated_actor_kind TEXT,
  ADD COLUMN updated_actor_ref TEXT;
UPDATE segment_audience_packages
  SET created_actor_kind='admin', created_actor_ref='admin:' || created_by::text,
      updated_actor_kind='admin', updated_actor_ref='admin:' || updated_by::text;
ALTER TABLE segment_audience_packages
  ALTER COLUMN created_actor_kind SET NOT NULL,
  ALTER COLUMN created_actor_ref SET NOT NULL,
  ALTER COLUMN updated_actor_kind SET NOT NULL,
  ALTER COLUMN updated_actor_ref SET NOT NULL,
  ADD CONSTRAINT segment_audience_packages_created_actor_check CHECK (
    (created_actor_kind='admin' AND created_by IS NOT NULL AND created_actor_ref=('admin:' || created_by::text)) OR
    (created_actor_kind='machine' AND created_by IS NULL AND created_actor_ref ~ '^machine:[A-Za-z0-9._-]{1,160}$')
  ),
  ADD CONSTRAINT segment_audience_packages_updated_actor_check CHECK (
    (updated_actor_kind='admin' AND updated_by IS NOT NULL AND updated_actor_ref=('admin:' || updated_by::text)) OR
    (updated_actor_kind='machine' AND updated_by IS NULL AND updated_actor_ref ~ '^machine:[A-Za-z0-9._-]{1,160}$')
  );

-- Configuration versions are immutable after insert. Temporarily disable only
-- their existing append-only trigger while this migration derives canonical
-- actor metadata from the legacy administrative projection; the transaction
-- restores the trigger before it commits.
ALTER TABLE segment_audience_configuration_versions DISABLE TRIGGER segment_audience_configuration_append_only;
ALTER TABLE segment_audience_configuration_versions
  ALTER COLUMN created_by DROP NOT NULL,
  ADD COLUMN created_actor_kind TEXT,
  ADD COLUMN created_actor_ref TEXT;
UPDATE segment_audience_configuration_versions
  SET created_actor_kind='admin', created_actor_ref='admin:' || created_by::text;
ALTER TABLE segment_audience_configuration_versions
  ALTER COLUMN created_actor_kind SET NOT NULL,
  ALTER COLUMN created_actor_ref SET NOT NULL,
  ADD CONSTRAINT segment_audience_configuration_actor_check CHECK (
    (created_actor_kind='admin' AND created_by IS NOT NULL AND created_actor_ref=('admin:' || created_by::text)) OR
    (created_actor_kind='machine' AND created_by IS NULL AND created_actor_ref ~ '^machine:[A-Za-z0-9._-]{1,160}$')
  );
ALTER TABLE segment_audience_configuration_versions ENABLE TRIGGER segment_audience_configuration_append_only;

ALTER TABLE segment_audience_automation_binding_versions DISABLE TRIGGER segment_audience_binding_append_only;
ALTER TABLE segment_audience_automation_binding_versions
  ALTER COLUMN created_by DROP NOT NULL,
  ADD COLUMN created_actor_kind TEXT,
  ADD COLUMN created_actor_ref TEXT;
UPDATE segment_audience_automation_binding_versions
  SET created_actor_kind='admin', created_actor_ref='admin:' || created_by::text;
ALTER TABLE segment_audience_automation_binding_versions
  ALTER COLUMN created_actor_kind SET NOT NULL,
  ALTER COLUMN created_actor_ref SET NOT NULL,
  ADD CONSTRAINT segment_audience_binding_actor_check CHECK (
    (created_actor_kind='admin' AND created_by IS NOT NULL AND created_actor_ref=('admin:' || created_by::text)) OR
    (created_actor_kind='machine' AND created_by IS NULL AND created_actor_ref ~ '^machine:[A-Za-z0-9._-]{1,160}$')
  );
ALTER TABLE segment_audience_automation_binding_versions ENABLE TRIGGER segment_audience_binding_append_only;

ALTER TABLE segment_audience_sender_sets DISABLE TRIGGER segment_audience_sender_sets_append_only;
ALTER TABLE segment_audience_sender_sets
  ALTER COLUMN created_by DROP NOT NULL,
  ADD COLUMN created_actor_kind TEXT,
  ADD COLUMN created_actor_ref TEXT;
UPDATE segment_audience_sender_sets
  SET created_actor_kind='admin', created_actor_ref='admin:' || created_by::text;
ALTER TABLE segment_audience_sender_sets
  ALTER COLUMN created_actor_kind SET NOT NULL,
  ALTER COLUMN created_actor_ref SET NOT NULL,
  ADD CONSTRAINT segment_audience_sender_set_actor_check CHECK (
    (created_actor_kind='admin' AND created_by IS NOT NULL AND created_actor_ref=('admin:' || created_by::text)) OR
    (created_actor_kind='machine' AND created_by IS NULL AND created_actor_ref ~ '^machine:[A-Za-z0-9._-]{1,160}$')
  );
ALTER TABLE segment_audience_sender_sets ENABLE TRIGGER segment_audience_sender_sets_append_only;

ALTER TABLE segment_audience_operation_receipts
  ADD COLUMN actor_kind TEXT,
  ADD COLUMN actor_ref TEXT;
UPDATE segment_audience_operation_receipts
  SET actor_kind='admin', actor_ref=actor_scope;
ALTER TABLE segment_audience_operation_receipts
  ALTER COLUMN actor_kind SET NOT NULL,
  ALTER COLUMN actor_ref SET NOT NULL,
  ADD CONSTRAINT segment_audience_receipt_actor_check CHECK (
    (actor_kind='admin' AND actor_scope=actor_ref AND actor_ref ~ '^admin:[1-9][0-9]*$') OR
    (actor_kind='machine' AND actor_scope=actor_ref AND actor_ref ~ '^machine:[A-Za-z0-9._-]{1,160}$')
  );

-- Audit events are append-only. This is the matching one-time canonical
-- metadata backfill, and the original append-only trigger is restored in the
-- same migration transaction before any application writer can observe it.
ALTER TABLE segment_audience_audit_events DISABLE TRIGGER segment_audience_audit_append_only;
ALTER TABLE segment_audience_audit_events
  ALTER COLUMN actor_id DROP NOT NULL,
  ADD COLUMN actor_kind TEXT,
  ADD COLUMN actor_ref TEXT;
UPDATE segment_audience_audit_events
  SET actor_kind='admin', actor_ref='admin:' || actor_id::text;
ALTER TABLE segment_audience_audit_events
  ALTER COLUMN actor_kind SET NOT NULL,
  ALTER COLUMN actor_ref SET NOT NULL,
  ADD CONSTRAINT segment_audience_audit_actor_check CHECK (
    (actor_kind='admin' AND actor_id IS NOT NULL AND actor_ref=('admin:' || actor_id::text)) OR
    (actor_kind='machine' AND actor_id IS NULL AND actor_ref ~ '^machine:[A-Za-z0-9._-]{1,160}$')
  );
ALTER TABLE segment_audience_audit_events ENABLE TRIGGER segment_audience_audit_append_only;
