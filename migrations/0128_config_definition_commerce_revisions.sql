-- Owner: internal/configmigration/target. Append-only proof of permitted
-- current-source coupon deltas; historical source mappings remain unchanged.
CREATE TABLE config_definition_commerce_revisions (
 id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 source_map_id BIGINT NOT NULL REFERENCES config_definition_import_source_maps(id),
 batch_id BIGINT NOT NULL REFERENCES config_definition_import_batches(id),
 prior_source_digest BYTEA NOT NULL CHECK(octet_length(prior_source_digest)=32),
 source_digest BYTEA NOT NULL CHECK(octet_length(source_digest)=32),
 source_updated_at TIMESTAMPTZ NOT NULL,
 total_issue_limit BIGINT NOT NULL CHECK(total_issue_limit>0),
 issued_count BIGINT NOT NULL CHECK(issued_count>=0 AND issued_count<=total_issue_limit),
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(source_map_id,batch_id)
);
CREATE TRIGGER config_definition_commerce_revisions_append_only
 BEFORE UPDATE OR DELETE OR TRUNCATE ON config_definition_commerce_revisions
 FOR EACH STATEMENT EXECUTE FUNCTION config_definition_import_source_maps_reject_mutation();
