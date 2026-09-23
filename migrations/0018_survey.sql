-- Owner: internal/survey.
-- Forward-only: published definition versions and referenced questionnaire
-- history are immutable. A rollback must first prove that no Survey business
-- rows exist; production migration tooling must never drop these tables.

CREATE TABLE survey_questionnaires (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name TEXT NOT NULL,
    title TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    mode TEXT NOT NULL CHECK (mode IN ('survey','assessment')),
    answer_display_mode TEXT NOT NULL CHECK (answer_display_mode IN ('all_in_one','one_by_one')),
    slug TEXT NOT NULL UNIQUE,
    status TEXT NOT NULL CHECK (status IN ('draft','published','disabled')),
    active_definition_version_id BIGINT,
    created_by BIGINT NOT NULL REFERENCES admin_users(id) ON DELETE RESTRICT,
    updated_by BIGINT NOT NULL REFERENCES admin_users(id) ON DELETE RESTRICT,
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT survey_questionnaires_name CHECK (btrim(name) = name AND char_length(name) BETWEEN 1 AND 200),
    CONSTRAINT survey_questionnaires_title CHECK (btrim(title) = title AND char_length(title) BETWEEN 1 AND 500),
    CONSTRAINT survey_questionnaires_description CHECK (btrim(description) = description AND char_length(description) <= 10000),
    CONSTRAINT survey_questionnaires_slug CHECK (slug ~ '^[a-z0-9][a-z0-9-]{0,127}$'),
    CONSTRAINT survey_questionnaires_timestamps CHECK (updated_at >= created_at)
);
CREATE INDEX survey_questionnaires_status_idx ON survey_questionnaires(status, updated_at DESC, id DESC);
CREATE INDEX survey_questionnaires_name_idx ON survey_questionnaires(name, id);

CREATE TABLE survey_definition_versions (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    questionnaire_id BIGINT NOT NULL REFERENCES survey_questionnaires(id) ON DELETE RESTRICT,
    version_number BIGINT NOT NULL CHECK (version_number > 0),
    mode TEXT NOT NULL CHECK (mode IN ('survey','assessment')),
    answer_display_mode TEXT NOT NULL CHECK (answer_display_mode IN ('all_in_one','one_by_one')),
    title_snapshot TEXT NOT NULL,
    description_snapshot TEXT NOT NULL DEFAULT '',
    assessment_config JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(assessment_config) = 'object'),
    definition_digest BYTEA NOT NULL CHECK (octet_length(definition_digest) = 32),
    is_immutable BOOLEAN NOT NULL DEFAULT FALSE,
    published_at TIMESTAMPTZ,
    created_by BIGINT NOT NULL REFERENCES admin_users(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT survey_definition_versions_title CHECK (btrim(title_snapshot) = title_snapshot AND char_length(title_snapshot) BETWEEN 1 AND 500),
    CONSTRAINT survey_definition_versions_description CHECK (btrim(description_snapshot) = description_snapshot AND char_length(description_snapshot) <= 10000),
    CONSTRAINT survey_definition_versions_publish CHECK ((is_immutable = FALSE AND published_at IS NULL) OR (is_immutable = TRUE AND published_at IS NOT NULL)),
    UNIQUE(questionnaire_id, version_number),
    UNIQUE(id, questionnaire_id)
);
CREATE INDEX survey_definition_versions_questionnaire_idx ON survey_definition_versions(questionnaire_id, version_number DESC, id DESC);

ALTER TABLE survey_questionnaires
    ADD CONSTRAINT survey_questionnaires_active_version_fk
    FOREIGN KEY (active_definition_version_id, id)
    REFERENCES survey_definition_versions(id, questionnaire_id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE survey_definition_questions (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    definition_version_id BIGINT NOT NULL REFERENCES survey_definition_versions(id) ON DELETE CASCADE,
    question_type TEXT NOT NULL CHECK (question_type IN ('single_choice','multi_choice','textarea','mobile')),
    title TEXT NOT NULL,
    assessment_dimension_key TEXT NOT NULL DEFAULT '',
    sidebar_profile_field TEXT NOT NULL DEFAULT '',
    required BOOLEAN NOT NULL DEFAULT FALSE,
    sort_order INTEGER NOT NULL CHECK (sort_order >= 0 AND sort_order < 200),
    placeholder_text TEXT NOT NULL DEFAULT '',
    validation JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(validation) = 'object'),
    CONSTRAINT survey_definition_questions_title CHECK (btrim(title) = title AND char_length(title) BETWEEN 1 AND 1000),
    CONSTRAINT survey_definition_questions_dimension CHECK (assessment_dimension_key = '' OR assessment_dimension_key ~ '^[A-Za-z0-9._:-]{1,128}$'),
    CONSTRAINT survey_definition_questions_sidebar CHECK (sidebar_profile_field = '' OR sidebar_profile_field ~ '^[A-Za-z0-9._:-]{1,128}$'),
    UNIQUE(definition_version_id, sort_order),
    UNIQUE(id, definition_version_id)
);
CREATE INDEX survey_definition_questions_version_idx ON survey_definition_questions(definition_version_id, sort_order, id);

