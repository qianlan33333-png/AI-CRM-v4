-- Owner: internal/survey. This compatibility fact preserves the frozen donor
-- questionnaire_submissions.unionid for the authorized external-read surface.
-- It is not an Identity fact and is never used to provision, resolve, link, or
-- merge a customer. The migration tool verifies its source digest on replay.
CREATE TABLE survey_legacy_external_projections (
    submission_id BIGINT PRIMARY KEY REFERENCES survey_submissions(id) ON DELETE CASCADE,
    historical_unionid TEXT NOT NULL CHECK (length(historical_unionid) BETWEEN 1 AND 1024 AND historical_unionid = btrim(historical_unionid)),
    source_projection_digest BYTEA NOT NULL CHECK (octet_length(source_projection_digest) = 32),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX survey_legacy_external_projections_unionid_idx
    ON survey_legacy_external_projections(historical_unionid, submission_id);
