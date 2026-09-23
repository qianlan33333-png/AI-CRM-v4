-- Owner: internal/survey for completion copy/target; internal/outbound for endpoint.
-- Future saves use the legacy questionnaire operations contract. Existing rows
-- remain unchanged and keep their opaque references.
ALTER TABLE survey_operation_configurations
    ADD COLUMN completion_target JSONB NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN lead_qr_title TEXT NOT NULL DEFAULT '',
    ADD COLUMN lead_qr_subtitle TEXT NOT NULL DEFAULT '';
ALTER TABLE survey_operation_configurations
    ADD CONSTRAINT survey_operation_completion_target_object CHECK (jsonb_typeof(completion_target) = 'object'),
    ADD CONSTRAINT survey_operation_lead_qr_title_length CHECK (length(lead_qr_title) <= 40),
    ADD CONSTRAINT survey_operation_lead_qr_subtitle_length CHECK (length(lead_qr_subtitle) <= 100);
CREATE TABLE outbound_survey_completion_endpoints (
    questionnaire_id BIGINT PRIMARY KEY CHECK (questionnaire_id > 0),
    configuration_reference TEXT NOT NULL UNIQUE,
    template_reference TEXT NOT NULL CHECK (length(template_reference) BETWEEN 1 AND 128),
    endpoint TEXT NOT NULL CHECK (length(endpoint) BETWEEN 1 AND 4096),
    configuration_metadata JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(configuration_metadata) = 'object'),
    revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
