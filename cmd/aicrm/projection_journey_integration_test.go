package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	adminopsapp "github.com/qianlan33333-png/AI-CRM-v3/internal/adminops/app"
	adminopsport "github.com/qianlan33333-png/AI-CRM-v3/internal/adminops/port"
	adminopsstore "github.com/qianlan33333-png/AI-CRM-v3/internal/adminops/store"
	configapp "github.com/qianlan33333-png/AI-CRM-v3/internal/config/app"
	configport "github.com/qianlan33333-png/AI-CRM-v3/internal/config/port"
	configstore "github.com/qianlan33333-png/AI-CRM-v3/internal/config/store"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func TestPostgreSQLProjectionAndConfigJourney(t *testing.T) {
	native, cleanup := projectionIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	for _, table := range []string{"config_settings", "config_audits", "config_outbox", "adminops_release_projections", "adminops_diagnostic_snapshots"} {
		var owned bool
		if err := native.QueryRow(ctx, `SELECT tableowner=current_user FROM pg_tables WHERE schemaname=current_schema() AND tablename=$1`, table).Scan(&owned); err != nil || !owned {
			t.Fatalf("table %s owner=%t err=%v", table, owned, err)
		}
	}
	wrapped, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	projectionStore, err := adminopsstore.NewProjectionPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	projectionService, err := adminopsapp.NewProjectionService(uow, projectionStore)
	if err != nil {
		t.Fatal(err)
	}
	configRepository, err := configstore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	configManager := configapp.NewManager(uow, configRepository, configRepository)
	actor := "journey-admin-7"
	if _, err = configManager.Set(ctx, configport.SetCommand{Key: configport.WeComCorpID, Value: []byte(`"journey-corp"`), Actor: actor, RequestID: "journey-config-request-1"}); err != nil {
		t.Fatal(err)
	}
	settings := configapp.NewSettingsCompatibilityService(uow, configRepository, configManager, configapp.SecretConfiguredSnapshot{})
	refreshed, err := settings.List(ctx, configapp.SettingsListInput{Scope: "editable"})
	if err != nil {
		t.Fatal(err)
	}
	if !settingsContainValue(refreshed, "wecom.corp_id", "journey-corp") {
		t.Fatalf("persisted settings projection=%#v", refreshed)
	}

	if _, err = projectionService.RecordReleaseProjection(ctx, adminopsport.ReleaseProjection{ReleaseSHA: "6bfbe5816bb89913c70adaca87d6a486260e016e", Status: "observed"}); err != nil {
		t.Fatal(err)
	}
	if _, err = projectionService.RecordDiagnosticSnapshot(ctx, adminopsport.DiagnosticSnapshot{Key: "aicrm.composition", Status: "ok"}); err != nil {
		t.Fatal(err)
	}
	releases, err := projectionService.ListReleaseProjections(ctx)
	if err != nil || len(releases) != 1 || releases[0].ReleaseSHA != "6bfbe5816bb89913c70adaca87d6a486260e016e" {
		t.Fatalf("persisted release projections=%#v err=%v", releases, err)
	}
	diagnostics, err := projectionService.ListDiagnosticSnapshots(ctx)
	if err != nil || len(diagnostics) != 1 || diagnostics[0].Key != "aicrm.composition" || diagnostics[0].Status != "ok" {
		t.Fatalf("persisted diagnostic projections=%#v err=%v", diagnostics, err)
	}
	var audits []configport.ProjectionAudit
	if err = uow.Within(ctx, func(tx context.Context) error {
		var listErr error
		audits, listErr = configRepository.ListAppSettingsAudit(tx)
		return listErr
	}); err != nil {
		t.Fatal(err)
	}
	if len(audits) != 1 || audits[0].Operator != actor || audits[0].TargetID != configport.WeComCorpID {
		t.Fatalf("queryable config audits=%#v", audits)
	}
	var outbox int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM config_outbox WHERE event_type='setting.updated' AND idempotency_key=$1`, "setting.updated:journey-config-request-1").Scan(&outbox); err != nil {
		t.Fatal(err)
	}
	if outbox != 1 {
		t.Fatalf("queryable config outbox rows=%d", outbox)
	}

	// Details are physically present only as an empty storage-owned object and
	// are never selected by the projection store. This guards the response
	// boundary if a future migration adds metadata to these rows.
	var details []byte
	if err = native.QueryRow(ctx, `SELECT details::text FROM adminops_release_projections LIMIT 1`).Scan(&details); err != nil {
		t.Fatal(err)
	}
	if string(details) != "{}" {
		t.Fatalf("unexpected projection details=%s", details)
	}
}

func settingsContainValue(projection configapp.SettingsProjection, key, want string) bool {
	for _, raw := range projection.Rows {
		row, ok := raw.(configapp.EditableSettingRow)
		if ok && string(row.Key) == key && row.Value == want {
			return true
		}
	}
	return false
}

func projectionIntegrationPool(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	raw, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("DATABASE_URL is not configured; skipping AdminOps PostgreSQL integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var random [8]byte
	if _, err = rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	schema := "aicrm_adminops_test_" + hex.EncodeToString(random[:])
	admin, err := pgx.Connect(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		admin.Close(ctx)
		t.Fatal(err)
	}
	config, err := pgxpool.ParseConfig(raw)
	if err != nil {
		admin.Close(ctx)
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		admin.Close(ctx)
		t.Fatal(err)
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		pool.Close()
		admin.Close(ctx)
		t.Fatal("locate test")
	}
	migration, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "migrations", "0015_config_adminops.sql"))
	if err != nil {
		pool.Close()
		admin.Close(ctx)
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, string(migration)); err != nil {
		pool.Close()
		admin.Close(ctx)
		t.Fatalf("apply config/adminops migration: %v", err)
	}
	return pool, func() {
		pool.Close()
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = admin.Exec(cleanup, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close(cleanup)
	}
}
