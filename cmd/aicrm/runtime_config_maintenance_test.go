package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	configport "github.com/qianlan33333-png/AI-CRM-v3/internal/config/port"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

// OneID: not involved; the command binds an integration identity, not a person
// or customer. Persistence: Config-owned release, receipts, audit and outbox
// use the existing PostgreSQL Unit of Work. External Effects: not involved;
// the role starts no Provider adapters, workers or runtime application.
func TestParseRuntimeConfigMaintenanceRequestRejectsNullUnknownDuplicateAndTrailingFields(t *testing.T) {
	valid := `{"schema_version":1,"operation_id":"shop-bind-operation-01","expected_base_revision":0,"app_id":"wx-shop-test","provider_enabled":false}`
	if _, err := parseRuntimeConfigMaintenanceRequest([]byte(valid)); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}
	for _, field := range []string{"schema_version", "operation_id", "expected_base_revision", "app_id", "provider_enabled"} {
		fields := map[string]any{
			"schema_version": 1, "operation_id": "shop-bind-operation-01",
			"expected_base_revision": 0, "app_id": "wx-shop-test", "provider_enabled": false,
		}
		fields[field] = nil
		raw, marshalErr := json.Marshal(fields)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		invalid := string(raw)
		if _, err := parseRuntimeConfigMaintenanceRequest([]byte(invalid)); err == nil {
			t.Errorf("%s:null was accepted", field)
		}
	}
	for _, invalid := range []string{
		strings.TrimSuffix(valid, "}") + `,"operation_id":"second-value"}`,
		strings.TrimSuffix(valid, "}") + `,"other":"ignored"}`,
		valid + ` {}`,
		`{"schema_version":1,"operation_id":"short","expected_base_revision":0,"app_id":"wx-shop-test","provider_enabled":false}`,
		`{"schema_version":1,"operation_id":"shop-bind-operation-01","expected_base_revision":-1,"app_id":"wx-shop-test","provider_enabled":false}`,
		`{"schema_version":1,"operation_id":"shop-bind-operation-01","expected_base_revision":0,"app_id":" wx-shop-test","provider_enabled":false}`,
	} {
		if _, err := parseRuntimeConfigMaintenanceRequest([]byte(invalid)); err == nil {
			t.Errorf("invalid request was accepted: %s", invalid)
		}
	}
}

