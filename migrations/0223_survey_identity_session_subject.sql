-- Owner: Survey. Preserve historical sessions and submissions. Old sessions
-- lack a trustworthy provider handle and require reauthorization in runtime.
-- The additive nullable column permits rolling source/schema compatibility;
-- no historical customer is guessed or rewritten.
ALTER TABLE survey_identity_sessions
    ADD COLUMN provider_identity_id BIGINT REFERENCES customer_identities(id) ON DELETE RESTRICT,
    ADD CONSTRAINT survey_identity_sessions_provider_identity_positive CHECK (provider_identity_id IS NULL OR provider_identity_id > 0);
