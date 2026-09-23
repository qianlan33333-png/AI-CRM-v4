ALTER TABLE product_external_push_configurations
    ADD COLUMN field_mapping jsonb,
    ADD CONSTRAINT product_external_push_field_mapping_shape CHECK (
        field_mapping IS NULL OR ((jsonb_typeof(field_mapping) = 'object' AND field_mapping->>'version' = '1' AND jsonb_typeof(field_mapping->'fields') = 'array') IS TRUE)
    );
