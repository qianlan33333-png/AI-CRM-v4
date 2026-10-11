package main

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	configapp "github.com/qianlan33333-png/AI-CRM-v3/internal/config/app"
	configport "github.com/qianlan33333-png/AI-CRM-v3/internal/config/port"
	configstore "github.com/qianlan33333-png/AI-CRM-v3/internal/config/store"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

const runtimeConfigMaintenanceTestDataKey = "01234567890123456789012345678901"

// This opt-in fixture driver is called by the Linux installed-binary smoke
// after it creates a registered loopback database clone. It writes only via
// the existing Config Owner services and is never part of ordinary package
// tests or production startup.
func TestPrepareRuntimeConfigInstalledSmokeFixtureWithStoredAIModel(t *testing.T) {
	if os.Getenv("AICRM_RUNTIME_CONFIG_SMOKE_FIXTURE") != "stored-ai-v1" {
		t.Skip("explicit installed Config smoke fixture flag is not set")
	}
	raw := os.Getenv("AICRM_DATABASE_URL")
	parsed, err := url.Parse(raw)
	if err != nil || raw == "" || (parsed.Hostname() != "localhost" && parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "::1") {
		t.Fatal("installed Config smoke fixture requires its registered loopback acceptance database")
	}
	database := strings.TrimPrefix(parsed.Path, "/")
	if !validCompositionDatabaseName(database) || !strings.HasPrefix(database, "aicrm_test_clone_") {
		t.Fatal("installed Config smoke fixture requires its registered loopback acceptance database")
	}
	if err = requireRegisteredRuntimeConfigSmokeClone(database); err != nil {
		t.Fatalf("installed Config smoke fixture database registration: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatal("open installed Config smoke fixture database")
	}
	defer pool.Close()
	var currentSchema string
	if err = pool.QueryRow(ctx, "SELECT current_schema()").Scan(&currentSchema); err != nil || currentSchema != "public" {
		t.Fatal("installed Config smoke fixture requires its public schema")
	}
	var hasUserRelations bool
	if err = pool.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
		WHERE n.nspname='public' AND c.relkind IN ('r','p','v','m','S') AND c.relname !~ '^(pg_|sql_)'
	)`).Scan(&hasUserRelations); err != nil || hasUserRelations {
		t.Fatal("installed Config smoke fixture requires an empty registered database")
	}
	if err = adminAccessMigrateCompositionSchema(ctx, pool); err != nil {
		t.Fatalf("migrate empty installed Config smoke fixture: %v", err)
	}
	wrapper, err := platformpostgres.Wrap(pool, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapper.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := configstore.NewPostgreSQL(pool, uow)
	if err != nil {
		t.Fatal(err)
	}
	cfg := runtimeConfigMaintenanceTestConfig()
	cfg.Survey.DataKey = strings.Repeat("a", 43)
	cfg.Survey.OAuthScope = "snsapi_userinfo"
	cfg.AIGeneration.Enabled = true
	cfg.AIGeneration.Timeout = 30 * time.Second
	cfg.Effects.ProviderEnabled = true
	modelOwner, err := configapp.NewAIModelService(uow, repository, []byte(cfg.Survey.DataKey))
	if err != nil {
		t.Fatal(err)
	}
	currentModel, err := modelOwner.ReadAIModel(ctx)
	if err != nil || currentModel.Version != 0 || currentModel.Configured {
		t.Fatal("installed Config smoke fixture database is not empty")
	}
	bootstrap, err := newRuntimeConfigReleaseService(cfg, uow, repository)
	if err != nil {
		t.Fatal(err)
	}
	page, err := bootstrap.ListRuntimeReleases(ctx, 1)
	if err != nil || page.ActiveRevision != 0 || len(page.Releases) != 0 {
		t.Fatal("installed Config smoke fixture requires an empty Config release owner")
	}
	if _, err = modelOwner.SaveAIModel(ctx, configport.AIModelCommand{
		Provider: "deepseek", Model: "synthetic-maintenance-model", APIKey: "synthetic-protected-ai-key",
		ExpectedVersion: 0, ActorID: 7,
	}); err != nil {
		t.Fatalf("seed installed smoke AI model through Config Owner: %v", err)
	}
	cfg, err = hydrateStoredAIModelRuntimeConfig(ctx, cfg, modelOwner)
	if err != nil || !runtimeConfigActivationGuards(cfg).AIAgentGenerationEnabled {
		t.Fatal("installed smoke AI model was not readable through Config Owner")
	}
	service, err := newRuntimeConfigReleaseService(cfg, uow, repository)
	if err != nil {
		t.Fatal(err)
	}
	defaults, err := runtimeConfigDefaults(cfg)
	if err != nil {
		t.Fatal(err)
	}
	settings := make([]configport.RuntimeSetting, 0, 33)
	for _, setting := range defaults {
		if setting.Key == configport.WeChatShopAppID || setting.Key == configport.WeChatShopProviderEnabled {
			continue
		}
		if len(settings) == 33 {
			break
		}
		settings = append(settings, configport.RuntimeSetting{Key: setting.Key, Value: append([]byte(nil), setting.Value...)})
	}
	if len(settings) != 33 {
		t.Fatal("installed Config smoke fixture could not seed the current non-Shop snapshot")
	}
	first, err := publishRuntimeConfigMaintenanceTestRelease(ctx, service, 0, "installed-smoke-fixture", "installed-smoke-baseline-create-01", settings)
	if err != nil {
		t.Fatalf("publish installed smoke baseline revision: %v", err)
	}
	if _, err = publishRuntimeConfigMaintenanceTestRelease(ctx, service, first.ID, "installed-smoke-fixture", "installed-smoke-baseline-create-02", settings[:1]); err != nil {
		t.Fatalf("publish installed smoke active revision: %v", err)
	}
	page, err = service.ListRuntimeReleases(ctx, 1)
	if err != nil || page.ActiveRevision != 2 {
		t.Fatal("installed Config smoke fixture did not reach expected revision 2")
	}
	if enabled, readErr := runtimeSettingBool(page.Effective.Settings, configport.AIAgentGenerationEnabled); readErr != nil || !enabled {
		t.Fatal("installed Config smoke fixture lost its active AI generation capability")
	}
	if appID, readErr := runtimeSettingString(page.Effective.Settings, configport.WeChatShopAppID); readErr != nil || appID != "" {
		t.Fatal("installed Config smoke fixture did not keep the Shop AppID unbound")
	}
	if enabled, readErr := runtimeSettingBool(page.Effective.Settings, configport.WeChatShopProviderEnabled); readErr != nil || enabled {
		t.Fatal("installed Config smoke fixture did not keep the Shop Provider disabled")
	}
	t.Log("installed Config smoke fixture ready: active AI generation with Config Owner stored model; Shop AppID empty and Provider disabled")
}

func requireRegisteredRuntimeConfigSmokeClone(database string) error {
	directory, err := compositionPreparationDirectory()
	if err != nil {
		return err
	}
	path := filepath.Join(directory, "database-"+database+".json")
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
		return os.ErrNotExist
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(owner.Uid) != os.Getuid() {
		return os.ErrPermission
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var record compositionDatabasePreparation
	if err = json.Unmarshal(data, &record); err != nil || record.Database != database || record.Purpose != "fixture" || record.Fingerprint != "" {
		return os.ErrPermission
	}
	return nil
}

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
	cfg.Survey.DataKey = runtimeConfigMaintenanceTestDataKey
	cfg.AIGeneration.Enabled = true
	cfg.AIGeneration.Timeout = 30 * time.Second
	cfg.Effects.ProviderEnabled = true
	withoutStoredAI, err := configapp.NewAIModelService(uow, repository, []byte(cfg.Survey.DataKey))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = withoutStoredAI.SaveAIModel(ctx, configport.AIModelCommand{
		Provider: "deepseek", Model: "synthetic-maintenance-model", APIKey: "synthetic-protected-ai-key",
		ExpectedVersion: 0, ActorID: 7,
	}); err != nil {
		t.Fatalf("seed encrypted AI model through Config Owner: %v", err)
	}
	cfgWithoutStoredAI := cfg
	cfg, err = hydrateStoredAIModelRuntimeConfig(ctx, cfg, withoutStoredAI)
	if err != nil {
		t.Fatalf("read encrypted AI model through Config Owner: %v", err)
	}
	if !runtimeConfigActivationGuards(cfg).AIAgentGenerationEnabled || cfg.AIGeneration.APIKey != "synthetic-protected-ai-key" {
		t.Fatal("stored AI model did not restore the existing protected activation guard")
	}
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
	withoutModelService, err := newRuntimeConfigReleaseService(cfgWithoutStoredAI, uow, repository)
	if err != nil {
		t.Fatal(err)
	}
	beforeMissingModel := runtimeConfigMaintenanceOwnerCounts(t, ctx, pool)
	if _, err = executeRuntimeConfigMaintenance(ctx, withoutModelService, cfgWithoutStoredAI,
		runtimeConfigMaintenanceRequest{OperationID: "shop-missing-ai-key-01", ExpectedBaseRevision: 2, AppID: "wx-shop-test", ProviderEnabled: false}, 0); err == nil {
		t.Fatal("maintenance accepted active AI generation without its stored protected key")
	}
	if afterMissingModel := runtimeConfigMaintenanceOwnerCounts(t, ctx, pool); afterMissingModel != beforeMissingModel {
		t.Fatalf("missing AI key refusal wrote Config owner state: before=%+v after=%+v", beforeMissingModel, afterMissingModel)
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
