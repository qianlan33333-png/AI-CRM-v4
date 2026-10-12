-- Media owns private images and blob bytes. Survey owns answer associations.
CREATE TABLE media_private_images (
 ref TEXT PRIMARY KEY CHECK(ref ~ '^[A-Za-z0-9_-]{43}$'),
 blob_digest TEXT NOT NULL REFERENCES media_blobs(digest),
 file_name TEXT NOT NULL,
 mime_type TEXT NOT NULL CHECK(mime_type IN ('image/png','image/jpeg','image/gif')),
 byte_size BIGINT NOT NULL CHECK(byte_size>0),
 width INTEGER NOT NULL CHECK(width>0),
 height INTEGER NOT NULL CHECK(height>0),
 created_at TIMESTAMPTZ NOT NULL DEFAULT transaction_timestamp()
);
ALTER TABLE survey_definition_questions DROP CONSTRAINT survey_definition_questions_question_type_check;
ALTER TABLE survey_definition_questions ADD CONSTRAINT survey_definition_questions_question_type_check
 CHECK(question_type IN ('single_choice','multi_choice','textarea','mobile','redemption_code','attachment'));
ALTER TABLE survey_submission_answers DROP CONSTRAINT survey_submission_answers_question_type_check;
ALTER TABLE survey_submission_answers ADD CONSTRAINT survey_submission_answers_question_type_check
 CHECK(question_type IN ('single_choice','multi_choice','textarea','mobile','legacy_unknown','redemption_code','attachment'));
CREATE TABLE survey_submission_attachments (
 answer_id BIGINT PRIMARY KEY REFERENCES survey_submission_answers(id),
 private_ref TEXT NOT NULL,
 file_name TEXT NOT NULL,
 mime_type TEXT NOT NULL CHECK(mime_type IN ('image/png','image/jpeg','image/gif')),
 byte_size BIGINT NOT NULL CHECK(byte_size>0 AND byte_size<2097152)
);