func TestExecuteRuntimeConfigMaintenancePublishesOnlyTwoShopKeysAndReplaysIdempotently(t *testing.T) {
	cfg := runtimeConfigMaintenanceTestConfig()
	defaults, err := runtimeConfigDefaults(cfg)
	if err != nil {
		t.Fatal(err)
	}
	initial := make([]configport.RuntimeSetting, 0, 35)
	for _, setting := range defaults {
		if setting.Key == configport.WeChatShopAppID || setting.Key == configport.WeChatShopProviderEnabled {
			continue
		}
		if len(initial) == 33 {
			break
		}
		initial = append(initial, configport.RuntimeSetting{Key: setting.Key, Value: append([]byte(nil), setting.Value...)})
	}
	if len(initial) != 33 {
		t.Fatalf("expected 33 preserved non-Shop settings, got %d", len(initial))
	}
	initial = append(initial,
		runtimeMaintenanceSetting(t, configport.WeChatShopAppID, ""),
		runtimeMaintenanceSetting(t, configport.WeChatShopProviderEnabled, false),
	)
	fake := newRuntimeConfigMaintenanceFake(initial)
	phaseOne := runtimeConfigMaintenanceRequest{OperationID: "shop-bind-operation-01", ExpectedBaseRevision: 0, AppID: "wx-shop-test", ProviderEnabled: false}
	first, err := executeRuntimeConfigMaintenance(context.Background(), fake, cfg, phaseOne, 0)
	if err != nil {
		t.Fatal(err)
	}
	if first.ReleaseID != 1 || first.Revision != 1 || first.ProviderEnabled || first.AppIDSHA256 != runtimeMaintenanceIDHash("wx-shop-test") {
		t.Fatalf("phase-one receipt=%+v", first)
	}
	if len(fake.lastCreate.Settings) != 2 || fake.lastCreate.Settings[0].Key != configport.WeChatShopAppID || fake.lastCreate.Settings[1].Key != configport.WeChatShopProviderEnabled || fake.lastCreate.ExpectedBaseRevision != 0 || fake.lastCreate.Actor != runtimeConfigMaintenanceActor || fake.lastCreate.IdempotencyKey != "shop-config-maintenance:shop-bind-operation-01:create" {
		t.Fatalf("owner draft command was not sparse, scoped and revision-bound: %+v", fake.lastCreate)
	}
	if got := len(fake.activeSettings); got != 35 {
		t.Fatalf("active setting count=%d, want 33 retained plus 2 Shop settings", got)
	}
	for _, before := range initial[:33] {
		if value, ok := runtimeMaintenanceValue(fake.activeSettings, before.Key); !ok || string(value) != string(before.Value) {
			t.Fatalf("unrequested setting %s changed: got %s want %s", before.Key, value, before.Value)
		}
	}
	if fake.createCalls != 1 || fake.validateCalls != 1 || fake.publishCalls != 1 {
		t.Fatalf("first publication calls create/validate/publish=%d/%d/%d", fake.createCalls, fake.validateCalls, fake.publishCalls)
	}

	phaseOneReplay, err := executeRuntimeConfigMaintenance(context.Background(), fake, cfg, phaseOne, 0)
	if err != nil || phaseOneReplay.ReleaseID != first.ReleaseID || phaseOneReplay.Revision != first.Revision {
		t.Fatalf("same-key phase-one replay receipt=%+v err=%v", phaseOneReplay, err)
	}
	if len(fake.createReceipts) != 1 || len(fake.validateKeys) != 1 || len(fake.publishKeys) != 1 {
		t.Fatal("same-key replay duplicated an owner receipt or mutation")
	}

	cfg.WeChatShop.AppID = "wx-shop-test"
	phaseTwo := runtimeConfigMaintenanceRequest{OperationID: "shop-enable-operation-02", ExpectedBaseRevision: first.Revision, AppID: "wx-shop-test", ProviderEnabled: true}
	second, err := executeRuntimeConfigMaintenance(context.Background(), fake, cfg, phaseTwo, 0)
	if err != nil {
		t.Fatal(err)
	}
	if second.ReleaseID != 2 || !second.ProviderEnabled || second.PreviousProviderEnabled {
		t.Fatalf("phase-two receipt=%+v", second)
	}
	if value, ok := runtimeMaintenanceValue(fake.activeSettings, configport.WeChatShopAppID); !ok || string(value) != `"wx-shop-test"` {
		t.Fatalf("published AppID=%s", value)
	}
	if value, ok := runtimeMaintenanceValue(fake.activeSettings, configport.WeChatShopProviderEnabled); !ok || string(value) != `true` {
		t.Fatalf("published Provider switch=%s", value)
	}
	for _, before := range initial[:33] {
		if value, ok := runtimeMaintenanceValue(fake.activeSettings, before.Key); !ok || string(value) != string(before.Value) {
			t.Fatalf("phase two changed unrelated setting %s", before.Key)
		}
	}
	phaseTwoReplay, err := executeRuntimeConfigMaintenance(context.Background(), fake, cfg, phaseTwo, 0)
	if err != nil || phaseTwoReplay.ReleaseID != second.ReleaseID || phaseTwoReplay.Revision != second.Revision {
		t.Fatalf("same-key phase-two replay receipt=%+v err=%v", phaseTwoReplay, err)
	}
	if len(fake.createReceipts) != 2 || len(fake.validateKeys) != 2 || len(fake.publishKeys) != 2 {
		t.Fatal("same-key enable replay duplicated an owner receipt or mutation")
	}
}

