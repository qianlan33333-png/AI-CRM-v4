-- Customer owns the latest reusable business contact. Original source facts
-- remain in Order/Survey. No phone uniqueness: contact is not identity.
CREATE TABLE customer_contact_profiles (
 customer_id BIGINT PRIMARY KEY REFERENCES customers(id),
 phone_ciphertext BYTEA,
 phone_source TEXT,
 phone_updated_at TIMESTAMPTZ,
 shipping_ciphertext BYTEA,
 shipping_source TEXT,
 shipping_updated_at TIMESTAMPTZ,
 CHECK ((phone_ciphertext IS NULL) = (phone_updated_at IS NULL)),
 CHECK ((shipping_ciphertext IS NULL) = (shipping_updated_at IS NULL))
);
ALTER TABLE survey_definition_questions DROP CONSTRAINT survey_definition_questions_question_type_check;
ALTER TABLE survey_definition_questions ADD CONSTRAINT survey_definition_questions_question_type_check
 CHECK(question_type IN ('single_choice','multi_choice','textarea','mobile','redemption_code','attachment','shipping_address'));
ALTER TABLE survey_submission_answers DROP CONSTRAINT survey_submission_answers_question_type_check;
ALTER TABLE survey_submission_answers ADD CONSTRAINT survey_submission_answers_question_type_check
 CHECK(question_type IN ('single_choice','multi_choice','textarea','mobile','legacy_unknown','redemption_code','attachment','shipping_address'));
