-- Owner: media. Stable groups and a compatibility projection for legacy category writers.
CREATE TABLE media_material_groups (
 id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 kind TEXT NOT NULL CHECK (kind IN ('image','attachment','miniprogram')),
 name TEXT NOT NULL CHECK (name <> ''),
 version BIGINT NOT NULL DEFAULT 1,
 created_by BIGINT NOT NULL DEFAULT 0,
 updated_by BIGINT NOT NULL DEFAULT 0,
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(kind,name)
);
ALTER TABLE media_images ADD COLUMN group_id BIGINT REFERENCES media_material_groups(id);
ALTER TABLE media_attachments ADD COLUMN group_id BIGINT REFERENCES media_material_groups(id);
ALTER TABLE media_miniprograms ADD COLUMN group_id BIGINT REFERENCES media_material_groups(id);
ALTER TABLE media_attachment_uploads ADD COLUMN group_id BIGINT REFERENCES media_material_groups(id) ON DELETE SET NULL;
INSERT INTO media_material_groups(kind,name)
 SELECT 'image',category FROM media_images WHERE category<>'' GROUP BY category
 UNION ALL SELECT 'attachment',category FROM media_attachments WHERE category<>'' GROUP BY category
 UNION ALL SELECT 'miniprogram',category FROM media_miniprograms WHERE category<>'' GROUP BY category;
UPDATE media_images m SET group_id=g.id FROM media_material_groups g WHERE g.kind='image' AND g.name=m.category;
UPDATE media_attachments m SET group_id=g.id FROM media_material_groups g WHERE g.kind='attachment' AND g.name=m.category;
UPDATE media_miniprograms m SET group_id=g.id FROM media_material_groups g WHERE g.kind='miniprogram' AND g.name=m.category;
CREATE INDEX media_images_group_id_idx ON media_images(group_id,id);
CREATE INDEX media_attachments_group_id_idx ON media_attachments(group_id,id);
CREATE INDEX media_miniprograms_group_id_idx ON media_miniprograms(group_id,id);

-- Legacy category adapters remain supported in the same owner/transaction.
-- New ID writes derive category; old name writes resolve/provision exactly one group.
CREATE FUNCTION media_sync_material_group() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE group_name TEXT;
BEGIN
 IF TG_OP='INSERT' THEN
   IF NEW.group_id IS NULL AND NEW.category<>'' THEN
     INSERT INTO media_material_groups(kind,name,created_by,updated_by)
       VALUES(TG_ARGV[0],NEW.category,NEW.created_by,NEW.updated_by)
       ON CONFLICT(kind,name) DO UPDATE SET name=EXCLUDED.name RETURNING id INTO NEW.group_id;
   END IF;
 ELSIF NEW.group_id IS NOT DISTINCT FROM OLD.group_id AND NEW.category IS DISTINCT FROM OLD.category THEN
   IF NEW.category='' THEN NEW.group_id=NULL;
   ELSE
     INSERT INTO media_material_groups(kind,name,created_by,updated_by)
       VALUES(TG_ARGV[0],NEW.category,NEW.updated_by,NEW.updated_by)
       ON CONFLICT(kind,name) DO UPDATE SET name=EXCLUDED.name RETURNING id INTO NEW.group_id;
   END IF;
 END IF;
 IF NEW.group_id IS NULL THEN NEW.category='';
 ELSE
   SELECT name INTO group_name FROM media_material_groups WHERE id=NEW.group_id AND kind=TG_ARGV[0] FOR SHARE;
   IF NOT FOUND THEN RAISE EXCEPTION 'invalid material group' USING ERRCODE='23514'; END IF;
   NEW.category=group_name;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER media_images_group_sync BEFORE INSERT OR UPDATE OF category,group_id ON media_images FOR EACH ROW EXECUTE FUNCTION media_sync_material_group('image');
CREATE TRIGGER media_attachments_group_sync BEFORE INSERT OR UPDATE OF category,group_id ON media_attachments FOR EACH ROW EXECUTE FUNCTION media_sync_material_group('attachment');
CREATE TRIGGER media_miniprograms_group_sync BEFORE INSERT OR UPDATE OF category,group_id ON media_miniprograms FOR EACH ROW EXECUTE FUNCTION media_sync_material_group('miniprogram');
