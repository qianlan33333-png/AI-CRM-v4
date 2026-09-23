-- Independent resolution derivation; original source snapshot time is immutable.
ALTER TABLE segment_audience_history_batches
 ADD COLUMN resolution_source_digest bytea CHECK(resolution_source_digest IS NULL OR octet_length(resolution_source_digest)=32),
 ADD COLUMN resolution_parent_digest bytea CHECK(resolution_parent_digest IS NULL OR octet_length(resolution_parent_digest)=32),
 ADD COLUMN resolution_derived_at timestamptz,
 ADD CONSTRAINT segment_history_resolution_proof_shape CHECK(
  (resolution_source_digest IS NULL AND resolution_derived_at IS NULL AND resolution_parent_digest IS NULL)
  OR (resolution_source_digest IS NOT NULL AND resolution_derived_at IS NOT NULL AND resolution_derived_at>=captured_at)
 );
