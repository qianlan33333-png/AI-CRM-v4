-- Owner: Survey. The immutable Identity-owned handle authorizes only exact
-- successful-command receipt recovery; customer_id remains historical fact.
-- Forward-only: no guessed backfill and no changes to answers/claims/tokens.
ALTER TABLE survey_submissions
    ADD COLUMN provider_identity_id BIGINT REFERENCES customer_identities(id) ON DELETE RESTRICT,
    ADD CONSTRAINT survey_submissions_provider_identity_positive CHECK (provider_identity_id IS NULL OR provider_identity_id > 0);
