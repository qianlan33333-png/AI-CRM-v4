package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"

	configapp "github.com/qianlan33333-png/AI-CRM-v3/internal/config/app"
	configport "github.com/qianlan33333-png/AI-CRM-v3/internal/config/port"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

// runtimeConfigDefaults is the composition-owned bridge from the deployment
// environment to Config's closed catalog.  It deliberately excludes every
// secret and arbitrary environment name: those remain protected deployment
// inputs and Config shows only their fixed safe reference.
func runtimeConfigDefaults(cfg platformconfig.Runtime) ([]configport.RuntimeSetting, error) {
	cfg = platformconfig.NormalizeRuntimePolicyDefaults(cfg)
	values := []struct {
		key   configport.RuntimeSettingKey
		value any
	}{
		{configport.AutomationOperationsMaxRecipientsPerRun, cfg.AutomationOperations.MaxRecipientsPerRun},
		{configport.AutomationOperationsProviderMode, string(cfg.AutomationOperations.ProviderMode)},
		{configport.AIAssistantUIEnabled, cfg.AIAssistant.UIEnabled}, {configport.AIAssistantIntakeEnabled, cfg.AIAssistant.IntakeEnabled}, {configport.AIAssistantDispatchEnabled, cfg.AIAssistant.DispatchEnabled},
		{configport.AIAgentGenerationEnabled, cfg.AIGeneration.Enabled},
		{configport.WeComEnabled, cfg.WeCom.Enabled}, {configport.RuntimeWeComCorpID, cfg.WeCom.CorpID}, {configport.RuntimeWeComAgentID, cfg.WeCom.AgentID}, {configport.WeComCallbackEnabled, cfg.WeCom.CallbackEnabled}, {configport.WeComCustomerSyncEnabled, cfg.WeCom.CustomerSyncEnabled},
		{configport.MessageArchiveEnabled, cfg.WeCom.MessageArchiveEnabled}, {configport.MessageArchivePageLimit, cfg.WeCom.MessageArchivePageLimit}, {configport.MessageArchivePageBudget, cfg.WeCom.MessageArchivePageBudget}, {configport.SidebarContextTokenTTLSeconds, int(cfg.WeCom.ContextTokenTTL / time.Second)},
		{configport.GroupOpsDirectoryReadEnabled, cfg.GroupOps.ProviderReadEnabled}, {configport.GroupOpsDispatchEnabled, cfg.GroupOps.ProviderEnabled},
		{configport.EffectsProviderEnabled, cfg.Effects.ProviderEnabled}, {configport.SurveyCompletionProviderEnabled, cfg.Survey.CompletionProviderEnabled}, {configport.CommercePushProviderEnabled, cfg.CommercePush.ProviderEnabled}, {configport.WorkerLimit, cfg.WorkerLimit},
		{configport.WeChatPayProviderEnabled, cfg.WeChatPay.Enabled}, {configport.WeChatPayAppID, cfg.WeChatPay.AppID}, {configport.WeChatPayAppScope, cfg.WeChatPay.AppScope}, {configport.WeChatPayH5OAuthEnabled, cfg.WeChatPay.H5OAuthEnabled}, {configport.WeChatPayH5AppID, cfg.WeChatPay.H5AppID}, {configport.WeChatPayH5AppScope, cfg.WeChatPay.H5AppScope}, {configport.WeChatPayMerchantID, cfg.WeChatPay.MerchantID}, {configport.WeChatPayMerchantSerial, cfg.WeChatPay.MerchantSerial},
		{configport.WeChatShopProviderEnabled, cfg.WeChatShop.Enabled}, {configport.WeChatShopAppID, cfg.WeChatShop.AppID},
		{configport.AlipayProviderEnabled, cfg.Alipay.Enabled}, {configport.AlipayAppID, cfg.Alipay.AppID}, {configport.AlipayProduction, cfg.Alipay.Production},
		{configport.SurveyOAuthEnabled, cfg.Survey.OAuthEnabled}, {configport.SurveyOAuthAppID, cfg.Survey.OAuthAppID}, {configport.SurveyOAuthOpenPlatformID, cfg.Survey.OAuthOpenPlatformID}, {configport.SurveyOAuthScope, cfg.Survey.OAuthScope},
	}
	settings := make([]configport.RuntimeSetting, 0, len(values))
	for _, item := range values {
		raw, err := json.Marshal(item.value)
		if err != nil {
			return nil, err
		}
		settings = append(settings, configport.RuntimeSetting{Key: item.key, Value: raw})
	}
	return settings, nil
}

