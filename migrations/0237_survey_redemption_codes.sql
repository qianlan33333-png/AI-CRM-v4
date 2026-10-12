-- Owner: Survey. Qualification belongs to a questionnaire, not a definition.
ALTER TABLE survey_definition_questions DROP CONSTRAINT survey_definition_questions_question_type_check;
ALTER TABLE survey_definition_questions ADD CONSTRAINT survey_definition_questions_question_type_check
 CHECK (question_type IN ('single_choice','multi_choice','textarea','mobile','redemption_code'));
ALTER TABLE survey_submission_answers DROP CONSTRAINT survey_submission_answers_question_type_check;
ALTER TABLE survey_submission_answers ADD CONSTRAINT survey_submission_answers_question_type_check
 CHECK (question_type IN ('single_choice','multi_choice','textarea','mobile','legacy_unknown','redemption_code'));
ALTER TABLE survey_operation_receipts DROP CONSTRAINT survey_operation_receipts_operation_check;
ALTER TABLE survey_operation_receipts ADD CONSTRAINT survey_operation_receipts_operation_check
 CHECK (operation IN ('definition_create','definition_update','definition_duplicate','definition_publish','definition_disable','definition_enable','definition_delete','definition_archive','redemption_import'));
CREATE TABLE survey_redemption_codes (
 id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 questionnaire_id BIGINT NOT NULL REFERENCES survey_questionnaires(id),
 code_digest BYTEA NOT NULL CHECK (octet_length(code_digest)=32),
 code_ciphertext BYTEA NOT NULL,
 used_submission_id BIGINT UNIQUE REFERENCES survey_submissions(id),
 created_by BIGINT NOT NULL REFERENCES admin_users(id),
 created_at TIMESTAMPTZ NOT NULL,
 UNIQUE(questionnaire_id,code_digest)
);
CREATE INDEX survey_redemption_codes_questionnaire_idx ON survey_redemption_codes(questionnaire_id,id);
