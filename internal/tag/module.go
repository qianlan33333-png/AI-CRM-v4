package tag

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5/pgxpool"
	tagapp "github.com/qianlan33333-png/AI-CRM-v3/internal/tag/app"
	taghttp "github.com/qianlan33333-png/AI-CRM-v3/internal/tag/http"
	tagport "github.com/qianlan33333-png/AI-CRM-v3/internal/tag/port"
	"net/http"
)

type ModuleRegistration struct{}
type HTTPBindings struct{ Tags http.Handler }

func NewModuleRegistration() *ModuleRegistration { return &ModuleRegistration{} }
func (m *ModuleRegistration) Bind(catalog *tagapp.Service, sync *tagapp.SyncService, gate tagport.ExecutionGateReader, security taghttp.RequestSecurity) (HTTPBindings, error) {
	if m == nil {
		return HTTPBindings{}, errors.New("tag module is required")
	}
	h, e := taghttp.NewHandler(catalog, sync, gate, security)
	return HTTPBindings{Tags: h}, e
}
func (m *ModuleRegistration) Readiness(ctx context.Context, pool *pgxpool.Pool) error {
	if m == nil || pool == nil {
		return errors.New("tag module dependencies are required")
	}
	var ready bool
	e := pool.QueryRow(ctx, `SELECT NOT EXISTS (SELECT 1 FROM unnest(ARRAY['tag_groups','tag_catalog_tags','tag_references','tag_operation_receipts','tag_audit_events','tag_outbox','tag_sync_receipts','tag_provider_observations','tag_provider_group_bindings','tag_provider_tag_bindings']) AS required(name) WHERE to_regclass(current_schema() || '.' || required.name) IS NULL)`).Scan(&ready)
	if e != nil {
		return e
	}
	if !ready {
		return errors.New("tag schema is not ready")
	}
	return nil
}
