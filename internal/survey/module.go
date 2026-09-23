package survey

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5/pgxpool"
	surveyhttp "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/http"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
	"net/http"
)

type ModuleRegistration struct {
	completionProviderEnabled bool
	completionTargets         surveyport.CompletionTargetCatalog
}
type HTTPBindings struct{ Survey http.Handler }

func NewModuleRegistration() *ModuleRegistration { return &ModuleRegistration{} }
func (m *ModuleRegistration) SetCompletionProviderEnabled(enabled bool) *ModuleRegistration {
	if m != nil {
		m.completionProviderEnabled = enabled
	}
	return m
}
func (m *ModuleRegistration) SetCompletionTargetCatalog(catalog surveyport.CompletionTargetCatalog) *ModuleRegistration {
	if m != nil {
		m.completionTargets = catalog
	}
	return m
}
func (m *ModuleRegistration) Bind(definitions surveyport.DefinitionApplication, submissions interface {
	surveyport.PublicApplication
	surveyport.SubmissionApplication
}, security surveyhttp.RequestSecurity, oauth ...surveyhttp.OAuthApplication) (HTTPBindings, error) {
	if m == nil {
		return HTTPBindings{}, errors.New("survey module required")
	}
	handler, err := surveyhttp.NewHandler(definitions, submissions, security, oauth...)
	if err != nil {
		return HTTPBindings{}, err
	}
	handler.SetCompletionProviderEnabled(m.completionProviderEnabled)
	handler.SetCompletionTargetCatalog(m.completionTargets)
	return HTTPBindings{Survey: handler}, nil
}
func (m *ModuleRegistration) Readiness(ctx context.Context, pool *pgxpool.Pool) error {
	if m == nil || pool == nil {
		return errors.New("survey module dependencies required")
	}
	var ready bool
	err := pool.QueryRow(ctx, `SELECT NOT EXISTS(SELECT 1 FROM unnest(ARRAY['survey_questionnaires','survey_definition_versions','survey_definition_questions','survey_definition_options','survey_score_rules','survey_submissions','survey_submission_claims','survey_submission_answers','survey_result_tokens','survey_oauth_states','survey_identity_sessions','survey_phone_binding_receipts','survey_operation_configurations','survey_external_operation_receipts','survey_completion_test_push_snapshots','survey_audit_events','survey_outbox','survey_migration_batches','survey_migration_source_map','survey_migration_quarantine','survey_legacy_external_projections']) required(name) WHERE to_regclass(current_schema()||'.'||required.name) IS NULL)`).Scan(&ready)
	if err != nil {
		return err
	}
	if !ready {
		return errors.New("survey schema is not ready")
	}
	var redirectConstraintReady bool
	err = pool.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1
		FROM pg_constraint c
		JOIN pg_class rel ON rel.oid=c.conrelid
		JOIN pg_namespace ns ON ns.oid=rel.relnamespace
		WHERE ns.nspname=current_schema()
		  AND rel.relname='survey_oauth_states'
		  AND c.conname='survey_oauth_states_redirect'
		  AND pg_get_constraintdef(c.oid) LIKE '%/h5/(all|one)\.html\?slug=%' ESCAPE ''
	)`).Scan(&redirectConstraintReady)
	if err != nil {
		return err
	}
	if !redirectConstraintReady {
		return errors.New("survey OAuth state redirect constraint is not ready")
	}
	var assessmentBusinessKeyConstraintsReady bool
	err = pool.QueryRow(ctx, `SELECT count(*) = 2
		FROM pg_constraint c
		JOIN pg_class rel ON rel.oid=c.conrelid
		JOIN pg_namespace ns ON ns.oid=rel.relnamespace
		WHERE ns.nspname=current_schema()
		  AND (
			(c.conname='survey_definition_questions_dimension'
			 AND rel.relname='survey_definition_questions'
			 AND strpos(pg_get_constraintdef(c.oid), 'char_length(assessment_dimension_key)') > 0
			 AND strpos(pg_get_constraintdef(c.oid), '[[:space:]]') > 0
			 AND strpos(pg_get_constraintdef(c.oid), '[[:cntrl:]]') > 0)
			OR
			(c.conname='survey_definition_options_type'
			 AND rel.relname='survey_definition_options'
			 AND strpos(pg_get_constraintdef(c.oid), 'char_length(assessment_type_key)') > 0
			 AND strpos(pg_get_constraintdef(c.oid), '[[:space:]]') > 0
			 AND strpos(pg_get_constraintdef(c.oid), '[[:cntrl:]]') > 0)
		  )`).Scan(&assessmentBusinessKeyConstraintsReady)
	if err != nil {
		return err
	}
	if !assessmentBusinessKeyConstraintsReady {
		return errors.New("survey assessment business key constraints are not ready")
	}
	var legacyOperationStructureReady bool
	err = pool.QueryRow(ctx, `SELECT
		EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='survey_operation_configurations' AND column_name='completion_target' AND data_type='jsonb' AND is_nullable='NO')
		AND EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='survey_operation_configurations' AND column_name='lead_qr_title' AND data_type='text' AND is_nullable='NO')
		AND EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='survey_operation_configurations' AND column_name='lead_qr_subtitle' AND data_type='text' AND is_nullable='NO')
		AND EXISTS(SELECT 1 FROM pg_constraint c JOIN pg_class rel ON rel.oid=c.conrelid JOIN pg_namespace ns ON ns.oid=rel.relnamespace WHERE ns.nspname=current_schema() AND rel.relname='survey_operation_configurations' AND c.conname='survey_operation_completion_target_object' AND pg_get_constraintdef(c.oid) LIKE '%jsonb_typeof(completion_target) = ''object''%')
		AND EXISTS(SELECT 1 FROM pg_constraint c JOIN pg_class rel ON rel.oid=c.conrelid JOIN pg_namespace ns ON ns.oid=rel.relnamespace WHERE ns.nspname=current_schema() AND rel.relname='survey_operation_configurations' AND c.conname='survey_operation_lead_qr_title_length' AND pg_get_constraintdef(c.oid) LIKE '%length(lead_qr_title) <= 40%')
		AND EXISTS(SELECT 1 FROM pg_constraint c JOIN pg_class rel ON rel.oid=c.conrelid JOIN pg_namespace ns ON ns.oid=rel.relnamespace WHERE ns.nspname=current_schema() AND rel.relname='survey_operation_configurations' AND c.conname='survey_operation_lead_qr_subtitle_length' AND pg_get_constraintdef(c.oid) LIKE '%length(lead_qr_subtitle) <= 100%')`).Scan(&legacyOperationStructureReady)
	if err != nil {
		return err
	}
	if !legacyOperationStructureReady {
		return errors.New("survey legacy operation completion schema is not ready")
	}
	return nil
}