func TestExecuteRuntimeConfigMaintenanceRefusesStaleWrongIdentityAndMissingCredentials(t *testing.T) {
	baseSettings := []configport.RuntimeSetting{
		runtimeMaintenanceSetting(t, configport.AutomationOperationsMaxRecipientsPerRun, 4),
		runtimeMaintenanceSetting(t, configport.AIAgentGenerationEnabled, false),
		runtimeMaintenanceSetting(t, configport.WeChatShopAppID, ""),
		runtimeMaintenanceSetting(t, configport.WeChatShopProviderEnabled, false),
	}
	cfg := runtimeConfigMaintenanceTestConfig()
	fake := newRuntimeConfigMaintenanceFake(baseSettings)
	first, err := executeRuntimeConfigMaintenance(context.Background(), fake, cfg,
		runtimeConfigMaintenanceRequest{OperationID: "shop-bind-operation-01", ExpectedBaseRevision: 0, AppID: "wx-shop-test", ProviderEnabled: false}, 0)
	if err != nil {
		t.Fatal(err)
	}
	before := len(fake.releases)
	stale := runtimeConfigMaintenanceRequest{OperationID: "shop-stale-operation-02", ExpectedBaseRevision: 0, AppID: "wx-shop-test", ProviderEnabled: false}
	if _, err = executeRuntimeConfigMaintenance(context.Background(), fake, cfg, stale, 0); err == nil {
		t.Fatal("stale expected revision was accepted")
	}
	if fake.revision != first.Revision || len(fake.releases) != before {
		t.Fatal("stale revision changed the active Config snapshot")
	}
	wrongIdentity := cfg
	wrongIdentity.WeChatShop.AppID = "wx-other-shop"
	if _, err = executeRuntimeConfigMaintenance(context.Background(), fake, wrongIdentity,
		runtimeConfigMaintenanceRequest{OperationID: "shop-wrong-operation-03", ExpectedBaseRevision: first.Revision, AppID: "wx-shop-test", ProviderEnabled: false}, 0); err == nil {
		t.Fatal("mismatched protected deployment AppID was accepted")
	}
	missingCredentials := cfg
	missingCredentials.WeChatShop.CallbackToken = ""
	if _, err = executeRuntimeConfigMaintenance(context.Background(), fake, missingCredentials,
		runtimeConfigMaintenanceRequest{OperationID: "shop-missing-operation-04", ExpectedBaseRevision: first.Revision, AppID: "wx-shop-test", ProviderEnabled: false}, 0); err == nil {
		t.Fatal("missing protected credential references were accepted")
	}
	if _, err = executeRuntimeConfigMaintenance(context.Background(), fake, cfg,
		runtimeConfigMaintenanceRequest{OperationID: "shop-enable-before-bind-05", ExpectedBaseRevision: first.Revision, AppID: "wx-shop-test", ProviderEnabled: true}, 0); err == nil {
		t.Fatal("Provider enablement was accepted before matching AppID reached protected deployment env")
	}
	if fake.revision != first.Revision || len(fake.releases) != before {
		t.Fatal("refused command changed the active Config snapshot")
	}
}

func TestExecuteRuntimeConfigMaintenanceRequiresRootRoleAndNonRebinding(t *testing.T) {
	cfg := runtimeConfigMaintenanceTestConfig()
	initial := []configport.RuntimeSetting{
		runtimeMaintenanceSetting(t, configport.WeChatShopAppID, "wx-existing-shop"),
		runtimeMaintenanceSetting(t, configport.WeChatShopProviderEnabled, false),
	}
	fake := newRuntimeConfigMaintenanceFake(initial)
	request := runtimeConfigMaintenanceRequest{OperationID: "shop-rebind-operation-01", ExpectedBaseRevision: 0, AppID: "wx-other-shop", ProviderEnabled: false}
	if _, err := executeRuntimeConfigMaintenance(context.Background(), fake, cfg, request, 0); err == nil {
		t.Fatal("nonempty active AppID rebinding was accepted")
	}
	if _, err := executeRuntimeConfigMaintenance(context.Background(), fake, cfg, request, 501); err == nil {
		t.Fatal("non-root execution was accepted")
	}
	cfg.Role = platformconfig.RoleAPI
	if _, err := executeRuntimeConfigMaintenance(context.Background(), fake, cfg, request, 0); err == nil {
		t.Fatal("normal API role entered runtime Config maintenance")
	}
	if fake.createCalls != 0 || fake.validateCalls != 0 || fake.publishCalls != 0 {
		t.Fatal("refused root/role/identity request reached Config owner mutations")
	}
}

