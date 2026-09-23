-- Owner: internal/survey.
-- Forward-only: restores the legacy assessment's local dimension/type business
-- keys. These are presentation and scoring associations, not opaque security
-- identifiers. Existing rows must satisfy the replacement constraints.

ALTER TABLE survey_definition_questions
    DROP CONSTRAINT survey_definition_questions_dimension;

ALTER TABLE survey_definition_questions
    ADD CONSTRAINT survey_definition_questions_dimension
    CHECK (
        assessment_dimension_key = '' OR (
            assessment_dimension_key !~ '(^[[:space:]]|[[:space:]]$)'
            AND char_length(assessment_dimension_key) BETWEEN 1 AND 128
            AND assessment_dimension_key !~ '[[:cntrl:]]'
        )
    );

ALTER TABLE survey_definition_options
    DROP CONSTRAINT survey_definition_options_type;

ALTER TABLE survey_definition_options
    ADD CONSTRAINT survey_definition_options_type
    CHECK (
        assessment_type_key = '' OR (
            assessment_type_key !~ '(^[[:space:]]|[[:space:]]$)'
            AND char_length(assessment_type_key) BETWEEN 1 AND 128
            AND assessment_type_key !~ '[[:cntrl:]]'
        )
    );
