-- Owner: internal/survey.
-- Freeze the non-secret outbound compatibility policy at submission acceptance.
-- URLs and signing keys remain composition-only; answers, phone and external
-- identity values are never stored in this JSON snapshot or External Effects.
ALTER TABLE survey_operation_configurations ADD COLUMN external_push_metadata JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(external_push_metadata)='object');
CREATE TABLE survey_completion_push_snapshots (
    submission_id BIGINT PRIMARY KEY REFERENCES survey_submissions(id) ON DELETE RESTRICT,
    questionnaire_id BIGINT NOT NULL REFERENCES survey_questionnaires(id) ON DELETE RESTRICT,
    configuration_ref TEXT NOT NULL,
    configuration_version TEXT NOT NULL DEFAULT '',
    configuration_digest BYTEA NOT NULL CHECK (octet_length(configuration_digest)=32),
    identity_kind TEXT NOT NULL DEFAULT '',
    identity_scope TEXT NOT NULL DEFAULT '',
    identity_evidence_digest BYTEA NOT NULL CHECK (octet_length(identity_evidence_digest)=32),
    external_identity_ciphertext BYTEA,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(metadata)='object'),
    definition_version_id BIGINT NOT NULL REFERENCES survey_definition_versions(id) ON DELETE RESTRICT,
    result_snapshot JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(result_snapshot)='object'),
    created_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT survey_completion_push_snapshot_ref CHECK (configuration_ref ~ '^[A-Za-z0-9._:-]{1,128}$'),
    CONSTRAINT survey_completion_push_snapshot_identity CHECK ((identity_kind='' AND identity_scope='') OR (identity_kind<>'' AND identity_scope<>''))
);
