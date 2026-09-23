-- Owner: media. Grouping is operator metadata, independent of provider content.
ALTER TABLE media_attachments ADD COLUMN category TEXT NOT NULL DEFAULT '' CHECK (length(category)<=100);
ALTER TABLE media_miniprograms ADD COLUMN category TEXT NOT NULL DEFAULT '' CHECK (length(category)<=100);
CREATE INDEX media_attachments_category_idx ON media_attachments(category,id);
CREATE INDEX media_miniprograms_category_idx ON media_miniprograms(category,id);
