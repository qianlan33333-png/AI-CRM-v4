-- Owner: internal/survey.
-- Synthetic admin test pushes are not submissions and never identify/create a
-- Customer. Freeze the safe body inputs and target policy before EER accepts.
CREATE TABLE survey_completion_test_push_snapshots (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    questionnaire_id BIGINT NOT NULL REFERENCES survey_questionnaires(id) ON DELETE RESTRICT,
    test_run_id TEXT NOT NULL UNIQUE,
    questionnaire_title TEXT NOT NULL,
    submitted_at TIMESTAMPTZ NOT NULL,
    configuration_ref TEXT NOT NULL,
    configuration_version TEXT NOT NULL DEFAULT '',
    configuration_digest BYTEA NOT NULL CHECK (octet_length(configuration_digest)=32),
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(metadata)='object'),
    source_digest BYTEA NOT NULL UNIQUE CHECK (octet_length(source_digest)=32),
    target_digest BYTEA NOT NULL CHECK (octet_length(target_digest)=32),
    payload_digest BYTEA NOT NULL CHECK (octet_length(payload_digest)=32),
    policy_digest BYTEA NOT NULL CHECK (octet_length(policy_digest)=32),
    idempotency_key_digest BYTEA NOT NULL UNIQUE CHECK (octet_length(idempotency_key_digest)=32),
    created_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT survey_completion_test_push_ref CHECK (configuration_ref ~ '^[A-Za-z0-9._:-]{1,128}$'),
    CONSTRAINT survey_completion_test_push_run CHECK (test_run_id ~ '^questionnaire-test-[a-f0-9]{32}$')
);
CREATE INDEX survey_completion_test_push_questionnaire_idx ON survey_completion_test_push_snapshots(questionnaire_id, created_at DESC, id DESC);
