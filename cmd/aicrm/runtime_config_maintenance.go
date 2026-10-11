package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"unicode/utf8"

	configapp "github.com/qianlan33333-png/AI-CRM-v3/internal/config/app"
	configport "github.com/qianlan33333-png/AI-CRM-v3/internal/config/port"
	configstore "github.com/qianlan33333-png/AI-CRM-v3/internal/config/store"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

const runtimeConfigMaintenanceActor = "ops:root-runtime-config-maintenance"

var runtimeConfigMaintenanceOperationID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{7,119}$`)

type runtimeConfigMaintenanceRequest struct {
	OperationID          string
	ExpectedBaseRevision int64
	AppID                string
	ProviderEnabled      bool
}

type runtimeConfigMaintenanceReceipt struct {
	ReleaseID               int64
	Revision                int64
	State                   string
	Checksum                string
	AppIDSHA256             string
	PreviousProviderEnabled bool
	ProviderEnabled         bool
}

// newRuntimeConfigReleaseService is shared by ordinary composition and the
// one-shot root Config owner command. It creates no adapters, workers, or
// runtime-application receipt.
func newRuntimeConfigReleaseService(cfg platformconfig.Runtime, uow platformport.UnitOfWork,
	repository *configstore.Repository) (*configapp.RuntimeReleaseService, error) {
	defaults, err := runtimeConfigDefaults(cfg)
	if err != nil {
		return nil, err
	}
	limit := cfg.AutomationOperations.MaxRecipientsPerRun
	if limit < 1 {
		limit = 1
	}
	return configapp.NewRuntimeReleaseService(
		uow, repository, repository, limit,
		configapp.WithRuntimeDefaults(defaults),
		configapp.WithProtectedReferencePresence(runtimeConfigProtectedReferencePresence(cfg)),
		configapp.WithRuntimeActivationGuards(runtimeConfigActivationGuards(cfg)),
	)
}

// hydrateStoredAIModelRuntimeConfig uses the same Config Owner read as normal
// composition. The decrypted key stays in process memory and is used only to
// evaluate existing activation guards; this function never calls the model
// Provider or logs the returned credential.
func hydrateStoredAIModelRuntimeConfig(ctx context.Context, cfg platformconfig.Runtime,
	settings configport.AIModelSettings) (platformconfig.Runtime, error) {
	if settings == nil {
		return cfg, errors.New("AI model Config Owner is unavailable")
	}
	stored, configured, err := settings.ReadAIModelRuntime(ctx)
	if err != nil {
		return cfg, err
	}
	if configured {
		cfg.AIGeneration.BaseURL = stored.BaseURL
		cfg.AIGeneration.APIKey = stored.APIKey
		cfg.AIGeneration.Model = stored.Model
	}
	return cfg, nil
}

func parseRuntimeConfigMaintenanceRequest(data []byte) (runtimeConfigMaintenanceRequest, error) {
	var request runtimeConfigMaintenanceRequest
	if len(data) == 0 || len(data) > 8192 {
		return request, errors.New("invalid runtime configuration request size")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return request, errors.New("invalid runtime configuration request")
	}
	fields := make(map[string]json.RawMessage, 5)
	for decoder.More() {
		token, tokenErr := decoder.Token()
		name, ok := token.(string)
		if tokenErr != nil || !ok {
			return request, errors.New("invalid runtime configuration request")
		}
		if _, duplicate := fields[name]; duplicate {
			return request, errors.New("runtime configuration request repeats a field")
		}
		if name != "schema_version" && name != "operation_id" && name != "expected_base_revision" && name != "app_id" && name != "provider_enabled" {
			return request, errors.New("runtime configuration request contains an unsupported field")
		}
		var value json.RawMessage
		if err = decoder.Decode(&value); err != nil {
			return request, errors.New("invalid runtime configuration request")
		}
		fields[name] = value
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim('}') {
		return request, errors.New("invalid runtime configuration request")
	}
	var trailing any
	if err = decoder.Decode(&trailing); err != io.EOF {
		return request, errors.New("runtime configuration request has trailing data")
	}
	if len(fields) != 5 {
		return request, errors.New("runtime configuration request is incomplete")
	}
	var schemaVersion int
	var operationID string
	var expectedRevision int64
	var appID string
	var providerEnabled bool
	for name, target := range map[string]any{
		"schema_version": &schemaVersion, "operation_id": &operationID,
		"expected_base_revision": &expectedRevision, "app_id": &appID,
		"provider_enabled": &providerEnabled,
	} {
		var nonNull any
		if err = json.Unmarshal(fields[name], &nonNull); err != nil || nonNull == nil {
			return request, errors.New("runtime configuration request has an invalid field type")
		}
		if err = json.Unmarshal(fields[name], target); err != nil {
			return request, errors.New("runtime configuration request has an invalid field type")
		}
	}
	request.OperationID = operationID
	request.ExpectedBaseRevision = expectedRevision
	request.AppID = appID
	request.ProviderEnabled = providerEnabled
	if schemaVersion != 1 || !validRuntimeConfigMaintenanceRequest(request) {
		return request, errors.New("runtime configuration request identity is invalid")
	}
	return request, nil
}

func validRuntimeConfigMaintenanceRequest(request runtimeConfigMaintenanceRequest) bool {
	return runtimeConfigMaintenanceOperationID.MatchString(request.OperationID) && request.ExpectedBaseRevision >= 0 &&
		utf8.ValidString(request.AppID) && request.AppID != "" && strings.TrimSpace(request.AppID) == request.AppID &&
		len(request.AppID) <= 256
}

func readProtectedRuntimeConfigMaintenanceRequest(path string, effectiveUID int) (runtimeConfigMaintenanceRequest, error) {
	if effectiveUID != 0 || path == "" || !filepath.IsAbs(path) {
		return runtimeConfigMaintenanceRequest{}, errors.New("runtime configuration maintenance requires OS root and an absolute request path")
	}
	requestPath := filepath.Clean(path)
	before, err := os.Lstat(requestPath)
	if err != nil || !before.Mode().IsRegular() || before.Mode().Perm() != 0o600 {
		return runtimeConfigMaintenanceRequest{}, errors.New("runtime configuration request must be a private regular file")
	}
	owner, ok := before.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != 0 || owner.Nlink != 1 {
		return runtimeConfigMaintenanceRequest{}, errors.New("runtime configuration request must be root-owned and unlinked")
	}
	file, err := os.Open(requestPath)
	if err != nil {
		return runtimeConfigMaintenanceRequest{}, errors.New("runtime configuration request is unavailable")
	}
	defer file.Close()
	after, err := file.Stat()
	pathAfter, pathErr := os.Lstat(requestPath)
	if err != nil || pathErr != nil || !os.SameFile(before, after) || !os.SameFile(after, pathAfter) {
		return runtimeConfigMaintenanceRequest{}, errors.New("runtime configuration request changed while opening")
	}
	data, err := io.ReadAll(io.LimitReader(file, 8193))
	if err != nil || len(data) > 8192 {
		return runtimeConfigMaintenanceRequest{}, errors.New("runtime configuration request is unreadable")
	}
	return parseRuntimeConfigMaintenanceRequest(data)
}

func executeRuntimeConfigMaintenance(ctx context.Context, service configport.RuntimeReleaseApplication,
	cfg platformconfig.Runtime, request runtimeConfigMaintenanceRequest, effectiveUID int) (runtimeConfigMaintenanceReceipt, error) {
	if effectiveUID != 0 || cfg.Role != platformconfig.RoleRuntimeConfigMaintenance || service == nil ||
		!validRuntimeConfigMaintenanceRequest(request) {
		return runtimeConfigMaintenanceReceipt{}, errors.New("runtime configuration maintenance requires the root Config owner role")
	}
	if cfg.WeChatShop.Enabled {
		return runtimeConfigMaintenanceReceipt{}, errors.New("runtime configuration maintenance cannot run with deployment Provider enablement")
	}
	if cfg.WeChatShop.AppID != "" && cfg.WeChatShop.AppID != request.AppID {
		return runtimeConfigMaintenanceReceipt{}, errors.New("request AppID differs from the protected deployment identity")
	}
	guards := runtimeConfigActivationGuards(cfg)
	if !guards.WeChatShopEnabled {
		return runtimeConfigMaintenanceReceipt{}, errors.New("protected WeChat Shop credentials are incomplete")
	}
	page, err := service.ListRuntimeReleases(ctx, 1)
	if err != nil {
		return runtimeConfigMaintenanceReceipt{}, errors.New("active Config revision could not be read")
	}
	currentAppID, err := runtimeSettingString(page.Effective.Settings, configport.WeChatShopAppID)
	if err != nil {
		return runtimeConfigMaintenanceReceipt{}, errors.New("active WeChat Shop identity is invalid")
	}
	if currentAppID != "" && currentAppID != request.AppID {
		return runtimeConfigMaintenanceReceipt{}, errors.New("a nonempty WeChat Shop AppID cannot be rebound")
	}
	currentEnabled, err := runtimeSettingBool(page.Effective.Settings, configport.WeChatShopProviderEnabled)
	if err != nil {
		return runtimeConfigMaintenanceReceipt{}, errors.New("active WeChat Shop Provider switch is invalid")
	}
	currentAIGenerationEnabled, err := runtimeSettingBool(page.Effective.Settings, configport.AIAgentGenerationEnabled)
	if err != nil {
		return runtimeConfigMaintenanceReceipt{}, errors.New("active AI generation switch is invalid")
	}
	if currentAIGenerationEnabled && !guards.AIAgentGenerationEnabled {
		return runtimeConfigMaintenanceReceipt{}, errors.New("active AI generation requires its stored model endpoint, model, protected key, and enabled outbound provider")
	}
	if request.ProviderEnabled && (currentAppID != request.AppID || cfg.WeChatShop.AppID != request.AppID || !guards.WeChatShopEnabled) {
		return runtimeConfigMaintenanceReceipt{}, errors.New("Provider activation requires the published identity, matching protected AppID, and credential presence")
	}
	appIDValue, _ := json.Marshal(request.AppID)
	enabledValue, _ := json.Marshal(request.ProviderEnabled)
	keyPrefix := "shop-config-maintenance:" + request.OperationID
	actor := runtimeConfigMaintenanceActor
	draft, err := service.CreateRuntimeReleaseDraft(ctx, configport.RuntimeReleaseDraftCommand{
		ExpectedBaseRevision: request.ExpectedBaseRevision,
		Settings: []configport.RuntimeSetting{
			{Key: configport.WeChatShopAppID, Value: appIDValue},
			{Key: configport.WeChatShopProviderEnabled, Value: enabledValue},
		},
		Actor: actor, IdempotencyKey: keyPrefix + ":create",
	})
	if err != nil {
		return runtimeConfigMaintenanceReceipt{}, errors.New("Config draft creation failed or the expected revision is stale")
	}
	if draft.ID < 1 || draft.BaseRevision != request.ExpectedBaseRevision || draft.Checksum == "" {
		return runtimeConfigMaintenanceReceipt{}, errors.New("Config draft identity differs from the requested revision")
	}
	if page.ActiveRevision == request.ExpectedBaseRevision && draft.State != configport.RuntimeReleasePublished && draft.State != configport.RuntimeReleaseSuperseded {
		if err = runtimeConfigSnapshotChangesOnly(page.Effective.Settings, draft.Settings, request); err != nil {
			return runtimeConfigMaintenanceReceipt{}, errors.New("Config draft did not preserve unrelated active settings")
		}
	}
	validated, err := service.ValidateRuntimeRelease(ctx, configport.RuntimeReleaseMutationCommand{
		ReleaseID: draft.ID, Actor: actor, IdempotencyKey: keyPrefix + ":validate",
	})
	if err != nil || validated.ID != draft.ID || (validated.State != configport.RuntimeReleaseValidated &&
		validated.State != configport.RuntimeReleasePublished && validated.State != configport.RuntimeReleaseSuperseded) {
		return runtimeConfigMaintenanceReceipt{}, errors.New("Config runtime validation failed")
	}
	published, err := service.PublishRuntimeRelease(ctx, configport.RuntimeReleasePublishCommand{
		ReleaseID: draft.ID, ExpectedBaseRevision: request.ExpectedBaseRevision,
		ExpectedChecksum: draft.Checksum, Actor: actor, IdempotencyKey: keyPrefix + ":publish",
	})
	if err != nil || published.ID != draft.ID || (published.State != configport.RuntimeReleasePublished && published.State != configport.RuntimeReleaseSuperseded) {
		return runtimeConfigMaintenanceReceipt{}, errors.New("Config publish failed or lost its revision comparison")
	}
	if err = runtimeConfigSnapshotChangesOnly(draft.Settings, published.Settings, request); err != nil {
		return runtimeConfigMaintenanceReceipt{}, errors.New("published Config changed an unrequested runtime setting")
	}
	finalAppID, err := runtimeSettingString(published.Settings, configport.WeChatShopAppID)
	if err != nil || finalAppID != request.AppID {
		return runtimeConfigMaintenanceReceipt{}, errors.New("published Config identity differs from the request")
	}
	finalEnabled, err := runtimeSettingBool(published.Settings, configport.WeChatShopProviderEnabled)
	if err != nil || finalEnabled != request.ProviderEnabled {
		return runtimeConfigMaintenanceReceipt{}, errors.New("published Config Provider switch differs from the request")
	}
	appIDDigest := sha256.Sum256([]byte(request.AppID))
	return runtimeConfigMaintenanceReceipt{ReleaseID: published.ID, Revision: published.ID,
		State: string(published.State), Checksum: published.Checksum,
		AppIDSHA256: hex.EncodeToString(appIDDigest[:]), PreviousProviderEnabled: currentEnabled,
		ProviderEnabled: finalEnabled}, nil
}

func runtimeConfigSnapshotChangesOnly(before, after []configport.RuntimeSetting,
	request runtimeConfigMaintenanceRequest) error {
	expected := make(map[configport.RuntimeSettingKey][]byte, len(before))
	actual := make(map[configport.RuntimeSettingKey][]byte, len(after))
	for _, setting := range before {
		expected[setting.Key] = append([]byte(nil), setting.Value...)
	}
	for _, setting := range after {
		if _, duplicate := actual[setting.Key]; duplicate {
			return errors.New("duplicate runtime setting")
		}
		actual[setting.Key] = append([]byte(nil), setting.Value...)
	}
	appID, _ := json.Marshal(request.AppID)
	enabled, _ := json.Marshal(request.ProviderEnabled)
	expected[configport.WeChatShopAppID] = appID
	expected[configport.WeChatShopProviderEnabled] = enabled
	if len(expected) != len(actual) {
		return errors.New("runtime snapshot setting count changed")
	}
	for key, value := range expected {
		if string(actual[key]) != string(value) {
			return errors.New("unrequested runtime setting changed")
		}
	}
	return nil
}

func runtimeSettingString(settings []configport.RuntimeSetting, key configport.RuntimeSettingKey) (string, error) {
	for _, setting := range settings {
		if setting.Key == key {
			var value string
			if err := json.Unmarshal(setting.Value, &value); err != nil {
				return "", err
			}
			return value, nil
		}
	}
	return "", errors.New("runtime setting is absent")
}

func runtimeSettingBool(settings []configport.RuntimeSetting, key configport.RuntimeSettingKey) (bool, error) {
	for _, setting := range settings {
		if setting.Key == key {
			var value bool
			if err := json.Unmarshal(setting.Value, &value); err != nil {
				return false, err
			}
			return value, nil
		}
	}
	return false, errors.New("runtime setting is absent")
}

func runRuntimeConfigMaintenance(ctx context.Context, cfg platformconfig.Runtime, effectiveUID int) (runtimeConfigMaintenanceReceipt, error) {
	if effectiveUID != 0 || cfg.Role != platformconfig.RoleRuntimeConfigMaintenance || cfg.RuntimeConfigRequestFile == "" {
		return runtimeConfigMaintenanceReceipt{}, errors.New("runtime configuration maintenance requires OS root")
	}
	request, err := readProtectedRuntimeConfigMaintenanceRequest(cfg.RuntimeConfigRequestFile, effectiveUID)
	if err != nil {
		return runtimeConfigMaintenanceReceipt{}, err
	}
	pool, err := platformpostgres.Open(ctx, platformpostgres.Config{URL: cfg.DatabaseURL, MaxConnections: 2, MinConnections: 1})
	if err != nil {
		return runtimeConfigMaintenanceReceipt{}, errors.New("Config database is unavailable")
	}
	defer pool.Close()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		return runtimeConfigMaintenanceReceipt{}, errors.New("Config transaction boundary is unavailable")
	}
	repository, err := configstore.NewPostgreSQL(pool.Native(), uow)
	if err != nil {
		return runtimeConfigMaintenanceReceipt{}, errors.New("Config owner repository is unavailable")
	}
	aiModelSettings, err := configapp.NewAIModelService(uow, repository, []byte(cfg.Survey.DataKey))
	if err != nil {
		return runtimeConfigMaintenanceReceipt{}, errors.New("AI model Config Owner is unavailable")
	}
	cfg, err = hydrateStoredAIModelRuntimeConfig(ctx, cfg, aiModelSettings)
	if err != nil {
		return runtimeConfigMaintenanceReceipt{}, errors.New("stored AI model runtime configuration is unavailable")
	}
	service, err := newRuntimeConfigReleaseService(cfg, uow, repository)
	if err != nil {
		return runtimeConfigMaintenanceReceipt{}, errors.New("Config owner service is unavailable")
	}
	return executeRuntimeConfigMaintenance(ctx, service, cfg, request, effectiveUID)
}
