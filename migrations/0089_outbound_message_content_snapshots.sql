-- Owner: Outbound.  An accepted automatic-message intent keeps only its
-- immutable content and Media-source references.  Provider identifiers,
-- binary bodies, and mutable presentation metadata remain outside this table.
ALTER TABLE outbound_message_intents
  ADD COLUMN content_snapshot JSONB,
  ADD COLUMN content_snapshot_digest BYTEA;

ALTER TABLE outbound_message_intents
  ADD CONSTRAINT outbound_message_intents_content_snapshot_shape CHECK (
    (content_snapshot IS NULL AND content_snapshot_digest IS NULL)
    OR (content_snapshot IS NOT NULL AND jsonb_typeof(content_snapshot)='object'
        AND content_snapshot_digest IS NOT NULL
        AND octet_length(content_snapshot_digest)=32)
  );
