-- Owner: internal/survey.
-- Archive is a retained terminal state: submissions, definition snapshots,
-- receipts and audit facts remain intact while normal lists and public reads
-- no longer expose the questionnaire.

ALTER TABLE survey_questionnaires
    DROP CONSTRAINT IF EXISTS survey_questionnaires_status_check;

ALTER TABLE survey_questionnaires
    ADD CONSTRAINT survey_questionnaires_status_check
    CHECK (status IN ('draft','published','disabled','archived'));
