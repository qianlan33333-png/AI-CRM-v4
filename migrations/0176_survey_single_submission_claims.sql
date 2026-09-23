-- Owner: internal/survey.
-- A resolved canonical customer may consume one post-cutover submission per
-- questionnaire. This migration deliberately does not backfill, merge, or
-- delete historical submissions: only claims accepted after it is installed
-- participate in the one-submission rule.
--
-- submission_id is nullable only while the enclosing Survey Unit of Work is
-- creating the submission. The owner fills it before commit; any failure rolls
-- back the claim with the submission, audit, Outbox, and effect acceptance.
CREATE TABLE survey_submission_claims (
    questionnaire_id BIGINT NOT NULL REFERENCES survey_questionnaires(id) ON DELETE RESTRICT,
    customer_id BIGINT NOT NULL REFERENCES customers(id) ON DELETE RESTRICT,
    submission_id BIGINT UNIQUE REFERENCES survey_submissions(id) ON DELETE RESTRICT,
    claimed_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (questionnaire_id, customer_id)
);
CREATE INDEX survey_submission_claims_submission_idx
    ON survey_submission_claims(submission_id) WHERE submission_id IS NOT NULL;