CREATE TABLE survey_definition_options (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    question_id BIGINT NOT NULL REFERENCES survey_definition_questions(id) ON DELETE CASCADE,
    definition_version_id BIGINT NOT NULL,
    option_text TEXT NOT NULL,
    score DOUBLE PRECISION NOT NULL DEFAULT 0 CHECK (score BETWEEN -1000000 AND 1000000 AND score <> 'NaN'::float8),
    assessment_type_key TEXT NOT NULL DEFAULT '',
    tag_codes JSONB NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(tag_codes) = 'array' AND jsonb_array_length(tag_codes) <= 100),
    is_other BOOLEAN NOT NULL DEFAULT FALSE,
    other_placeholder TEXT NOT NULL DEFAULT '',
    other_max_length INTEGER NOT NULL DEFAULT 0 CHECK (other_max_length BETWEEN 0 AND 200),
    sort_order INTEGER NOT NULL CHECK (sort_order >= 0 AND sort_order < 100),
    CONSTRAINT survey_definition_options_question_fk FOREIGN KEY (question_id, definition_version_id) REFERENCES survey_definition_questions(id, definition_version_id) ON DELETE CASCADE,
    CONSTRAINT survey_definition_options_text CHECK (btrim(option_text) = option_text AND char_length(option_text) BETWEEN 1 AND 1000),
    CONSTRAINT survey_definition_options_type CHECK (assessment_type_key = '' OR assessment_type_key ~ '^[A-Za-z0-9._:-]{1,128}$'),
    CONSTRAINT survey_definition_options_other CHECK ((is_other = FALSE AND other_placeholder = '' AND other_max_length = 0) OR (is_other = TRUE AND btrim(other_placeholder) = other_placeholder AND char_length(other_placeholder) <= 500 AND other_max_length BETWEEN 1 AND 200)),
    UNIQUE(question_id, sort_order),
    UNIQUE(id, definition_version_id)
);
CREATE INDEX survey_definition_options_question_idx ON survey_definition_options(question_id, sort_order, id);

CREATE TABLE survey_score_rules (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    definition_version_id BIGINT NOT NULL REFERENCES survey_definition_versions(id) ON DELETE CASCADE,
    minimum_score DOUBLE PRECISION,
    maximum_score DOUBLE PRECISION,
    tag_codes JSONB NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(tag_codes) = 'array' AND jsonb_array_length(tag_codes) <= 100),
    sort_order INTEGER NOT NULL CHECK (sort_order >= 0 AND sort_order < 100),
    CONSTRAINT survey_score_rules_range CHECK ((minimum_score IS NOT NULL OR maximum_score IS NOT NULL) AND (minimum_score IS NULL OR maximum_score IS NULL OR minimum_score <= maximum_score)),
    UNIQUE(definition_version_id, sort_order)
);
CREATE INDEX survey_score_rules_version_idx ON survey_score_rules(definition_version_id, sort_order, id);

CREATE TABLE survey_operation_receipts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    operation TEXT NOT NULL CHECK (operation IN ('definition_create','definition_update','definition_duplicate','definition_publish','definition_disable','definition_enable','definition_delete')),
    actor_scope TEXT NOT NULL CHECK (actor_scope ~ '^admin:[1-9][0-9]*$'),
    key_digest BYTEA NOT NULL CHECK (octet_length(key_digest) = 32),
    payload_digest BYTEA NOT NULL CHECK (octet_length(payload_digest) = 32),
    state TEXT NOT NULL CHECK (state IN ('in_progress','completed')),
    result_snapshot JSONB,
    created_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    CONSTRAINT survey_operation_receipts_completion CHECK ((state = 'in_progress' AND result_snapshot IS NULL AND completed_at IS NULL) OR (state = 'completed' AND result_snapshot IS NOT NULL AND completed_at IS NOT NULL)),
    UNIQUE(operation, actor_scope, key_digest)
);
CREATE INDEX survey_operation_receipts_created_idx ON survey_operation_receipts(created_at DESC, id DESC);

