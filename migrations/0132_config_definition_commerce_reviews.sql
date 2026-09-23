-- Owner: internal/configmigration/target. Explicit reviewed exceptions never
-- pretend to reconstruct an unavailable historical source snapshot.
CREATE TABLE config_definition_commerce_reviews (
 id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 source_map_id BIGINT NOT NULL REFERENCES config_definition_import_source_maps(id),
 batch_id BIGINT NOT NULL REFERENCES config_definition_import_batches(id),
 basis TEXT NOT NULL CHECK(basis='source_authoritative_review'),
 manifest_digest BYTEA NOT NULL CHECK(octet_length(manifest_digest)=32),
 full_source_digest BYTEA NOT NULL CHECK(octet_length(full_source_digest)=32),
 before_digest BYTEA NOT NULL CHECK(octet_length(before_digest)=32),
 after_digest BYTEA NOT NULL CHECK(octet_length(after_digest)=32),
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(source_map_id,batch_id)
);
CREATE TRIGGER config_definition_commerce_reviews_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON config_definition_commerce_reviews FOR EACH STATEMENT EXECUTE FUNCTION config_definition_import_source_maps_reject_mutation();

-- Coupon Owner: preserve normal link immutability. A reviewed cutover may
-- replace a test-only link exactly once, with an Owner audit accepted in the
-- same transaction and no target claims. No API path emits this audit type.
CREATE OR REPLACE FUNCTION coupon_rules_public_slug_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.public_slug IS NOT NULL AND NEW.public_slug IS DISTINCT FROM OLD.public_slug THEN
  IF NEW.version<>OLD.version+1 OR NEW.public_slug IS NULL OR EXISTS(SELECT 1 FROM coupon_customer_claims WHERE coupon_id=OLD.id) OR NOT EXISTS(
   SELECT 1 FROM coupon_audit_events a WHERE a.coupon_id=OLD.id AND a.event_type='coupon.cutover_reviewed'
   AND a.payload->>'transaction_id'=txid_current()::text
   AND a.payload->>'old_slug'=OLD.public_slug AND a.payload->>'new_slug'=NEW.public_slug
   AND a.payload->>'old_version'=OLD.version::text
   AND a.payload->>'before_sha256' ~ '^[0-9a-f]{64}$'
  ) THEN RAISE EXCEPTION 'coupon public slug is immutable'; END IF;
 END IF;
 RETURN NEW;
END;
$$;
