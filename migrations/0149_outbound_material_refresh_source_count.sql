-- Owner: outbound. A prepared material effect first creates a durable refresh
-- membership row, then the worker atomically claims a source page and records
-- its exact source-reference count. Zero therefore means "bound, not yet
-- counted" and is never a completed page count.
ALTER TABLE outbound_material_refresh_items
    DROP CONSTRAINT outbound_material_refresh_items_source_count_check;
ALTER TABLE outbound_material_refresh_items
    ADD CONSTRAINT outbound_material_refresh_items_source_count_check CHECK(source_count >= 0);
ALTER TABLE outbound_material_refresh_items
    ALTER COLUMN source_count SET DEFAULT 0;
