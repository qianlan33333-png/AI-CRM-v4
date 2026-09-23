package module

import (
	"context"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	radarhttp "github.com/qianlan33333-png/AI-CRM-v3/internal/radar/http"
	radarport "github.com/qianlan33333-png/AI-CRM-v3/internal/radar/port"
)

type ModuleRegistration struct{}
type HTTPBindings struct{ Radar http.Handler }

func NewModuleRegistration() *ModuleRegistration { return &ModuleRegistration{} }
func (m *ModuleRegistration) Bind(manager radarport.Manager, query radarport.QueryService, public radarport.PublicService, security radarhttp.RequestSecurity, origin string) (HTTPBindings, error) {
	if m == nil {
		return HTTPBindings{}, errors.New("radar module is required")
	}
	handler, err := radarhttp.NewHandler(manager, query, public, security, origin)
	if err != nil {
		return HTTPBindings{}, err
	}
	return HTTPBindings{Radar: handler}, nil
}
func (m *ModuleRegistration) Readiness(ctx context.Context, pool *pgxpool.Pool) error {
	if m == nil || pool == nil {
		return errors.New("radar module dependencies are required")
	}
	var ready bool
	err := pool.QueryRow(ctx, `SELECT NOT EXISTS (SELECT 1 FROM unnest(ARRAY['radar_links','radar_link_versions','radar_operation_receipts','radar_audit_events','radar_outbox','radar_oauth_states','radar_view_sessions','radar_events','radar_migration_batches','radar_migration_source_map','radar_migration_quarantine','radar_legacy_events']) AS required(name) WHERE to_regclass(current_schema() || '.' || required.name) IS NULL)`).Scan(&ready)
	if err != nil {
		return err
	}
	if !ready {
		return errors.New("radar schema is not ready")
	}
	return nil
}
