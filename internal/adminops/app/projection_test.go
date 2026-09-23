package app

import (
	"context"
	"errors"
	"testing"
	"time"

	adminopsport "github.com/qianlan33333-png/AI-CRM-v3/internal/adminops/port"
	configport "github.com/qianlan33333-png/AI-CRM-v3/internal/config/port"
)

func TestProjectionServiceReturnsOnlySafeStoredFacts(t *testing.T) {
	observed := time.Date(2026, 9, 3, 4, 5, 6, 0, time.UTC)
	service, err := NewProjectionService(projectionUnitOfWork{}, &projectionStoreStub{
		releases:    []adminopsport.ReleaseProjection{{ID: 7, ReleaseSHA: "6bfbe5816bb89913c70adaca87d6a486260e016e", Status: "observed", ObservedAt: observed}},
		diagnostics: []adminopsport.DiagnosticSnapshot{{ID: 8, Key: "aicrm.composition", Status: "ok", ObservedAt: observed}},
	})
	if err != nil {
		t.Fatal(err)
	}
	releases, err := service.ListReleaseProjections(context.Background())
	if err != nil || len(releases) != 1 || releases[0].ID != 7 || releases[0].ReleaseSHA == "" {
		t.Fatalf("releases=%#v err=%v", releases, err)
	}
	diagnostics, err := service.ListDiagnosticSnapshots(context.Background())
	if err != nil || len(diagnostics) != 1 || diagnostics[0].Key != "aicrm.composition" || diagnostics[0].Status != "ok" {
		t.Fatalf("diagnostics=%#v err=%v", diagnostics, err)
	}
}

func TestProjectionServiceFailsClosedOnUnsafePersistedProjection(t *testing.T) {
	observed := time.Date(2026, 9, 3, 4, 5, 6, 0, time.UTC)
	service, err := NewProjectionService(projectionUnitOfWork{}, &projectionStoreStub{
		releases: []adminopsport.ReleaseProjection{{ID: 7, ReleaseSHA: "secret-token", Status: "observed", ObservedAt: observed}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.ListReleaseProjections(context.Background()); !errors.Is(err, ErrProjectionUnavailable) {
		t.Fatalf("unsafe release error=%v", err)
	}
	service, err = NewProjectionService(projectionUnitOfWork{}, &projectionStoreStub{
		diagnostics: []adminopsport.DiagnosticSnapshot{{ID: 8, Key: "runtime.secret", Status: "ok", ObservedAt: observed}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.ListDiagnosticSnapshots(context.Background()); !errors.Is(err, ErrProjectionUnavailable) {
		t.Fatalf("unsafe diagnostic error=%v", err)
	}
}

func TestProjectionServiceRecordsBoundedObservations(t *testing.T) {
	store := &projectionStoreStub{
		recordedRelease:    adminopsport.ReleaseProjection{ID: 9, ReleaseSHA: "development", Status: "observed", ObservedAt: time.Now().UTC()},
		recordedDiagnostic: adminopsport.DiagnosticSnapshot{ID: 10, Key: "aicrm.composition", Status: "ok", ObservedAt: time.Now().UTC()},
	}
	service, err := NewProjectionService(projectionUnitOfWork{}, store)
	if err != nil {
		t.Fatal(err)
	}
	release, err := service.RecordReleaseProjection(context.Background(), adminopsport.ReleaseProjection{ReleaseSHA: "development", Status: "observed"})
	if err != nil || release.ID != 9 || store.releaseInput.ReleaseSHA != "development" {
		t.Fatalf("release=%#v input=%#v err=%v", release, store.releaseInput, err)
	}
	diagnostic, err := service.RecordDiagnosticSnapshot(context.Background(), adminopsport.DiagnosticSnapshot{Key: "aicrm.composition", Status: "ok"})
	if err != nil || diagnostic.ID != 10 || store.diagnosticInput.Key != "aicrm.composition" {
		t.Fatalf("diagnostic=%#v input=%#v err=%v", diagnostic, store.diagnosticInput, err)
	}
	if _, err = service.RecordDiagnosticSnapshot(context.Background(), adminopsport.DiagnosticSnapshot{Key: "runtime.password", Status: "ok"}); !errors.Is(err, ErrProjectionUnavailable) {
		t.Fatalf("unsafe record error=%v", err)
	}
}

type projectionUnitOfWork struct{}

func (projectionUnitOfWork) Within(ctx context.Context, callback func(context.Context) error) error {
	return callback(ctx)
}

type projectionStoreStub struct {
	releases           []adminopsport.ReleaseProjection
	diagnostics        []adminopsport.DiagnosticSnapshot
	recordedRelease    adminopsport.ReleaseProjection
	recordedDiagnostic adminopsport.DiagnosticSnapshot
	releaseInput       adminopsport.ReleaseProjection
	diagnosticInput    adminopsport.DiagnosticSnapshot
}

func (stub projectionStoreStub) ListReleaseProjections(context.Context) ([]adminopsport.ReleaseProjection, error) {
	return stub.releases, nil
}

func (stub projectionStoreStub) ListDiagnosticSnapshots(context.Context) ([]adminopsport.DiagnosticSnapshot, error) {
	return stub.diagnostics, nil
}

func (stub *projectionStoreStub) RecordReleaseProjection(_ context.Context, item adminopsport.ReleaseProjection) (adminopsport.ReleaseProjection, error) {
	stub.releaseInput = item
	return stub.recordedRelease, nil
}

func (stub *projectionStoreStub) RecordDiagnosticSnapshot(_ context.Context, item adminopsport.DiagnosticSnapshot) (adminopsport.DiagnosticSnapshot, error) {
	stub.diagnosticInput = item
	return stub.recordedDiagnostic, nil
}

var _ configport.SafeProjectionReader = (*ProjectionService)(nil)
var _ adminopsport.ProjectionStore = (*projectionStoreStub)(nil)
