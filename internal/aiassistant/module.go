package aiassistant

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"
)

type ModuleRegistration struct{}

func NewModuleRegistration() *ModuleRegistration { return &ModuleRegistration{} }

func (m *ModuleRegistration) Readiness(ctx context.Context, pool *pgxpool.Pool) error {
	if m == nil || pool == nil {
		return errors.New("AI Assistant module dependencies are required")
	}
	var ready bool
	err := pool.QueryRow(ctx, `SELECT NOT EXISTS (
		SELECT 1 FROM unnest(ARRAY[
			'ai_assistant_plans','ai_assistant_plan_recipients','ai_assistant_content_versions',
			'ai_assistant_review_decisions','ai_assistant_effect_bindings','ai_assistant_operation_receipts',
			'ai_assistant_integration_nonces','ai_assistant_audit_events','ai_assistant_outbox',
			'ai_assistant_excel_imports','ai_assistant_excel_batch_versions','ai_assistant_excel_batch_version_covers'
		]) AS required(name)
		WHERE to_regclass(current_schema() || '.' || required.name) IS NULL
	) AND NOT EXISTS (
		SELECT 1 FROM (VALUES
			('ai_assistant_excel_imports'::text, 'operation_cycle_strategy_key'::text),
			('ai_assistant_excel_imports'::text, 'source_origin'::text),
			('ai_assistant_excel_imports'::text, 'content_revision'::text),
			('ai_assistant_excel_imports'::text, 'created_at'::text),
			('ai_assistant_plan_recipients'::text, 'excel_batch_content_revision'::text),
			('ai_assistant_plan_recipients'::text, 'excel_segment'::text),
			('ai_assistant_plan_recipients'::text, 'excel_excluded'::text),
			('ai_assistant_excel_batch_versions'::text, 'cover_image_id'::text),
			('ai_assistant_excel_batch_version_covers'::text, 'cover_image_id'::text)
		) AS required(table_name,column_name)
		WHERE NOT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_schema=current_schema()
				AND table_name=required.table_name
				AND column_name=required.column_name
		)
	)`).Scan(&ready)
	if err != nil {
		return err
	}
	if !ready {
		return errors.New("AI Assistant schema is not ready: required plan or Excel lifecycle tables or columns are missing")
	}
	return nil
}