CREATE TABLE survey_submissions (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    questionnaire_id BIGINT NOT NULL REFERENCES survey_questionnaires(id) ON DELETE RESTRICT,
    definition_version_id BIGINT NOT NULL REFERENCES survey_definition_versions(id) ON DELETE RESTRICT,
    definition_version_number BIGINT NOT NULL CHECK (definition_version_number > 0),
    customer_id BIGINT REFERENCES customers(id) ON DELETE RESTRICT,
    identity_state TEXT NOT NULL CHECK (identity_state IN ('anonymous','resolved','unresolved','conflict')),
    identity_reason TEXT NOT NULL DEFAULT '',
    evidence_digest BYTEA CHECK (evidence_digest IS NULL OR octet_length(evidence_digest) = 32),
    submission_key_digest BYTEA NOT NULL CHECK (octet_length(submission_key_digest) = 32),
    payload_digest BYTEA NOT NULL CHECK (octet_length(payload_digest) = 32),
    questionnaire_slug_snapshot TEXT NOT NULL,
    title_snapshot TEXT NOT NULL,
    mode_snapshot TEXT NOT NULL CHECK (mode_snapshot IN ('survey','assessment')),
    total_score DOUBLE PRECISION NOT NULL DEFAULT 0 CHECK (total_score <> 'NaN'::float8),
    result_snapshot JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(result_snapshot) = 'object'),
    source_channel TEXT NOT NULL DEFAULT '',
    campaign_id TEXT NOT NULL DEFAULT '',
    staff_id TEXT NOT NULL DEFAULT '',
    submitted_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT survey_submissions_identity CHECK ((identity_state = 'resolved' AND customer_id IS NOT NULL) OR (identity_state <> 'resolved' AND customer_id IS NULL)),
    UNIQUE(questionnaire_id, submission_key_digest)
);
CREATE INDEX survey_submissions_questionnaire_idx ON survey_submissions(questionnaire_id, submitted_at DESC, id DESC);
CREATE INDEX survey_submissions_customer_idx ON survey_submissions(customer_id, submitted_at DESC, id DESC) WHERE customer_id IS NOT NULL;
CREATE INDEX survey_submissions_identity_idx ON survey_submissions(identity_state, submitted_at DESC, id DESC);

CREATE TABLE survey_submission_answers (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    submission_id BIGINT NOT NULL REFERENCES survey_submissions(id) ON DELETE RESTRICT,
    definition_question_id BIGINT REFERENCES survey_definition_questions(id) ON DELETE RESTRICT,
    legacy_source_question_id BIGINT,
    question_type TEXT NOT NULL CHECK (question_type IN ('single_choice','multi_choice','textarea','mobile','legacy_unknown')),
    question_title_snapshot TEXT NOT NULL,
    question_sort_order INTEGER NOT NULL DEFAULT 0,
    required_snapshot BOOLEAN NOT NULL DEFAULT FALSE,
    selected_options_snapshot JSONB NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(selected_options_snapshot) = 'array'),
    text_value_ciphertext BYTEA,
    text_value_masked TEXT NOT NULL DEFAULT '',
    answer_digest BYTEA NOT NULL CHECK (octet_length(answer_digest) = 32),
    score_snapshot DOUBLE PRECISION NOT NULL DEFAULT 0 CHECK (score_snapshot <> 'NaN'::float8),
    legacy_definition_missing BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL,
    UNIQUE(submission_id, id),
    CONSTRAINT survey_submission_answers_legacy CHECK (legacy_definition_missing = FALSE OR definition_question_id IS NULL)
);
CREATE INDEX survey_submission_answers_submission_idx ON survey_submission_answers(submission_id, question_sort_order, id);

CREATE TABLE survey_result_tokens (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    submission_id BIGINT NOT NULL UNIQUE REFERENCES survey_submissions(id) ON DELETE RESTRICT,
    token_digest BYTEA NOT NULL UNIQUE CHECK (octet_length(token_digest) = 32),
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    CONSTRAINT survey_result_tokens_expiry CHECK (expires_at IS NULL OR expires_at > created_at)
);

