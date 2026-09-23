-- Owner: internal/tag. Completes the durable WeCom tag catalog read workflow.
-- Provider identifiers remain scoped to Tag-owned bindings and never become
-- customer identities or cross-domain keys.

ALTER TABLE tag_sync_receipts DROP CONSTRAINT tag_sync_receipts_state_check;
ALTER TABLE tag_sync_receipts
    ADD CONSTRAINT tag_sync_receipts_state_check CHECK (
        state IN (
            'reserved', 'queued', 'executed', 'outcome_unknown',
            'retryable_failed', 'final_failed', 'cancelled', 'reconciled'
        )
    ),
    ADD COLUMN group_count INTEGER NOT NULL DEFAULT 0 CHECK (group_count >= 0),
    ADD COLUMN tag_count INTEGER NOT NULL DEFAULT 0 CHECK (tag_count >= 0),
    ADD COLUMN completed_at TIMESTAMPTZ;

-- Historical successful effects already produced immutable observations but
-- older code left their receipts queued. Close those facts before enforcing
-- the global single-flight invariant.
UPDATE tag_sync_receipts receipt
SET state = 'executed',
    completed_at = observation.observed_at,
    group_count = jsonb_array_length(observation.snapshot->'groups'),
    tag_count = (
        SELECT COALESCE(sum(jsonb_array_length(group_value->'tags')), 0)::INTEGER
        FROM jsonb_array_elements(observation.snapshot->'groups') group_value
    )
FROM tag_provider_observations observation
WHERE observation.effect_id = receipt.effect_id
  AND receipt.state IN ('reserved', 'queued');

UPDATE tag_sync_receipts receipt
SET state = effect.state,
    completed_at = CASE
        WHEN effect.state IN ('final_failed', 'cancelled', 'reconciled') THEN effect.updated_at
        ELSE NULL
    END
FROM external_effects effect
WHERE effect.id = receipt.effect_id
  AND receipt.state IN ('reserved', 'queued')
  AND effect.state IN (
      'queued', 'outcome_unknown', 'retryable_failed',
      'final_failed', 'cancelled', 'reconciled'
  );

CREATE UNIQUE INDEX tag_sync_receipts_single_active
ON tag_sync_receipts ((1))
WHERE state IN ('reserved', 'queued', 'outcome_unknown', 'retryable_failed');

CREATE TABLE tag_provider_group_bindings (
    provider_group_id TEXT PRIMARY KEY CHECK (
        length(provider_group_id) BETWEEN 1 AND 128
        AND provider_group_id = btrim(provider_group_id)
    ),
    group_id BIGINT NOT NULL UNIQUE REFERENCES tag_groups(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);

CREATE TABLE tag_provider_tag_bindings (
    provider_tag_id TEXT PRIMARY KEY CHECK (
        length(provider_tag_id) BETWEEN 1 AND 128
        AND provider_tag_id = btrim(provider_tag_id)
    ),
    tag_id BIGINT NOT NULL UNIQUE REFERENCES tag_catalog_tags(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);

-- The affected production release already holds validated immutable snapshots
-- while its local catalog is empty. Backfill only that unambiguous case so the
-- repaired release renders data immediately without another Provider call.
DO $$
DECLARE
    latest_snapshot JSONB;
    latest_effect_id BIGINT;
    latest_generation BIGINT;
    latest_digest TEXT;
    latest_receipt_id BIGINT;
    latest_actor BIGINT;
    group_row RECORD;
    tag_row RECORD;
    local_group_id BIGINT;
    audit_id BIGINT;
BEGIN
    IF NOT EXISTS (SELECT 1 FROM tag_groups WHERE archived_at IS NULL)
       AND NOT EXISTS (SELECT 1 FROM tag_catalog_tags WHERE archived_at IS NULL) THEN
        SELECT observation.snapshot,
               observation.effect_id,
               observation.generation,
               observation.artifact_digest,
               receipt.id,
               receipt.actor_admin_user_id
        INTO latest_snapshot,
             latest_effect_id,
             latest_generation,
             latest_digest,
             latest_receipt_id,
             latest_actor
        FROM tag_sync_receipts receipt
        JOIN tag_provider_observations observation
          ON observation.effect_id = receipt.effect_id
        WHERE receipt.state = 'executed'
        ORDER BY receipt.id DESC, observation.generation DESC
        LIMIT 1;

        IF FOUND THEN
            FOR group_row IN
                SELECT value, ordinal
                FROM jsonb_array_elements(latest_snapshot->'groups') WITH ORDINALITY AS item(value, ordinal)
                ORDER BY ordinal
            LOOP
                INSERT INTO tag_groups(group_name, sort_order)
                VALUES(group_row.value->>'name', (group_row.ordinal - 1)::INTEGER)
                RETURNING id INTO local_group_id;

                INSERT INTO tag_provider_group_bindings(provider_group_id, group_id)
                VALUES(group_row.value->>'id', local_group_id);

                FOR tag_row IN
                    SELECT value, ordinal
                    FROM jsonb_array_elements(group_row.value->'tags') WITH ORDINALITY AS item(value, ordinal)
                    ORDER BY ordinal
                LOOP
                    WITH inserted AS (
                        INSERT INTO tag_catalog_tags(group_id, tag_name, sort_order)
                        VALUES(local_group_id, tag_row.value->>'name', (tag_row.ordinal - 1)::INTEGER)
                        RETURNING id
                    )
                    INSERT INTO tag_provider_tag_bindings(provider_tag_id, tag_id)
                    SELECT tag_row.value->>'id', id FROM inserted;
                END LOOP;
            END LOOP;

            INSERT INTO tag_audit_events(event_type, actor_admin_user_id, payload, occurred_at)
            VALUES(
                'tag.catalog_sync_backfilled',
                latest_actor,
                jsonb_build_object(
                    'actor', latest_actor,
                    'receipt_id', latest_receipt_id,
                    'effect_id', latest_effect_id,
                    'generation', latest_generation,
                    'artifact_digest', latest_digest,
                    'group_count', jsonb_array_length(latest_snapshot->'groups'),
                    'tag_count', (
                        SELECT COALESCE(sum(jsonb_array_length(value->'tags')), 0)::INTEGER
                        FROM jsonb_array_elements(latest_snapshot->'groups')
                    ),
                    'state', 'executed'
                ),
                clock_timestamp()
            )
            RETURNING id INTO audit_id;

            INSERT INTO tag_outbox(event_type, aggregate_kind, aggregate_id, payload)
            SELECT event_type, 'tag_catalog', id, payload
            FROM tag_audit_events
            WHERE id = audit_id;
        END IF;
    END IF;
END;
$$;
