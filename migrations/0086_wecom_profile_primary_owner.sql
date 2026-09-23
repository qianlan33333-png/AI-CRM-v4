-- Owner: internal/wecom.
-- The old identity bridge selected the lexicographically first non-empty
-- provider userid from a fully replaced follow-user set.  Keep that
-- provider-derived fact with the profile and its relationship observations;
-- an empty trusted set deliberately preserves a previously known primary.
ALTER TABLE wecom_external_contact_profiles
  ADD COLUMN primary_owner_userid TEXT NOT NULL DEFAULT '' CHECK (
    primary_owner_userid = btrim(primary_owner_userid)
    AND char_length(primary_owner_userid) <= 1024
    AND primary_owner_userid !~ '[[:cntrl:]]'
  );

ALTER TABLE wecom_external_contact_profiles
  ADD COLUMN primary_owner_run_id BIGINT REFERENCES wecom_customer_sync_runs(id) ON DELETE RESTRICT;

ALTER TABLE wecom_customer_owner_observations
  ADD COLUMN primary_owner_userid TEXT NOT NULL DEFAULT '' CHECK (
    primary_owner_userid = btrim(primary_owner_userid)
    AND char_length(primary_owner_userid) <= 1024
    AND primary_owner_userid !~ '[[:cntrl:]]'
  );

CREATE INDEX wecom_customer_owner_observations_primary_owner_idx
  ON wecom_customer_owner_observations(customer_id, relationship_status, primary_owner_userid)
  WHERE primary_owner_userid <> '';
CREATE INDEX wecom_external_contact_profiles_primary_owner_run_idx
  ON wecom_external_contact_profiles(primary_owner_run_id, customer_id)
  WHERE primary_owner_userid <> '';