CREATE TABLE survey_oauth_states (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    state_digest BYTEA NOT NULL UNIQUE CHECK (octet_length(state_digest)=32),
    questionnaire_slug TEXT NOT NULL,
    redirect_path TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT survey_oauth_states_redirect CHECK (redirect_path ~ '^/h5/(all|one)\\.html\\?slug=[a-z0-9][a-z0-9-]{0,127}$')
);
CREATE INDEX survey_oauth_states_expiry_idx ON survey_oauth_states(expires_at) WHERE consumed_at IS NULL;

CREATE TABLE survey_identity_sessions (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    session_digest BYTEA NOT NULL UNIQUE CHECK (octet_length(session_digest)=32),
    customer_id BIGINT REFERENCES customers(id) ON DELETE RESTRICT,
    identity_state TEXT NOT NULL CHECK (identity_state IN ('resolved','unresolved','conflict')),
    evidence_digest BYTEA NOT NULL CHECK (octet_length(evidence_digest)=32),
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT survey_identity_sessions_customer CHECK ((identity_state='resolved' AND customer_id IS NOT NULL) OR (identity_state<>'resolved' AND customer_id IS NULL))
);
CREATE INDEX survey_identity_sessions_expiry_idx ON survey_identity_sessions(expires_at) WHERE revoked_at IS NULL;

CREATE TABLE survey_operation_configurations (
    questionnaire_id BIGINT PRIMARY KEY REFERENCES survey_questionnaires(id) ON DELETE RESTRICT,
    completion_navigation_ref TEXT NOT NULL DEFAULT '',
    completion_channel_id BIGINT,
    external_push_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    external_push_configuration_ref TEXT NOT NULL DEFAULT '',
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    updated_by BIGINT NOT NULL REFERENCES admin_users(id) ON DELETE RESTRICT,
    updated_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT survey_operation_completion_ref CHECK (completion_navigation_ref = '' OR completion_navigation_ref ~ '^[A-Za-z0-9._:-]{1,128}$'),
    CONSTRAINT survey_operation_channel CHECK (completion_channel_id IS NULL OR completion_channel_id > 0),
    CONSTRAINT survey_operation_external_ref CHECK (external_push_configuration_ref = '' OR external_push_configuration_ref ~ '^[A-Za-z0-9._:-]{1,128}$'),
    CONSTRAINT survey_operation_external_enabled CHECK (external_push_enabled = FALSE OR external_push_configuration_ref <> '')
);

CREATE TABLE survey_external_operation_receipts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    questionnaire_id BIGINT NOT NULL REFERENCES survey_questionnaires(id) ON DELETE RESTRICT,
    submission_id BIGINT REFERENCES survey_submissions(id) ON DELETE RESTRICT,
    operation_kind TEXT NOT NULL CHECK (operation_kind IN ('completion','external_push','scrm_apply')),
    configuration_ref TEXT NOT NULL DEFAULT '',
    effect_id TEXT,
    status TEXT NOT NULL CHECK (status IN ('disabled','accepted','queued','attempted','executed','failed','outcome_unknown','reconciled','skipped','legacy_success','legacy_failed')),
    failure_category TEXT NOT NULL DEFAULT '',
    occurrence_count BIGINT NOT NULL DEFAULT 1 CHECK (occurrence_count > 0),
    occurred_at TIMESTAMPTZ NOT NULL,
    read_only_legacy BOOLEAN NOT NULL DEFAULT FALSE,
    replayable BOOLEAN NOT NULL DEFAULT TRUE,
    idempotency_key_digest BYTEA UNIQUE CHECK (idempotency_key_digest IS NULL OR octet_length(idempotency_key_digest)=32),
    source_system TEXT,
    source_table TEXT,
    source_pk TEXT,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT survey_external_operation_replay CHECK (read_only_legacy = FALSE OR replayable = FALSE),
    CONSTRAINT survey_external_operation_source CHECK ((source_system IS NULL AND source_table IS NULL AND source_pk IS NULL) OR (source_system IS NOT NULL AND source_table IS NOT NULL AND source_pk IS NOT NULL)),
    UNIQUE(source_system, source_table, source_pk)
);
CREATE INDEX survey_external_operation_questionnaire_idx ON survey_external_operation_receipts(questionnaire_id, occurred_at DESC, id DESC);

CREATE TABLE survey_audit_events (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    event_type TEXT NOT NULL,
    aggregate_type TEXT NOT NULL,
    aggregate_id BIGINT NOT NULL,
    actor_scope TEXT NOT NULL,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(metadata) = 'object'),
    occurred_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX survey_audit_events_aggregate_idx ON survey_audit_events(aggregate_type, aggregate_id, occurred_at DESC, id DESC);

