-- Owner: aiassistant. Operation-cycle keys are opaque references verified only
-- through the OperationCycle stable read port at command time; no cross-domain
-- foreign key or write is introduced.  All changes are forward-only because
-- review content, audit facts and idempotency receipts are retained.

ALTER TABLE ai_assistant_excel_imports
  ADD COLUMN operation_cycle_strategy_key TEXT,
  ADD COLUMN source_origin TEXT NOT NULL DEFAULT 'legacy_snapshot',
  ADD COLUMN content_revision INTEGER NOT NULL DEFAULT 1,
  ADD COLUMN created_at TIMESTAMPTZ;

UPDATE ai_assistant_excel_imports import
SET created_at = plan.created_at
FROM ai_assistant_plans plan
WHERE plan.id = import.plan_id AND import.created_at IS NULL;

ALTER TABLE ai_assistant_excel_imports
  ALTER COLUMN created_at SET NOT NULL,
  ALTER COLUMN created_at SET DEFAULT clock_timestamp(),
  ADD CONSTRAINT ck_ai_assistant_excel_import_strategy_key CHECK (
    operation_cycle_strategy_key IS NULL OR operation_cycle_strategy_key ~ '^[A-Za-z0-9][A-Za-z0-9_.:-]{0,119}$'
  ),
  ADD CONSTRAINT ck_ai_assistant_excel_import_source_origin CHECK (source_origin IN ('excel', 'legacy_snapshot')),
  ADD CONSTRAINT ck_ai_assistant_excel_import_revision CHECK (content_revision > 0);
CREATE INDEX ai_assistant_excel_import_strategy_created
  ON ai_assistant_excel_imports(operation_cycle_strategy_key, created_at DESC, plan_id DESC)
  WHERE operation_cycle_strategy_key IS NOT NULL;
CREATE INDEX ai_assistant_excel_import_digest_created
  ON ai_assistant_excel_imports(file_digest, created_at DESC, plan_id DESC);

ALTER TABLE ai_assistant_plan_recipients
  ADD COLUMN excel_batch_content_revision INTEGER,
  ADD COLUMN excel_segment TEXT NOT NULL DEFAULT '',
  ADD COLUMN excel_excluded BOOLEAN NOT NULL DEFAULT false,
  ADD CONSTRAINT ck_ai_assistant_excel_segment CHECK (excel_segment IN ('', 'A', 'B', 'C', 'D')),
  ADD CONSTRAINT ck_ai_assistant_excel_batch_revision CHECK (excel_batch_content_revision IS NULL OR excel_batch_content_revision > 0);

UPDATE ai_assistant_plan_recipients recipient
SET excel_batch_content_revision = import.content_revision
FROM ai_assistant_excel_imports import
WHERE import.plan_id = recipient.plan_id;

CREATE INDEX ai_assistant_excel_recipient_current
  ON ai_assistant_plan_recipients(plan_id, excel_batch_content_revision, id)
  WHERE excel_batch_content_revision IS NOT NULL;

CREATE TABLE ai_assistant_excel_batch_versions (
  plan_id BIGINT NOT NULL REFERENCES ai_assistant_plans(id) ON DELETE RESTRICT,
  content_revision INTEGER NOT NULL,
  file_digest TEXT NOT NULL,
  cover_digest TEXT NOT NULL DEFAULT '',
  created_by BIGINT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (plan_id, content_revision),
  CONSTRAINT ck_ai_assistant_excel_batch_versions_revision CHECK (content_revision > 0),
  CONSTRAINT ck_ai_assistant_excel_batch_versions_file_digest CHECK (length(file_digest) BETWEEN 8 AND 200),
  CONSTRAINT ck_ai_assistant_excel_batch_versions_cover_digest CHECK (cover_digest = '' OR cover_digest ~ '^sha256:[0-9a-f]{64}$')
);

-- A cover can change within one content revision. Keep every effective cover
-- reference append-only, so historic version readbacks never infer today's
-- cover from mutable recipient content.
CREATE TABLE ai_assistant_excel_batch_version_covers (
  plan_id BIGINT NOT NULL REFERENCES ai_assistant_plans(id) ON DELETE RESTRICT,
  content_revision INTEGER NOT NULL,
  cover_digest TEXT NOT NULL DEFAULT '',
  created_by BIGINT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (plan_id, content_revision, cover_digest, created_at),
  CONSTRAINT ck_ai_assistant_excel_batch_version_covers_revision CHECK (content_revision > 0),
  CONSTRAINT ck_ai_assistant_excel_batch_version_covers_digest CHECK (cover_digest = '' OR cover_digest ~ '^sha256:[0-9a-f]{64}$')
);
CREATE OR REPLACE FUNCTION ai_assistant_excel_batch_version_covers_reject_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'AI Assistant Excel batch cover history is append-only'; END $$;
CREATE TRIGGER ai_assistant_excel_batch_version_covers_append_only
  BEFORE UPDATE OR DELETE OR TRUNCATE ON ai_assistant_excel_batch_version_covers
  FOR EACH STATEMENT EXECUTE FUNCTION ai_assistant_excel_batch_version_covers_reject_mutation();

-- Existing pre-0124 imports may already have had their legacy uniform cover
-- applied. Capture that immutable content reference before enabling the
-- append-only guards; an absent or malformed legacy value stays empty rather
-- than being promoted into a trusted cover digest.
INSERT INTO ai_assistant_excel_batch_versions(plan_id, content_revision, file_digest, cover_digest, created_by, created_at)
SELECT import.plan_id, import.content_revision, import.file_digest,
  COALESCE((SELECT CASE WHEN content.content_payload->1->'excel_card'->>'cover_digest' ~ '^sha256:[0-9a-f]{64}$'
                        THEN content.content_payload->1->'excel_card'->>'cover_digest' ELSE '' END
            FROM ai_assistant_plan_recipients recipient
            JOIN ai_assistant_content_versions content ON content.id=recipient.current_content_version_id
            WHERE recipient.plan_id=import.plan_id ORDER BY recipient.id LIMIT 1), ''),
  plan.created_by, import.created_at