func TestRuntimeConfigMaintenanceHydratesStoredAIModelAndRefusesMissingKey(t *testing.T) {
	cfg := runtimeConfigMaintenanceTestConfig()
	cfg.AIGeneration.Enabled = true
	cfg.Effects.ProviderEnabled = true
	modelOwner := &runtimeConfigMaintenanceAIModelSettings{
		configured: true,
		model:      configport.AIModelRuntime{BaseURL: "https://api.deepseek.com/v1", Model: "synthetic-model", APIKey: "synthetic-protected-key"},
	}
	hydrated, err := hydrateStoredAIModelRuntimeConfig(context.Background(), cfg, modelOwner)
	if err != nil {
		t.Fatal(err)
	}
	if hydrated.AIGeneration.BaseURL != modelOwner.model.BaseURL || hydrated.AIGeneration.Model != modelOwner.model.Model || hydrated.AIGeneration.APIKey != modelOwner.model.APIKey {
		t.Fatal("stored AI model runtime settings were not hydrated from Config Owner")
	}
	if !hydrated.AIGeneration.Enabled || !runtimeConfigActivationGuards(hydrated).AIAgentGenerationEnabled {
		t.Fatal("hydration disabled an existing AI capability or failed its protected activation guard")
	}
	if modelOwner.readRuntimeCalls != 1 || modelOwner.saveCalls != 0 {
		t.Fatalf("maintenance hydration made unexpected Config Owner calls: read=%d save=%d", modelOwner.readRuntimeCalls, modelOwner.saveCalls)
	}

	active := []configport.RuntimeSetting{
		runtimeMaintenanceSetting(t, configport.WeChatShopAppID, ""),
		runtimeMaintenanceSetting(t, configport.WeChatShopProviderEnabled, false),
		runtimeMaintenanceSetting(t, configport.AIAgentGenerationEnabled, true),
	}
	fake := newRuntimeConfigMaintenanceFake(active)
	withoutStoredKey := &runtimeConfigMaintenanceAIModelSettings{}
	unhydrated, err := hydrateStoredAIModelRuntimeConfig(context.Background(), cfg, withoutStoredKey)
	if err != nil {
		t.Fatal(err)
	}
	if runtimeConfigActivationGuards(unhydrated).AIAgentGenerationEnabled {
		t.Fatal("AI generation guard accepted a missing stored key")
	}
	request := runtimeConfigMaintenanceRequest{OperationID: "shop-missing-ai-key-01", ExpectedBaseRevision: 0, AppID: "wx-shop-test", ProviderEnabled: false}
	if _, err = executeRuntimeConfigMaintenance(context.Background(), fake, unhydrated, request, 0); err == nil {
		t.Fatal("maintenance proceeded with an active AI capability but no readable stored model key")
	}
	if fake.createCalls != 0 || fake.validateCalls != 0 || fake.publishCalls != 0 {
		t.Fatal("missing stored model key refusal created a Config draft or receipt")
	}
}

type runtimeConfigMaintenanceAIModelSettings struct {
	configured       bool
	model            configport.AIModelRuntime
	err              error
	readRuntimeCalls int
	saveCalls        int
}

func (*runtimeConfigMaintenanceAIModelSettings) ReadAIModel(context.Context) (configport.AIModelSelection, error) {
	return configport.AIModelSelection{}, nil
}

func (f *runtimeConfigMaintenanceAIModelSettings) SaveAIModel(context.Context, configport.AIModelCommand) (configport.AIModelSelection, error) {
	f.saveCalls++
	return configport.AIModelSelection{}, nil
}

func (f *runtimeConfigMaintenanceAIModelSettings) ReadAIModelRuntime(context.Context) (configport.AIModelRuntime, bool, error) {
	f.readRuntimeCalls++
	return f.model, f.configured, f.err
}

var _ configport.AIModelSettings = (*runtimeConfigMaintenanceAIModelSettings)(nil)

func runtimeConfigMaintenanceTestConfig() platformconfig.Runtime {
	return platformconfig.Runtime{
		Role:         platformconfig.RoleRuntimeConfigMaintenance,
		AIGeneration: platformconfig.AIGeneration{Timeout: 30 * time.Second},
		AutomationOperations: platformconfig.AutomationOperations{
			ProviderMode: platformconfig.AutomationProviderDisabled, MaxRecipientsPerRun: 1,
		},
		WorkerLimit: 25,
		WeChatShop: platformconfig.WeChatShop{
			AppSecret: "test-protected-app-secret", CallbackToken: "test-protected-callback-token",
			CallbackEncodingAESKey: "0123456789012345678901234567890123456789012",
		},
	}
}

func runtimeMaintenanceSetting(t *testing.T, key configport.RuntimeSettingKey, value any) configport.RuntimeSetting {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return configport.RuntimeSetting{Key: key, Value: raw}
}

func runtimeMaintenanceIDHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func runtimeMaintenanceValue(settings []configport.RuntimeSetting, key configport.RuntimeSettingKey) ([]byte, bool) {
	for _, setting := range settings {
		if setting.Key == key {
			return setting.Value, true
		}
	}
	return nil, false
}

type runtimeConfigMaintenanceFake struct {
	revision         int64
	nextID           int64
	activeSettings   []configport.RuntimeSetting
	releases         map[int64]configport.RuntimeRelease
	createReceipts   map[string]runtimeConfigMaintenanceFakeReceipt
	validateKeys     map[string]int64
	publishKeys      map[string]int64
	lastCreate       configport.RuntimeReleaseDraftCommand
	createCalls      int
	validateCalls    int
	publishCalls     int
	applicationCalls int
}

type runtimeConfigMaintenanceFakeReceipt struct {
	digest  string
	release configport.RuntimeRelease
}

func newRuntimeConfigMaintenanceFake(settings []configport.RuntimeSetting) *runtimeConfigMaintenanceFake {
	return &runtimeConfigMaintenanceFake{
		activeSettings: append([]configport.RuntimeSetting(nil), settings...),
		releases:       map[int64]configport.RuntimeRelease{},
		createReceipts: map[string]runtimeConfigMaintenanceFakeReceipt{},
		validateKeys:   map[string]int64{}, publishKeys: map[string]int64{},
	}
}

func (f *runtimeConfigMaintenanceFake) ListRuntimeReleases(context.Context, int) (configport.RuntimeReleasePage, error) {
	return configport.RuntimeReleasePage{ActiveRevision: f.revision, Effective: configport.EffectiveSnapshot{Revision: f.revision, Settings: append([]configport.RuntimeSetting(nil), f.activeSettings...)}}, nil
}

func (f *runtimeConfigMaintenanceFake) RuntimeRelease(_ context.Context, id int64) (configport.RuntimeRelease, error) {
	release, ok := f.releases[id]
	if !ok {
		return configport.RuntimeRelease{}, errors.New("not found")
	}
	return release, nil
}

func (f *runtimeConfigMaintenanceFake) CreateRuntimeReleaseDraft(_ context.Context, command configport.RuntimeReleaseDraftCommand) (configport.RuntimeRelease, error) {
	f.createCalls++
	f.lastCreate = command
	commandBytes, _ := json.Marshal(command)
	digest := runtimeMaintenanceIDHash(string(commandBytes))
	receiptKey := command.Actor + "\x00" + command.IdempotencyKey
	if receipt, ok := f.createReceipts[receiptKey]; ok {
		if receipt.digest != digest {
			return configport.RuntimeRelease{}, errors.New("idempotency conflict")
		}
		return receipt.release, nil
	}
	if command.ExpectedBaseRevision != f.revision {
		return configport.RuntimeRelease{}, configport.ErrRuntimeReleaseConflict
	}
	if len(command.Settings) != 2 || command.Settings[0].Key != configport.WeChatShopAppID || command.Settings[1].Key != configport.WeChatShopProviderEnabled {
		return configport.RuntimeRelease{}, errors.New("unexpected settings")
	}
	merged := make(map[configport.RuntimeSettingKey][]byte, len(f.activeSettings)+2)
	for _, setting := range f.activeSettings {
		merged[setting.Key] = append([]byte(nil), setting.Value...)
	}
	for _, setting := range command.Settings {
		merged[setting.Key] = append([]byte(nil), setting.Value...)
	}
	settings := make([]configport.RuntimeSetting, 0, len(merged))
	for key, value := range merged {
		settings = append(settings, configport.RuntimeSetting{Key: key, Value: value})
	}
	f.nextID++
	checksumRaw, _ := json.Marshal(settings)
	checksum := runtimeMaintenanceIDHash(string(checksumRaw))
	release := configport.RuntimeRelease{ID: f.nextID, State: configport.RuntimeReleaseDraft, BaseRevision: f.revision, Settings: settings, Checksum: checksum, CreatedBy: command.Actor}
	f.releases[release.ID] = release
	f.createReceipts[receiptKey] = runtimeConfigMaintenanceFakeReceipt{digest: digest, release: release}
	return release, nil
}