CREATE TABLE survey_outbox (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    event_type TEXT NOT NULL,
    aggregate_type TEXT NOT NULL,
    aggregate_id BIGINT NOT NULL,
    payload JSONB NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
    idempotency_key TEXT NOT NULL UNIQUE,
    occurred_at TIMESTAMPTZ NOT NULL,
    published_at TIMESTAMPTZ
);
CREATE INDEX survey_outbox_pending_idx ON survey_outbox(id) WHERE published_at IS NULL;

CREATE TABLE survey_migration_batches (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    batch_key TEXT NOT NULL UNIQUE,
    source_system TEXT NOT NULL,
    snapshot_at TIMESTAMPTZ NOT NULL,
    manifest JSONB NOT NULL CHECK (jsonb_typeof(manifest) = 'object'),
    manifest_digest BYTEA NOT NULL CHECK (octet_length(manifest_digest) = 32),
    status TEXT NOT NULL CHECK (status IN ('extracted','validated','importing','imported','reconciled','rolled_back','failed')),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE survey_migration_source_map (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    migration_batch_id BIGINT NOT NULL REFERENCES survey_migration_batches(id) ON DELETE RESTRICT,
    source_system TEXT NOT NULL,
    source_table TEXT NOT NULL,
    source_pk TEXT NOT NULL,
    target_table TEXT NOT NULL,
    target_pk BIGINT,
    record_digest BYTEA NOT NULL CHECK (octet_length(record_digest) = 32),
    import_state TEXT NOT NULL CHECK (import_state IN ('imported','quarantined')),
    created_at TIMESTAMPTZ NOT NULL,
    UNIQUE(source_system, source_table, source_pk)
);
CREATE INDEX survey_migration_source_map_batch_idx ON survey_migration_source_map(migration_batch_id, source_table, id);

CREATE TABLE survey_migration_quarantine (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    migration_batch_id BIGINT NOT NULL REFERENCES survey_migration_batches(id) ON DELETE RESTRICT,
    source_system TEXT NOT NULL,
    source_table TEXT NOT NULL,
    source_pk TEXT NOT NULL,
    reason_code TEXT NOT NULL,
    safe_snapshot JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(safe_snapshot) = 'object'),
    record_digest BYTEA NOT NULL CHECK (octet_length(record_digest) = 32),
    created_at TIMESTAMPTZ NOT NULL,
    UNIQUE(source_system, source_table, source_pk, reason_code)
);

CREATE FUNCTION survey_reject_immutable_version_change() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF current_setting('aicrm.survey_migration_rollback', TRUE) = 'authorized' THEN
        IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
        RETURN NEW;
    END IF;
    IF OLD.is_immutable THEN
        RAISE EXCEPTION 'published survey definition version is immutable' USING ERRCODE = 'check_violation';
    END IF;
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER survey_definition_versions_immutable
BEFORE UPDATE OR DELETE ON survey_definition_versions
FOR EACH ROW EXECUTE FUNCTION survey_reject_immutable_version_change();

CREATE FUNCTION survey_reject_published_child_change() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    candidate_version_id BIGINT;
    immutable BOOLEAN;
BEGIN
    IF current_setting('aicrm.survey_migration_rollback', TRUE) = 'authorized' THEN
        IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
        RETURN NEW;
    END IF;
    candidate_version_id := COALESCE(OLD.definition_version_id, NEW.definition_version_id);
    SELECT is_immutable INTO immutable FROM survey_definition_versions WHERE id = candidate_version_id;
    IF immutable THEN
        RAISE EXCEPTION 'published survey definition children are immutable' USING ERRCODE = 'check_violation';
    END IF;
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER survey_definition_questions_immutable
BEFORE UPDATE OR DELETE ON survey_definition_questions
FOR EACH ROW EXECUTE FUNCTION survey_reject_published_child_change();
CREATE TRIGGER survey_definition_options_immutable
BEFORE UPDATE OR DELETE ON survey_definition_options
FOR EACH ROW EXECUTE FUNCTION survey_reject_published_child_change();
CREATE TRIGGER survey_score_rules_immutable
BEFORE UPDATE OR DELETE ON survey_score_rules
FOR EACH ROW EXECUTE FUNCTION survey_reject_published_child_change();