FROM ai_assistant_excel_imports import
JOIN ai_assistant_plans plan ON plan.id = import.plan_id
ON CONFLICT DO NOTHING;
INSERT INTO ai_assistant_excel_batch_version_covers(plan_id, content_revision, cover_digest, created_by, created_at)
SELECT import.plan_id, import.content_revision,
  COALESCE((SELECT CASE WHEN content.content_payload->1->'excel_card'->>'cover_digest' ~ '^sha256:[0-9a-f]{64}$'
                        THEN content.content_payload->1->'excel_card'->>'cover_digest' ELSE '' END
            FROM ai_assistant_plan_recipients recipient
            JOIN ai_assistant_content_versions content ON content.id=recipient.current_content_version_id
            WHERE recipient.plan_id=import.plan_id ORDER BY recipient.id LIMIT 1), ''),
  plan.created_by, import.created_at
FROM ai_assistant_excel_imports import
JOIN ai_assistant_plans plan ON plan.id = import.plan_id
ON CONFLICT DO NOTHING;

CREATE OR REPLACE FUNCTION ai_assistant_excel_batch_versions_reject_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'AI Assistant Excel batch versions are append-only'; END $$;
CREATE TRIGGER ai_assistant_excel_batch_versions_append_only
  BEFORE UPDATE OR DELETE OR TRUNCATE ON ai_assistant_excel_batch_versions
  FOR EACH STATEMENT EXECUTE FUNCTION ai_assistant_excel_batch_versions_reject_mutation();

-- Compatibility bridge owner: aiassistant. The release installer can restore a
-- pre-0124 binary after this migration has committed. That binary inserts only
-- (batch_key, plan_id, file_digest), so preserve a complete legacy snapshot in
-- the new read model without changing review, approval, intent, or effect
-- state. Remove these triggers only after every retained rollback release is
-- 0124-aware and the old import endpoint has been retired.
CREATE OR REPLACE FUNCTION ai_assistant_excel_legacy_import_seed_version() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  plan_actor BIGINT;
BEGIN
  IF NEW.source_origin <> 'legacy_snapshot' OR NEW.operation_cycle_strategy_key IS NOT NULL THEN
    RETURN NEW;
  END IF;

  SELECT created_by INTO plan_actor FROM ai_assistant_plans WHERE id=NEW.plan_id;
  INSERT INTO ai_assistant_excel_batch_versions(plan_id,content_revision,file_digest,cover_digest,created_by,created_at)
    VALUES(NEW.plan_id,NEW.content_revision,NEW.file_digest,'',plan_actor,NEW.created_at)
    ON CONFLICT DO NOTHING;
  INSERT INTO ai_assistant_excel_batch_version_covers(plan_id,content_revision,cover_digest,created_by,created_at)
    VALUES(NEW.plan_id,NEW.content_revision,'',plan_actor,NEW.created_at)
    ON CONFLICT DO NOTHING;
  UPDATE ai_assistant_plan_recipients
    SET excel_batch_content_revision=NEW.content_revision
    WHERE plan_id=NEW.plan_id AND excel_batch_content_revision IS NULL;
  RETURN NEW;
END $$;
CREATE TRIGGER ai_assistant_excel_legacy_import_seed_version
  AFTER INSERT ON ai_assistant_excel_imports
  FOR EACH ROW EXECUTE FUNCTION ai_assistant_excel_legacy_import_seed_version();

-- A rollback binary can still upload a cover after its legacy import row has
-- been seeded. Follow the recipient's effective immutable content reference,
-- rather than content insertion: a later A→B→A reversion can deliberately
-- reuse the earlier immutable A content row. Excel-controlled rows use
-- source_origin 'excel' and never enter this compatibility path.
CREATE OR REPLACE FUNCTION ai_assistant_excel_legacy_recipient_seed_cover() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  legacy_plan_id BIGINT;
  legacy_revision INTEGER;
  cover TEXT;
  cover_actor BIGINT;
BEGIN
  IF NEW.current_content_version_id IS NOT DISTINCT FROM OLD.current_content_version_id THEN
    RETURN NEW;
  END IF;
  SELECT import.plan_id,import.content_revision,
      COALESCE(content.content_payload->1->'excel_card'->>'cover_digest',''),content.created_by
    INTO legacy_plan_id,legacy_revision,cover,cover_actor
    FROM ai_assistant_excel_imports import
    JOIN ai_assistant_content_versions content ON content.id=NEW.current_content_version_id
    WHERE import.plan_id=NEW.plan_id
      AND import.source_origin='legacy_snapshot'
      AND import.operation_cycle_strategy_key IS NULL;
  IF NOT FOUND THEN
    RETURN NEW;
  END IF;
  IF cover !~ '^sha256:[0-9a-f]{64}$' THEN
    RETURN NEW;
  END IF;
  INSERT INTO ai_assistant_excel_batch_version_covers(plan_id,content_revision,cover_digest,created_by,created_at)
    VALUES(legacy_plan_id,legacy_revision,cover,cover_actor,NEW.updated_at)
    ON CONFLICT DO NOTHING;
  RETURN NEW;
END $$;
CREATE TRIGGER ai_assistant_excel_legacy_recipient_seed_cover
  AFTER UPDATE OF current_content_version_id ON ai_assistant_plan_recipients
  FOR EACH ROW EXECUTE FUNCTION ai_assistant_excel_legacy_recipient_seed_cover();
