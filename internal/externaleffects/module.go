package externaleffects

import (
	"context"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	platformjobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	"github.com/riverqueue/river"
)

// ModuleRegistration is the effects module's stable composition contract.
// The composition root supplies infrastructure and chooses roles; this module
// only publishes HTTP bindings, typed worker registration, and readiness.
type ModuleRegistration struct{ worker *Worker }

type HTTPBindings struct {
	Effects    http.Handler
	PushCenter http.Handler
}

func NewModuleRegistration() *ModuleRegistration {
	return &ModuleRegistration{worker: NewWorker(nil, nil)}
}

// SetProviderAdapter is composition-only wiring. It is intentionally narrow:
// binding a later-enabled connector never changes module routing or HTTP.
func (module *ModuleRegistration) SetProviderAdapter(adapter ProviderAdapter) error {
	if module == nil || module.worker == nil || module.worker.adapter != nil || adapter == nil {
		return ErrInvalid
	}
	module.worker.adapter = adapter
	return nil
}

func (module *ModuleRegistration) RegisterWorkers(workers *river.Workers) error {
	if module == nil || module.worker == nil || workers == nil {
		return ErrInvalid
	}
	return river.AddWorkerSafely[EffectJobArgs](workers, module.worker)
}

func (module *ModuleRegistration) Bind(repository *Repository, security RequestSecurity) (HTTPBindings, error) {
	if module == nil || module.worker == nil || repository == nil || security == nil {
		return HTTPBindings{}, ErrInvalid
	}
	if err := module.worker.BindRepository(repository); err != nil {
		return HTTPBindings{}, err
	}
	effects, err := NewHTTPHandler(repository, security)
	if err != nil {
		return HTTPBindings{}, err
	}
	push, err := NewPushCenterHandler(repository, security)
	if err != nil {
		return HTTPBindings{}, err
	}
	return HTTPBindings{Effects: effects, PushCenter: push}, nil
}

// UIBinding exposes the module-owned, query-restricted asset/page handler;
// the composition root still supplies the v3 shell rendering adapter.
func (module *ModuleRegistration) UIBinding(dist string, render PageRenderer) http.Handler {
	if module == nil {
		return http.NotFoundHandler()
	}
	return NewUIHandler(dist, render)
}

func (module *ModuleRegistration) Readiness(ctx context.Context, pool *pgxpool.Pool) error {
	if module == nil || pool == nil {
		return ErrInvalid
	}
	if err := platformjobqueue.CheckReady(ctx, pool); err != nil {
		return err
	}
	var complete bool
	err := pool.QueryRow(ctx, `SELECT NOT EXISTS (
        SELECT 1 FROM unnest(ARRAY['external_effects','external_effect_generations','external_effect_operation_receipts','external_effect_attempts','external_effect_jobs']) AS required(name)
        WHERE to_regclass(current_schema() || '.' || required.name) IS NULL
    )`).Scan(&complete)
	if err != nil {
		return err
	}
	if !complete {
		return errors.New("external effects schema is not ready")
	}
	return nil
}