func (f *runtimeConfigMaintenanceFake) ValidateRuntimeRelease(_ context.Context, command configport.RuntimeReleaseMutationCommand) (configport.RuntimeRelease, error) {
	f.validateCalls++
	key := command.Actor + "\x00" + command.IdempotencyKey
	if id, ok := f.validateKeys[key]; ok {
		return f.releases[id], nil
	}
	release, ok := f.releases[command.ReleaseID]
	if !ok || release.State != configport.RuntimeReleaseDraft {
		return configport.RuntimeRelease{}, errors.New("invalid release")
	}
	release.State = configport.RuntimeReleaseValidated
	f.releases[release.ID] = release
	f.validateKeys[key] = release.ID
	return release, nil
}

func (f *runtimeConfigMaintenanceFake) PublishRuntimeRelease(_ context.Context, command configport.RuntimeReleasePublishCommand) (configport.RuntimeRelease, error) {
	f.publishCalls++
	key := command.Actor + "\x00" + command.IdempotencyKey
	if id, ok := f.publishKeys[key]; ok {
		return f.releases[id], nil
	}
	release, ok := f.releases[command.ReleaseID]
	if !ok || release.State != configport.RuntimeReleaseValidated || release.BaseRevision != command.ExpectedBaseRevision || release.Checksum != command.ExpectedChecksum || f.revision != command.ExpectedBaseRevision {
		return configport.RuntimeRelease{}, configport.ErrRuntimeReleaseConflict
	}
	for id, old := range f.releases {
		if old.State == configport.RuntimeReleasePublished {
			old.State = configport.RuntimeReleaseSuperseded
			f.releases[id] = old
		}
	}
	release.State = configport.RuntimeReleasePublished
	f.releases[release.ID] = release
	f.revision = release.ID
	f.activeSettings = append([]configport.RuntimeSetting(nil), release.Settings...)
	f.publishKeys[key] = release.ID
	return release, nil
}

func (*runtimeConfigMaintenanceFake) RollbackRuntimeRelease(context.Context, configport.RuntimeReleaseRollbackCommand) (configport.RuntimeRelease, error) {
	return configport.RuntimeRelease{}, errors.New("unused")
}
func (*runtimeConfigMaintenanceFake) PrepareLegacyRuntimeRecovery(context.Context, configport.RuntimeReleaseLegacyRecoveryCommand) (configport.RuntimeRelease, error) {
	return configport.RuntimeRelease{}, errors.New("unused")
}
func (*runtimeConfigMaintenanceFake) ListRuntimeUsage(context.Context, int64, int) ([]configport.RuntimeUsage, error) {
	return nil, nil
}
func (f *runtimeConfigMaintenanceFake) RecordRuntimeApplication(context.Context, configport.RuntimeApplication) error {
	f.applicationCalls++
	return nil
}
func (*runtimeConfigMaintenanceFake) ListRuntimeApplications(context.Context, int) ([]configport.RuntimeApplication, error) {
	return nil, nil
}
func (*runtimeConfigMaintenanceFake) ProtectedReferenceStatuses(context.Context) ([]configport.ProtectedReferenceStatus, error) {
	return nil, nil
}

var _ configport.RuntimeReleaseApplication = (*runtimeConfigMaintenanceFake)(nil)

func TestRuntimeConfigMaintenanceErrorsDoNotExposeAppID(t *testing.T) {
	request := runtimeConfigMaintenanceRequest{OperationID: "shop-identity-operation-01", ExpectedBaseRevision: 0, AppID: "wx-secret-marker", ProviderEnabled: false}
	cfg := runtimeConfigMaintenanceTestConfig()
	cfg.WeChatShop.AppID = "wx-other-marker"
	fake := newRuntimeConfigMaintenanceFake([]configport.RuntimeSetting{
		runtimeMaintenanceSetting(t, configport.WeChatShopAppID, ""), runtimeMaintenanceSetting(t, configport.WeChatShopProviderEnabled, false),
	})
	_, err := executeRuntimeConfigMaintenance(context.Background(), fake, cfg, request, 0)
	if err == nil || strings.Contains(err.Error(), request.AppID) || strings.Contains(err.Error(), cfg.WeChatShop.AppID) {
		t.Fatalf("maintenance error exposed an integration identifier: %v", err)
	}
	if strings.Contains(err.Error(), "wx-") {
		t.Fatal("maintenance failure disclosed an AppID marker")
	}
}
