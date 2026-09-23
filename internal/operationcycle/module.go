package operationcycle

import (
	"context"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	operationapp "github.com/qianlan33333-png/AI-CRM-v3/internal/operationcycle/app"
	operationhttp "github.com/qianlan33333-png/AI-CRM-v3/internal/operationcycle/http"
)

type ModuleRegistration struct{}
type HTTPBindings struct{ API http.Handler }

func NewModuleRegistration() *ModuleRegistration { return &ModuleRegistration{} }

func (m *ModuleRegistration) Bind(service *operationapp.Service, security operationhttp.RequestSecurity, serviceToken string) (HTTPBindings, error) {
	if m == nil {
		return HTTPBindings{}, errors.New("operation-cycle module is required")
	}
	handler, err := operationhttp.NewHandler(service, security, serviceToken)
	return HTTPBindings{API: handler}, err
}

func (m *ModuleRegistration) Readiness(ctx context.Context, pool *pgxpool.Pool) error {
	if m == nil || pool == nil {
		return errors.New("operation-cycle module dependencies are required")
	}
	var ready bool
	err := pool.QueryRow(ctx, `SELECT NOT EXISTS (SELECT 1 FROM unnest(ARRAY[
		'operation_cycle_strategies','operation_cycle_runs','operation_cycle_report_receipts',
		'operation_cycle_runners','operation_cycle_action_requests','operation_cycle_action_request_events',
		'operation_cycle_strategy_proposals','operation_cycle_strategy_versions',
		'operation_cycle_run_versions','operation_cycle_run_ordinals','operation_cycle_admin_receipts']) AS required(name)
		WHERE to_regclass(current_schema() || '.' || required.name) IS NULL)`).Scan(&ready)
	if err != nil {
		return err
	}
	if !ready {
		return errors.New("operation-cycle schema is not ready")
	}
	return nil
}
