-- Owner: media. Provider preparation consumes this immutable source evidence
-- only through internal/media/port; External Effects never receives blob data.
CREATE TABLE media_material_source_snapshots (
    source_ref TEXT NOT NULL CHECK (source_ref ~ '^(image|attachment):[1-9][0-9]*$'),
    snapshot_version BIGINT NOT NULL CHECK (snapshot_version > 0),
    source_type TEXT NOT NULL CHECK (source_type IN ('image','file')),
    content_digest TEXT NOT NULL REFERENCES media_blobs(digest),
    blob_digest TEXT NOT NULL REFERENCES media_blobs(digest),
    file_name TEXT NOT NULL CHECK (length(file_name) BETWEEN 1 AND 255),
    media_type TEXT NOT NULL CHECK (length(media_type) BETWEEN 1 AND 255),
    byte_size BIGINT NOT NULL CHECK (byte_size > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (source_ref, snapshot_version, content_digest),
    CONSTRAINT media_material_source_snapshot_digest_matches_blob CHECK (content_digest = blob_digest)
);
CREATE INDEX media_material_source_snapshots_source_created
    ON media_material_source_snapshots (source_ref, created_at DESC);

-- Owner: aiassistant. Existing rows retain their legacy Python cover
-- readbacks with image_id=0. This migration does not rewrite a frozen card,
-- digest, content version, review state, approval, or delivery receipt.
ALTER TABLE ai_assistant_excel_batch_versions
    ADD COLUMN cover_image_id BIGINT NOT NULL DEFAULT 0 CHECK (cover_image_id >= 0);
ALTER TABLE ai_assistant_excel_batch_version_covers
    ADD COLUMN cover_image_id BIGINT NOT NULL DEFAULT 0 CHECK (cover_image_id >= 0);
