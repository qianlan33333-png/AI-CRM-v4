-- Owner: media
-- Retention: immutable, verified correspondences from a frozen legacy material
-- snapshot to an enabled V3 Media record. AI Assistant and other consumers
-- read these mappings but never write them from an intake request.

CREATE TABLE media_legacy_material_mappings (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    source_system TEXT NOT NULL CHECK (source_system <> '' AND length(source_system) <= 80 AND source_system !~ '[[:space:][:cntrl:]]'),
    legacy_material_kind TEXT NOT NULL CHECK (legacy_material_kind IN ('image','attachment','miniprogram','group_invite')),
    legacy_material_id TEXT NOT NULL CHECK (legacy_material_id <> '' AND length(legacy_material_id) <= 128 AND legacy_material_id !~ '[[:space:][:cntrl:]]'),
    material_kind TEXT NOT NULL CHECK (material_kind IN ('image','attachment','miniprogram','group_invite')),
    material_id BIGINT NOT NULL CHECK (material_id > 0),
    source_digest TEXT NOT NULL CHECK (source_digest ~ '^sha256:[0-9a-f]{64}$'),
    source_record_digest TEXT NOT NULL CHECK (source_record_digest ~ '^sha256:[0-9a-f]{64}$'),
    imported_by TEXT NOT NULL CHECK (imported_by <> '' AND length(imported_by) <= 120 AND imported_by !~ '[[:cntrl:]]'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (source_system, legacy_material_kind, legacy_material_id)
);

CREATE INDEX media_legacy_material_mappings_target_idx
    ON media_legacy_material_mappings(material_kind, material_id);

CREATE FUNCTION media_legacy_material_mappings_reject_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'media legacy material mappings are immutable';
END;
$$;

CREATE TRIGGER media_legacy_material_mappings_immutable
    BEFORE UPDATE OR DELETE OR TRUNCATE ON media_legacy_material_mappings
    FOR EACH STATEMENT EXECUTE FUNCTION media_legacy_material_mappings_reject_mutation();
