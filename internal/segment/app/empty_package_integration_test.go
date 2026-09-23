package app

import (
	"context"
	"encoding/json"
	"errors"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	segmentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/domain"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
	segmentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/store"
	"testing"
	"time"
)

func TestPostgreSQLEmptyPackageLifecycleAndCoreBinding(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	native, cleanup := scheduleRuntimeDatabase(t, ctx)
	defer cleanup()
	applySegmentRuntimeMigration(t, native, "0183_segment_core_operations.sql")
	applySegmentRuntimeMigration(t, native, "0200_segment_core_recommendation_failure_code.sql")
	wrapped, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := segmentstore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(uow, repo)
	service.allowActivation = true
	cmd := PackageCreateCommand{Name: "等待 AI", CreationMode: "empty", Actor: 7, IdempotencyKey: "empty-package-create-001"}
	pkg, err := service.CreatePackage(ctx, cmd)
	if err != nil {
		t.Fatal(err)
	}
	if pkg.CurrentConfigurationVersionID != nil || pkg.PublishedSnapshotID != nil || pkg.Lifecycle != segmentdomain.Paused {
		t.Fatalf("not empty: %+v", pkg)
	}
	again, err := service.CreatePackage(ctx, cmd)
	if err != nil || again.ID != pkg.ID {
		t.Fatalf("replay: %+v %v", again, err)
	}
	if _, err = service.CurrentConfiguration(ctx, pkg.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("empty configuration: %v", err)
	}
	if _, err = service.TransitionPackage(ctx, VersionCommand{ID: pkg.ID, ExpectedVersion: pkg.Version, Actor: 7, IdempotencyKey: "empty-package-activate-001"}, segmentdomain.Active); !errors.Is(err, ErrNotReady) {
		t.Fatalf("empty activated: %v", err)
	}
	if _, err = service.PutConfiguration(ctx, ConfigurationCommand{PackageID: pkg.ID, ExpectedPackageVersion: pkg.Version, Actor: 7, IdempotencyKey: "empty-package-rule-forbidden", Definition: json.RawMessage(`{"schema_version":1,"template_key":"active_contacts","parameters":{"within_days":"30"}}`)}); !errors.Is(err, ErrConflict) {
		t.Fatalf("empty rule mutation: %v", err)
	}
	copy, err := service.CopyPackage(ctx, VersionCommand{ID: pkg.ID, ExpectedVersion: pkg.Version, Actor: 7, IdempotencyKey: "empty-package-copy-001"})
	if err != nil || copy.ID == pkg.ID || copy.CurrentConfigurationVersionID != nil {
		t.Fatalf("copy %+v %v", copy, err)
	}
	updated, err := service.UpdatePackage(ctx, PackageUpdateCommand{ID: pkg.ID, Name: "改名空包", ExpectedVersion: pkg.Version, Actor: 7, IdempotencyKey: "empty-package-rename-001"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.UpdatePackage(ctx, PackageUpdateCommand{ID: pkg.ID, Name: "旧版本", ExpectedVersion: pkg.Version, Actor: 7, IdempotencyKey: "empty-package-conflict-001"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale update %v", err)
	}
	core := NewCoreOperations(service, repo, nil, nil)
	_, err = core.PutProduct(ctx, CoreProductCommand{Product: segmentport.CoreProduct{ID: 1, PackageID: updated.ID, Name: "产品", Description: "AI 人群", Enabled: true}, Actor: 7, IdempotencyKey: "empty-core-product-bind-001"})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := service.CurrentConfiguration(ctx, pkg.ID)
	if err != nil || !isCoreDefinition(cfg.Definition) {
		t.Fatalf("core config %+v %v", cfg, err)
	}
	bound, err := service.GetPackage(ctx, pkg.ID)
	if err != nil || bound.PublishedSnapshotID == nil {
		t.Fatalf("core snapshot %+v %v", bound, err)
	}
	if _, err = service.CopyPackage(ctx, VersionCommand{ID: bound.ID, ExpectedVersion: bound.Version, Actor: 7, IdempotencyKey: "empty-core-copy-forbidden"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("core copied %v", err)
	}
	legacy, err := service.CreatePackage(ctx, PackageCreateCommand{Name: "原算法包", TemplateKey: "active_contacts", Actor: 7, IdempotencyKey: "empty-legacy-package-001"})
	if err != nil || legacy.CurrentConfigurationVersionID == nil {
		t.Fatalf("legacy %+v %v", legacy, err)
	}
	active, err := service.TransitionPackage(ctx, VersionCommand{ID: legacy.ID, ExpectedVersion: legacy.Version, Actor: 7, IdempotencyKey: "empty-legacy-activate-001"}, segmentdomain.Active)
	if err != nil {
		t.Fatal(err)
	}
	renamed, err := service.UpdatePackage(ctx, PackageUpdateCommand{ID: active.ID, Name: "运行中改名", ExpectedVersion: active.Version, Actor: 7, IdempotencyKey: "empty-legacy-rename-001"})
	if err != nil || renamed.Lifecycle != segmentdomain.Active || *renamed.CurrentConfigurationVersionID != *active.CurrentConfigurationVersionID {
		t.Fatalf("active metadata %+v %v", renamed, err)
	}
}
