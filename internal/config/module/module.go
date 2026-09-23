// Package module exposes Config's stable composition contract without making
// the registry package depend on its application layer.
package module

import (
	"context"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	configapp "github.com/qianlan33333-png/AI-CRM-v3/internal/config/app"
	confighttp "github.com/qianlan33333-png/AI-CRM-v3/internal/config/http"
	configport "github.com/qianlan33333-png/AI-CRM-v3/internal/config/port"
)

type Registration struct{}
type HTTPBindings struct{ Config http.Handler }

func NewRegistration() *Registration { return &Registration{} }
func (m *Registration) Bind(settings *configapp.SettingsCompatibilityService, wizard *configapp.SetupWizardService, configService configport.Service, projections configport.SafeProjectionReader, security confighttp.RequestSecurity, runtime ...configport.RuntimeReleaseApplication) (HTTPBindings, error) {
	if m == nil || len(runtime) > 1 {
		return HTTPBindings{}, errors.New("config module is required")
	}
	h, e := confighttp.NewHandler(settings, wizard, configService, projections, security, runtime...)
	return HTTPBindings{Config: h}, e
}
func (m *Registration) Readiness(ctx context.Context, pool *pgxpool.Pool) error {
	if m == nil || pool == nil {
		return errors.New("config module dependencies are required")
	}
	var ready bool
	e := pool.QueryRow(ctx, `SELECT NOT EXISTS (SELECT 1 FROM unnest(ARRAY['config_settings','config_audits','config_outbox','config_command_receipts','config_runtime_releases','config_runtime_release_values','config_runtime_active_release','config_runtime_release_command_receipts','config_runtime_release_audits','config_runtime_usage','config_runtime_release_history_batches','config_runtime_release_history_rows','config_runtime_applications']) AS required(name) WHERE to_regclass(current_schema() || '.' || required.name) IS NULL)`).Scan(&ready)
	if e != nil {
		return e
	}
	if !ready {
		return errors.New("config schema is not ready")
	}
	return nil
}

func (b HTTPBindings) WithAIModels(service configport.AIModelSettings) HTTPBindings {
	if h, ok := b.Config.(*confighttp.Handler); ok {
		h.WithAIModels(service)
	}
	return b
}
