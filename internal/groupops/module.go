package groupops

import (
	"context"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	groupopshttp "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/http"
	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
)

// ModuleRegistration is the Group Ops composition boundary. The module
// publishes local HTTP/runtime bindings and readiness only; donor frontend
// files remain in the existing web build and are mounted by UI adapter.
type ModuleRegistration struct{}

type HTTPBindings struct {
	GroupOps http.Handler
	UI       http.Handler
}

func NewModuleRegistration() *ModuleRegistration { return &ModuleRegistration{} }

func (m *ModuleRegistration) Bind(application groupopshttp.Application, runtime groupopshttp.RuntimeApplication, security groupopshttp.RequestSecurity, protocols groupopshttp.ProtocolAuthenticator, contentDelivery ...mediaport.ContentDeliveryService) (HTTPBindings, error) {
	if m == nil || application == nil || runtime == nil || security == nil {
		return HTTPBindings{}, errors.New("Group Ops module dependencies are required")
	}
	handler, err := groupopshttp.NewHandlerWithRuntime(application, runtime, security, protocols, contentDelivery...)
	if err != nil {
		return HTTPBindings{}, err
	}
	return HTTPBindings{GroupOps: handler}, nil
}

// BindWithHistory adds the minimal v3 host binding needed by the frozen donor
// history pages. The donor files still own the URL, markup and interaction;
// this only supplies their authenticated local read transport.
func (m *ModuleRegistration) BindWithHistory(application groupopshttp.Application, runtime groupopshttp.RuntimeApplication, history groupopshttp.HistoryApplication, security groupopshttp.RequestSecurity, protocols groupopshttp.ProtocolAuthenticator, contentDelivery ...mediaport.ContentDeliveryService) (HTTPBindings, error) {
	if m == nil || application == nil || runtime == nil || history == nil || security == nil {
		return HTTPBindings{}, errors.New("Group Ops history module dependencies are required")
	}
	handler, err := groupopshttp.NewHandlerWithRuntimeAndHistory(application, runtime, history, security, protocols, contentDelivery...)
	if err != nil {
		return HTTPBindings{}, err
	}
	return HTTPBindings{GroupOps: handler}, nil
}

func (m *ModuleRegistration) Readiness(ctx context.Context, pool *pgxpool.Pool) error {
	if m == nil || pool == nil {
		return errors.New("Group Ops module dependencies are required")
	}
	var ready bool
	err := pool.QueryRow(ctx, `SELECT
NOT EXISTS (SELECT 1 FROM unnest(ARRAY['group_ops_plans','group_ops_plan_members','group_ops_plan_group_assets','group_ops_plan_nodes','group_ops_plan_webhook_descriptors','group_ops_operation_receipts','group_ops_audit_events','group_ops_outbox','group_ops_runs','group_ops_executions','group_ops_execution_intents','group_ops_group_message_tasks','group_ops_directory_groups','group_ops_directory_refresh_receipts','group_ops_protocol_replays','group_ops_operation_member_directory','group_ops_v1_history_plans','group_ops_v1_history_directory','group_ops_v1_history_groups','group_ops_v1_history_nodes','group_ops_v1_history_import_batches','group_ops_v1_history_import_rows']) AS required(name) WHERE to_regclass(current_schema() || '.' || required.name) IS NULL)
AND EXISTS (SELECT 1 FROM pg_indexes WHERE schemaname=current_schema() AND indexname='group_ops_plan_webhook_descriptors_configured_reference_unique' AND indexdef LIKE '%WHERE (reference <> ''''::text)%')
AND EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='group_ops_v1_history_plans' AND column_name='source_created_by_reference')
AND EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='group_ops_v1_history_directory' AND column_name='source_owner_reference')
AND EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='group_ops_plans' AND column_name='plan_type')
AND EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='group_ops_runs' AND column_name='webhook_payload_digest')
AND EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='group_ops_executions' AND column_name='node_id' AND is_nullable='YES')
AND EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='group_ops_execution_intents' AND column_name='node_id' AND is_nullable='YES')
AND EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='group_ops_protocol_replays' AND column_name='protocol_version')
AND EXISTS (SELECT 1 FROM pg_indexes WHERE schemaname=current_schema() AND indexname='group_ops_executions_webhook_run_target_unique')
AND EXISTS (SELECT 1 FROM pg_indexes WHERE schemaname=current_schema() AND indexname='group_ops_execution_intents_webhook_run_target_unique')
AND NOT EXISTS (SELECT 1 FROM unnest(ARRAY['day_index','scheduled_time','trigger_time_label','action_title','node_status']) AS required(name) WHERE NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='group_ops_plan_nodes' AND column_name=required.name))`).Scan(&ready)
	if err != nil {
		return err
	}
	if !ready {
		return errors.New("Group Ops schema is not ready")
	}
	var migrationTable bool
	if err = pool.QueryRow(ctx, `SELECT to_regclass(current_schema() || '.platform_schema_migrations') IS NOT NULL`).Scan(&migrationTable); err != nil {
		return err
	}
	if !migrationTable {
		return errors.New("Group Ops schema is not ready")
	}
	if err = pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM platform_schema_migrations WHERE version='0078' AND name='0078_group_ops_provider_tasks.sql')
AND EXISTS (SELECT 1 FROM platform_schema_migrations WHERE version='0081' AND name='0081_group_ops_webhook_unconfigured_reference.sql')
AND EXISTS (SELECT 1 FROM platform_schema_migrations WHERE version='0082' AND name='0082_group_ops_history_import.sql')
AND EXISTS (SELECT 1 FROM platform_schema_migrations WHERE version='0101' AND name='0101_group_ops_ui_metadata.sql')
AND EXISTS (SELECT 1 FROM platform_schema_migrations WHERE version='0116' AND name='0116_group_ops_operation_member_directory.sql')
AND EXISTS (SELECT 1 FROM platform_schema_migrations WHERE version='0155' AND name='0155_group_ops_webhook_dynamic_executions.sql')`).Scan(&ready); err != nil {
		return err
	}
	if !ready {
		return errors.New("Group Ops schema is not ready")
	}
	return nil
}