func applyRuntimeConfig(cfg platformconfig.Runtime, snapshot configport.EffectiveSnapshot) (platformconfig.Runtime, error) {
	defaults, err := runtimeConfigDefaults(cfg)
	if err != nil {
		return cfg, err
	}
	baseline := make(map[configport.RuntimeSettingKey]json.RawMessage, len(defaults))
	for _, setting := range defaults {
		baseline[setting.Key] = setting.Value
	}
	for _, setting := range snapshot.Settings {
		baselineValue, found := baseline[setting.Key]
		if !found {
			return cfg, fmt.Errorf("runtime config %s is outside the closed catalog", setting.Key)
		}
		switch setting.Key {
		case configport.AIAssistantIntakeEnabled, configport.WeChatPayAppScope, configport.WeChatPayH5AppScope, configport.SurveyOAuthScope:
			// The old signed AI intake and payment/OAuth scopes stay deployment-
			// controlled. Automation mode and AI dispatch intentionally do not
			// appear here: a validated Config release may change those switches.
			if !bytes.Equal(setting.Value, baselineValue) {
				return cfg, fmt.Errorf("runtime config %s is deployment-controlled", setting.Key)
			}
		case configport.RuntimeWeComCorpID, configport.SurveyOAuthAppID, configport.SurveyOAuthOpenPlatformID, configport.WeChatPayAppID, configport.WeChatPayH5AppID, configport.WeChatPayMerchantID, configport.WeChatShopAppID, configport.AlipayAppID:
			var bound string
			if err := json.Unmarshal(baselineValue, &bound); err != nil {
				return cfg, fmt.Errorf("runtime config %s has invalid deployment baseline: %w", setting.Key, err)
			}
			if bound != "" && !bytes.Equal(setting.Value, baselineValue) {
				return cfg, fmt.Errorf("runtime config %s requires an explicit identity or integration migration", setting.Key)
			}
		}
	}
	values := make(map[configport.RuntimeSettingKey]json.RawMessage, len(snapshot.Settings))
	for _, setting := range snapshot.Settings {
		values[setting.Key] = setting.Value
	}
	boolValue := func(key configport.RuntimeSettingKey, target *bool) error {
		if raw, ok := values[key]; ok {
			if err := json.Unmarshal(raw, target); err != nil {
				return fmt.Errorf("runtime config %s: %w", key, err)
			}
		}
		return nil
	}
	intValue := func(key configport.RuntimeSettingKey, target *int) error {
		if raw, ok := values[key]; ok {
			if err := json.Unmarshal(raw, target); err != nil {
				return fmt.Errorf("runtime config %s: %w", key, err)
			}
		}
		return nil
	}
	stringValue := func(key configport.RuntimeSettingKey, target *string) error {
		if raw, ok := values[key]; ok {
			if err := json.Unmarshal(raw, target); err != nil {
				return fmt.Errorf("runtime config %s: %w", key, err)
			}
		}
		return nil
	}
	if err := intValue(configport.AutomationOperationsMaxRecipientsPerRun, &cfg.AutomationOperations.MaxRecipientsPerRun); err != nil {
		return cfg, err
	}
	mode := string(cfg.AutomationOperations.ProviderMode)
	if err := stringValue(configport.AutomationOperationsProviderMode, &mode); err != nil {
		return cfg, err
	}
	cfg.AutomationOperations.ProviderMode = platformconfig.AutomationProviderMode(mode)
	if err := boolValue(configport.AIAssistantUIEnabled, &cfg.AIAssistant.UIEnabled); err != nil {
		return cfg, err
	}
	if err := boolValue(configport.AIAssistantIntakeEnabled, &cfg.AIAssistant.IntakeEnabled); err != nil {
		return cfg, err
	}
	if err := boolValue(configport.AIAssistantDispatchEnabled, &cfg.AIAssistant.DispatchEnabled); err != nil {
		return cfg, err
	}
	if err := boolValue(configport.AIAgentGenerationEnabled, &cfg.AIGeneration.Enabled); err != nil {
		return cfg, err
	}
	if err := boolValue(configport.WeComEnabled, &cfg.WeCom.Enabled); err != nil {
		return cfg, err
	}
	if err := stringValue(configport.RuntimeWeComCorpID, &cfg.WeCom.CorpID); err != nil {
		return cfg, err
	}
	if err := stringValue(configport.RuntimeWeComAgentID, &cfg.WeCom.AgentID); err != nil {
		return cfg, err
	}
	if err := boolValue(configport.WeComCallbackEnabled, &cfg.WeCom.CallbackEnabled); err != nil {
		return cfg, err
	}
	if err := boolValue(configport.WeComCustomerSyncEnabled, &cfg.WeCom.CustomerSyncEnabled); err != nil {
		return cfg, err
	}
	if err := boolValue(configport.MessageArchiveEnabled, &cfg.WeCom.MessageArchiveEnabled); err != nil {
		return cfg, err
	}
	archivePageLimit := int(cfg.WeCom.MessageArchivePageLimit)
	if err := intValue(configport.MessageArchivePageLimit, &archivePageLimit); err != nil {
		return cfg, err
	}
	cfg.WeCom.MessageArchivePageLimit = uint32(archivePageLimit)
	if err := intValue(configport.MessageArchivePageBudget, &cfg.WeCom.MessageArchivePageBudget); err != nil {
		return cfg, err
	}
	var ttlSeconds int
	if err := intValue(configport.SidebarContextTokenTTLSeconds, &ttlSeconds); err != nil {
		return cfg, err
	}
	if ttlSeconds > 0 {
		cfg.WeCom.ContextTokenTTL = time.Duration(ttlSeconds) * time.Second
	}
	if err := boolValue(configport.GroupOpsDirectoryReadEnabled, &cfg.GroupOps.ProviderReadEnabled); err != nil {
		return cfg, err
	}
	if err := boolValue(configport.GroupOpsDispatchEnabled, &cfg.GroupOps.ProviderEnabled); err != nil {
		return cfg, err
	}
	if err := boolValue(configport.EffectsProviderEnabled, &cfg.Effects.ProviderEnabled); err != nil {
		return cfg, err
	}
	if err := boolValue(configport.SurveyCompletionProviderEnabled, &cfg.Survey.CompletionProviderEnabled); err != nil {
		return cfg, err
	}
	if err := boolValue(configport.CommercePushProviderEnabled, &cfg.CommercePush.ProviderEnabled); err != nil {
		return cfg, err
	}
	if err := intValue(configport.WorkerLimit, &cfg.WorkerLimit); err != nil {
		return cfg, err
	}
	if err := boolValue(configport.WeChatPayProviderEnabled, &cfg.WeChatPay.Enabled); err != nil {
		return cfg, err
	}
	if err := stringValue(configport.WeChatPayAppID, &cfg.WeChatPay.AppID); err != nil {
		return cfg, err
	}
	if err := stringValue(configport.WeChatPayAppScope, &cfg.WeChatPay.AppScope); err != nil {
		return cfg, err
	}
	if err := boolValue(configport.WeChatPayH5OAuthEnabled, &cfg.WeChatPay.H5OAuthEnabled); err != nil {
		return cfg, err
	}
	if err := stringValue(configport.WeChatPayH5AppID, &cfg.WeChatPay.H5AppID); err != nil {
		return cfg, err
	}
	if err := stringValue(configport.WeChatPayH5AppScope, &cfg.WeChatPay.H5AppScope); err != nil {
		return cfg, err
	}
	if err := stringValue(configport.WeChatPayMerchantID, &cfg.WeChatPay.MerchantID); err != nil {
		return cfg, err
	}
	if err := stringValue(configport.WeChatPayMerchantSerial, &cfg.WeChatPay.MerchantSerial); err != nil {
		return cfg, err
	}
	if err := boolValue(configport.WeChatShopProviderEnabled, &cfg.WeChatShop.Enabled); err != nil {
		return cfg, err
	}
	if err := stringValue(configport.WeChatShopAppID, &cfg.WeChatShop.AppID); err != nil {
		return cfg, err
	}
	if err := boolValue(configport.AlipayProviderEnabled, &cfg.Alipay.Enabled); err != nil {
		return cfg, err
	}
	if err := stringValue(configport.AlipayAppID, &cfg.Alipay.AppID); err != nil {
		return cfg, err
	}
	if err := boolValue(configport.AlipayProduction, &cfg.Alipay.Production); err != nil {
		return cfg, err
	}
	if err := boolValue(configport.SurveyOAuthEnabled, &cfg.Survey.OAuthEnabled); err != nil {
		return cfg, err
	}
	if err := stringValue(configport.SurveyOAuthAppID, &cfg.Survey.OAuthAppID); err != nil {
		return cfg, err
	}
	if err := stringValue(configport.SurveyOAuthOpenPlatformID, &cfg.Survey.OAuthOpenPlatformID); err != nil {
		return cfg, err
	}
	if err := stringValue(configport.SurveyOAuthScope, &cfg.Survey.OAuthScope); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// validateAppliedRuntimeConfig repeats release validation against the exact
// deployment snapshot after Config has been projected but before adapters are
// constructed. A release validated under an earlier deployment cannot leave a
// misleading application fact if a required protected authorization or
// credential is absent at this startup.
func validateAppliedRuntimeConfig(cfg platformconfig.Runtime) error {
	settings, err := runtimeConfigDefaults(cfg)
	if err != nil {
		return err
	}
	return configapp.ValidateEffectiveRuntimeSettings(settings, runtimeConfigActivationGuards(cfg))
}

// runtimeConfigProtectedReferencePresence is deliberately a closed map. It
// reports only startup presence of named deployment references, never their
// values, and is the sole source for Config Center's secret-reference badges.
func runtimeConfigProtectedReferencePresence(cfg platformconfig.Runtime) map[string]bool {
	return map[string]bool{
		"environment://AICRM_WECOM_SECRET":                       cfg.WeCom.Secret != "",
		"environment://AICRM_WECOM_CONTACT_SECRET":               cfg.WeCom.ContactSecret != "",
		"environment://AICRM_WECOM_CALLBACK_TOKEN":               cfg.WeCom.CallbackToken != "",
		"environment://AICRM_WECOM_CALLBACK_AES_KEY":             cfg.WeCom.CallbackAESKey != "",
		"environment://AICRM_WECOM_MESSAGE_ARCHIVE_SECRET":       cfg.WeCom.MessageArchiveSecret != "",
		"environment://AICRM_AUTOMATION_OPS_WEBHOOK_SECRET":      cfg.AutomationOperations.WebhookSecret != "",
		"environment://AICRM_AUDIENCE_PUSH_WEBHOOK_SECRET":       cfg.AutomationOperations.AudiencePushWebhookSecret != "",
		"environment://AICRM_AUTOMATION_OPS_PROVIDER_PERMISSION": cfg.AutomationOperations.ProviderPermission != "",
		"environment://AICRM_AI_GENERATION_API_KEY":              cfg.AIGeneration.APIKey != "",
		"environment://AICRM_WECHAT_PAY_API_V3_KEY":              cfg.WeChatPay.APIV3Key != "",
		"environment://AICRM_WECHAT_PAY_PRIVATE_KEY_PATH":        cfg.WeChatPay.PrivateKeyPath != "",
		"environment://AICRM_WECHAT_PAY_PLATFORM_CERT_PATH":      cfg.WeChatPay.PlatformCertPath != "",
		"environment://AICRM_WECHAT_SHOP_APP_SECRET":             cfg.WeChatShop.AppSecret != "",
		"environment://AICRM_WECHAT_SHOP_CALLBACK_TOKEN":         cfg.WeChatShop.CallbackToken != "",
		"environment://AICRM_WECHAT_SHOP_CALLBACK_AES_KEY":       cfg.WeChatShop.CallbackEncodingAESKey != "",
		"environment://AICRM_ALIPAY_PRIVATE_KEY_PATH":            cfg.Alipay.PrivateKeyPath != "",
		"environment://AICRM_ALIPAY_PUBLIC_KEY":                  cfg.Alipay.AlipayPublicKey != "",
		"environment://AICRM_ALIPAY_APP_CERT_PATH":               cfg.Alipay.AppCertPath != "",
		"environment://AICRM_ALIPAY_ALIPAY_CERT_PATH":            cfg.Alipay.AlipayCertPath != "",
		"environment://AICRM_ALIPAY_ROOT_CERT_PATH":              cfg.Alipay.AlipayRootPath != "",
		"environment://AICRM_SURVEY_OAUTH_SECRET":                cfg.Survey.OAuthSecret != "",
	}
}

// runtimeConfigActivationGuards make a release fail validation when its
// requested capability lacks the already-deployed authorization or protected
// credential contract. They contain no secret material and cannot grant an
// authorization by themselves.
func runtimeConfigActivationGuards(cfg platformconfig.Runtime) configapp.RuntimeActivationGuards {
	return configapp.RuntimeActivationGuards{
		WeComEnabled:              cfg.WeCom.Secret != "" && cfg.WeCom.ContextSigningKey != "",
		MessageArchiveEnabled:     cfg.WeCom.MessageArchiveSecret != "" && cfg.WeCom.MessageArchiveRunnerPath != "" && cfg.WeCom.MessageArchiveLibraryPath != "" && len(cfg.WeCom.MessageArchivePrivateKeyPaths) > 0,
		AutomationProviderEnabled: cfg.AutomationOperations.ProviderPermission == "fixed-script-send-authorized" && cfg.WeCom.ContactSecret != "",
		AIDispatchEnabled:         cfg.AIAssistant.ProviderPermission == "private-message-authorized" && cfg.WeCom.ContactSecret != "",
		AIAssistantIntakeEnabled:  cfg.AIAssistant.IntegrationKey != "" && cfg.AIAssistant.IntegrationSecret != "" && cfg.AIAssistant.IntegrationActorID > 0,
		AIAgentGenerationEnabled:  cfg.Effects.ProviderEnabled && cfg.AIGeneration.Ready(),
		WeChatPayEnabled:          cfg.WeChatPay.AppSecret != "" && cfg.WeChatPay.PrivateKeyPath != "" && cfg.WeChatPay.PlatformCertPath != "" && cfg.WeChatPay.APIV3Key != "",
		WeChatPayH5OAuthEnabled:   cfg.WeChatPay.H5AppSecret != "" && cfg.WeChatPay.OrderContactDataKey != "",
		WeChatShopEnabled:         cfg.WeChatShop.AppSecret != "" && cfg.WeChatShop.CallbackToken != "" && cfg.WeChatShop.CallbackEncodingAESKey != "",
		AlipayEnabled:             cfg.Alipay.PrivateKeyPath != "" && (cfg.Alipay.AlipayPublicKey != "" || (cfg.Alipay.AppCertPath != "" && cfg.Alipay.AlipayCertPath != "" && cfg.Alipay.AlipayRootPath != "")),
		SurveyOAuthEnabled:        cfg.Survey.OAuthSecret != "",
	}
}
