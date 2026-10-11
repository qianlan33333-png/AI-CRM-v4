package main

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	configport "github.com/qianlan33333-png/AI-CRM-v3/internal/config/port"
	configstore "github.com/qianlan33333-png/AI-CRM-v3/internal/config/store"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// OneID: not involved; only the WeChat Shop integration ID is bound. Config
// release values, command receipts, audit and outbox are persisted by their
// existing Owner through one PostgreSQL UoW. No Provider or worker starts.
func TestPostgreSQLRuntimeConfigMaintenancePreservesSettingsAndOwnerReceipts(t *testing.T) {
	raw, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("isolated local PostgreSQL acceptance URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	databaseURL, cleanup := preparedCompositionDatabase(t, ctx, raw)
	defer cleanup()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal("open isolated Config database")
	}
	defer pool.Close()
	wrapper, err := platformpostgres.Wrap(pool, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := configstore.NewPostgreSQL(pool, uow)
	if err != nil {
		t.Fatal(err)
	}
	cfg := runtimeConfigMaintenanceTestConfig()
	service, err := newRuntimeConfigReleaseService(cfg, uow, repository)
	if err != nil {
		t.Fatal(err)
	}
	defaults, err := runtimeConfigDefaults(cfg)
	if err != nil {
		t.Fatal(err)
	}
	seedSettings := make([]configport.RuntimeSetting, 0, 35)
	var preserved []configport.RuntimeSetting
	for _, setting := range defaults {
		if setting.Key == configport.WeChatShopAppID || setting.Key == configport.WeChatShopProviderEnabled {
			continue
		}
		if len(preserved) == 33 {
			break
		}
		copy := configport.RuntimeSetting{Key: setting.Key, Value: append([]byte(nil), setting.Value...)}
		preserved = append(preserved, copy)
		seedSettings = append(seedSettings, copy)
	}
	if len(preserved) != 33 {
		t.Fatalf("Config defaults contain only %d non-Shop settings", len(preserved))
	}
	baselineOne, err := publishRuntimeConfigMaintenanceTestRelease(ctx, service, 0, "maintenance-test-baseline", "shop-maintenance-baseline-create-01", seedSettings)
	if err != nil {
		t.Fatalf("seed revision one: %v", err)
	}
	baselineTwoSettings := []configport.RuntimeSetting{preserved[0]}
	baselineTwo, err := publishRuntimeConfigMaintenanceTestRelease(ctx, service, baselineOne.ID, "maintenance-test-baseline", "shop-maintenance-baseline-create-02", baselineTwoSettings)
	if err != nil {
		t.Fatalf("seed revision two: %v", err)
	}
	if baselineTwo.ID != 2 {
		t.Fatalf("fixture active revision=%d, want 2", baselineTwo.ID)
	}

	page, err := service.ListRuntimeReleases(ctx, 1)
	if err != nil || page.ActiveRevision != 2 {
		t.Fatalf("read fixture revision: active=%d err=%v", page.ActiveRevision, err)
	}
	for _, setting := range preserved {
		value, ok := runtimeMaintenanceValue(page.Effective.Settings, setting.Key)
		if !ok || string(value) != string(setting.Value) {
			t.Fatalf("fixture setting %s differs from its seed", setting.Key)
		}
	}

	phaseOne := runtimeConfigMaintenanceRequest{OperationID: "shop-bind-operation-01", ExpectedBaseRevision: 2, AppID: "wx-shop-test", ProviderEnabled: false}
	first, err := executeRuntimeConfigMaintenance(ctx, service, cfg, phaseOne, 0)
	if err != nil || first.Revision != 3 || first.ProviderEnabled {
		t.Fatalf("bind AppID while disabled: receipt=%+v err=%v", first, err)
	}
	firstReplay, err := executeRuntimeConfigMaintenance(ctx, service, cfg, phaseOne, 0)
	if err != nil || firstReplay.ReleaseID != first.ReleaseID || firstReplay.Revision != first.Revision {
		t.Fatalf("phase-one replay=%+v err=%v", firstReplay, err)
	}
	changedReplay := phaseOne
	changedReplay.ExpectedBaseRevision = first.Revision
	if _, err = executeRuntimeConfigMaintenance(ctx, service, cfg, changedReplay, 0); err == nil {
		t.Fatal("reusing an operation id with a different expected revision was accepted")
	}

	cfg.WeChatShop.AppID = "wx-shop-test"
	service, err = newRuntimeConfigReleaseService(cfg, uow, repository)
	if err != nil {
		t.Fatal(err)
	}
	phaseTwo := runtimeConfigMaintenanceRequest{OperationID: "shop-enable-operation-02", ExpectedBaseRevision: first.Revision, AppID: "wx-shop-test", ProviderEnabled: true}
	second, err := executeRuntimeConfigMaintenance(ctx, service, cfg, phaseTwo, 0)
	if err != nil || second.Revision != 4 || !second.ProviderEnabled {
		t.Fatalf("enable after protected env binding: receipt=%+v err=%v", second, err)
	}
	secondReplay, err := executeRuntimeConfigMaintenance(ctx, service, cfg, phaseTwo, 0)
	if err != nil || secondReplay.ReleaseID != second.ReleaseID || secondReplay.Revision != second.Revision {
		t.Fatalf("phase-two replay=%+v err=%v", secondReplay, err)
	}
	final, err := service.ListRuntimeReleases(ctx, 1)
	if err != nil || final.ActiveRevision != second.Revision {
		t.Fatalf("active revision after maintenance=%d err=%v", final.ActiveRevision, err)
	}
	for _, setting := range preserved {
		value, ok := runtimeMaintenanceValue(final.Effective.Settings, setting.Key)
		if !ok || string(value) != string(setting.Value) {
			t.Fatalf("maintenance changed non-Shop setting %s", setting.Key)
		}
	}
	if value, ok := runtimeMaintenanceValue(final.Effective.Settings, configport.WeChatShopAppID); !ok || string(value) != `"wx-shop-test"` {
		t.Fatal("active AppID was not bound to the requested integration")
	}
	if value, ok := runtimeMaintenanceValue(final.Effective.Settings, configport.WeChatShopProviderEnabled); !ok || string(value) != `true` {
		t.Fatal("active Provider switch was not enabled by the second release")
	}

	beforeRefusal := runtimeConfigMaintenanceOwnerCounts(t, ctx, pool)
	stale := runtimeConfigMaintenanceRequest{OperationID: "shop-stale-operation-03", ExpectedBaseRevision: first.Revision, AppID: "wx-shop-test", ProviderEnabled: false}
	if _, err = executeRuntimeConfigMaintenance(ctx, service, cfg, stale, 0); err == nil {
		t.Fatal("stale expected revision was accepted")
	}
	wrongDeployment := cfg
	wrongDeployment.WeChatShop.AppID = "wx-other-shop"
	if _, err = executeRuntimeConfigMaintenance(ctx, service, wrongDeployment,
		runtimeConfigMaintenanceRequest{OperationID: "shop-env-mismatch-04", ExpectedBaseRevision: second.Revision, AppID: "wx-shop-test", ProviderEnabled: false}, 0); err == nil {
		t.Fatal("request AppID differing from protected deployment identity was accepted")
	}
	missingCredential := cfg
	missingCredential.WeChatShop.CallbackToken = ""
	if _, err = executeRuntimeConfigMaintenance(ctx, service, missingCredential,
		runtimeConfigMaintenanceRequest{OperationID: "shop-missing-credential-05", ExpectedBaseRevision: second.Revision, AppID: "wx-shop-test", ProviderEnabled: false}, 0); err == nil {
		t.Fatal("missing protected callback credential was accepted")
	}
	rebind := cfg
	rebind.WeChatShop.AppID = "wx-other-shop"
	if _, err = executeRuntimeConfigMaintenance(ctx, service, rebind,
		runtimeConfigMaintenanceRequest{OperationID: "shop-rebind-operation-06", ExpectedBaseRevision: second.Revision, AppID: "wx-other-shop", ProviderEnabled: false}, 0); err == nil {
		t.Fatal("nonempty active AppID rebinding was accepted")
	}
	if afterRefusal := runtimeConfigMaintenanceOwnerCounts(t, ctx, pool); afterRefusal != beforeRefusal {
		t.Fatalf("refused request wrote Config owner state: before=%+v after=%+v", beforeRefusal, afterRefusal)
	}
	if beforeRefusal.Receipts != 6 || beforeRefusal.Audits != 6 || beforeRefusal.Outbox != 2 || beforeRefusal.Applications != 0 || beforeRefusal.AdminSessions != 0 {
		t.Fatalf("unexpected Config owner or session rows after idempotent run: %+v", beforeRefusal)
	}
}

func publishRuntimeConfigMaintenanceTestRelease(ctx context.Context, service configport.RuntimeReleaseApplication, base int64, actor, createKey string, settings []configport.RuntimeSetting) (configport.RuntimeRelease, error) {
	draft, err := service.CreateRuntimeReleaseDraft(ctx, configport.RuntimeReleaseDraftCommand{ExpectedBaseRevision: base, Settings: settings, Actor: actor, IdempotencyKey: createKey})
	if err != nil {
		return configport.RuntimeRelease{}, err
	}
	validated, err := service.ValidateRuntimeRelease(ctx, configport.RuntimeReleaseMutationCommand{ReleaseID: draft.ID, Actor: actor, IdempotencyKey: createKey + "-validate"})
	if err != nil {
		return configport.RuntimeRelease{}, err
	}
	return service.PublishRuntimeRelease(ctx, configport.RuntimeReleasePublishCommand{ReleaseID: validated.ID, ExpectedBaseRevision: base, ExpectedChecksum: validated.Checksum, Actor: actor, IdempotencyKey: createKey + "-publish"})
}

type runtimeConfigMaintenanceOwnerCount struct {
	Receipts, Audits, Outbox, Applications, AdminSessions int
}

func runtimeConfigMaintenanceOwnerCounts(t *testing.T, ctx context.Context, pool *pgxpool.Pool) runtimeConfigMaintenanceOwnerCount {
	t.Helper()
	var counts runtimeConfigMaintenanceOwnerCount
	err := pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM config_runtime_release_command_receipts WHERE actor=$1),
		(SELECT count(*) FROM config_runtime_release_audits WHERE actor=$1),
		(SELECT count(*) FROM config_outbox WHERE idempotency_key=ANY($2::text[])),
		(SELECT count(*) FROM config_runtime_applications),
		(SELECT count(*) FROM admin_sessions)`, runtimeConfigMaintenanceActor,
		[]string{"runtime_release.published:release:3", "runtime_release.published:release:4"}).Scan(
		&counts.Receipts, &counts.Audits, &counts.Outbox, &counts.Applications, &counts.AdminSessions)
	if err != nil {
		t.Fatal(err)
	}
	return counts
}
