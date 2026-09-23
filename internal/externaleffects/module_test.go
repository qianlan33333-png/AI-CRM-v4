package externaleffects

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	platformruntime "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/runtime"
	"github.com/riverqueue/river"
)

func TestModuleRegistrationPublishesTypedWorkerAndRejectsUnboundHTTP(t *testing.T) {
	registration := NewModuleRegistration()
	workers := river.NewWorkers()
	if err := registration.RegisterWorkers(workers); err != nil {
		t.Fatal(err)
	}
	if _, err := registration.Bind(nil, nil); err != ErrInvalid {
		t.Fatalf("unbound module error=%v", err)
	}
}

func TestModuleReadinessReturns503WhenRiverSchemaIsMissing(t *testing.T) {
	pool, cleanup := effectIntegrationPool(t)
	defer cleanup()
	registration := NewModuleRegistration()
	handler, err := platformruntime.NewHandler(platformruntime.HandlerOptions{ReleaseSHA: "test", Readiness: platformruntime.ReadinessFunc(func(ctx context.Context) error {
		return registration.Readiness(ctx, pool)
	})})
	if err != nil {
		t.Fatal(err)
	}
	ready := httptest.NewRecorder()
	handler.ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if ready.Code != http.StatusOK {
		t.Fatalf("ready status=%d", ready.Code)
	}
	if _, err = pool.Exec(context.Background(), `DROP TABLE river_leader`); err != nil {
		t.Fatal(err)
	}
	notReady := httptest.NewRecorder()
	handler.ServeHTTP(notReady, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if notReady.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing River schema readiness status=%d", notReady.Code)
	}
}
