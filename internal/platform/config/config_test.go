package config

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func TestLoadDefaultsAndRejectsInvalidRole(t *testing.T) {
	t.Setenv("AICRM_DATABASE_URL", "postgres://aicrm:test@localhost/aicrm")
	t.Setenv("AICRM_LISTEN_ADDR", "")
	t.Setenv("AICRM_RELEASE_SHA", "")
	t.Setenv("AICRM_ROLE", "")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ListenAddress != "127.0.0.1:8080" || cfg.ReleaseSHA != "development" || cfg.Role != RoleAPI ||
		cfg.DatabaseURL == "" || cfg.PublicOrigin != "https://id-dev.youcangogogo.com" || cfg.WorkerLimit != 25 {
		t.Fatalf("defaults=%+v", cfg)
	}

	t.Setenv("AICRM_ROLE", "unknown")
	if _, err = Load(); err == nil {
		t.Fatal("expected invalid role error")
	}
}

func TestNormalizeRuntimePolicyDefaultsOnlyFillsOmittedValues(t *testing.T) {
	defaults := NormalizeRuntimePolicyDefaults(Runtime{})
	if defaults.WorkerLimit != DefaultWorkerLimit ||
		defaults.WeCom.MessageArchivePageLimit != DefaultMessageArchivePageLimit ||
		defaults.WeCom.MessageArchivePageBudget != DefaultMessageArchivePageBudget ||
		defaults.WeCom.ContextTokenTTL != DefaultContextTokenTTL ||
		defaults.AutomationOperations.ProviderMode != AutomationProviderDisabled ||
		defaults.AutomationOperations.MaxRecipientsPerRun != DefaultAutomationMaxRecipientsPerRun {
		t.Fatalf("defaults=%+v", defaults)
	}

	explicit := Runtime{
		WorkerLimit: -1,
		WeCom: WeCom{
			MessageArchivePageLimit:  9,
			MessageArchivePageBudget: -1,
			ContextTokenTTL:          time.Second,
		},
		AutomationOperations: AutomationOperations{
			ProviderMode:        AutomationProviderLimited,
			MaxRecipientsPerRun: -1,
		},
	}
	got := NormalizeRuntimePolicyDefaults(explicit)
	if got.WorkerLimit != -1 || got.WeCom.MessageArchivePageLimit != 9 || got.WeCom.MessageArchivePageBudget != -1 || got.WeCom.ContextTokenTTL != time.Second || got.AutomationOperations.ProviderMode != AutomationProviderLimited || got.AutomationOperations.MaxRecipientsPerRun != -1 {
		t.Fatalf("normalization changed explicit values: %+v", got)
	}
}

func TestSourceDatabaseURLIsExplicitAndTrimmed(t *testing.T) {
	t.Setenv("AICRM_SOURCE_DATABASE_URL", "postgres:///legacy")
	if value, err := SourceDatabaseURL(); err != nil || value != "postgres:///legacy" {
		t.Fatalf("source database URL=%q err=%v", value, err)
	}
	t.Setenv("AICRM_SOURCE_DATABASE_URL", " postgres:///legacy")
	if _, err := SourceDatabaseURL(); err == nil {
		t.Fatal("accepted source database URL with surrounding whitespace")
	}
}

func TestSurveyOAuthAndPhoneVaultConfigurationFailClosed(t *testing.T) {
	t.Setenv("AICRM_DATABASE_URL", "postgres://aicrm:test@localhost/aicrm")
	t.Setenv("AICRM_IDENTITY_PHONE_DATA_KEY", base64.RawStdEncoding.EncodeToString(make([]byte, 32)))
	t.Setenv("AICRM_SURVEY_OAUTH_ENABLED", "true")
	t.Setenv("AICRM_SURVEY_OAUTH_APP_ID", "wx-app")
	t.Setenv("AICRM_SURVEY_OAUTH_SECRET", "secret")
	t.Setenv("AICRM_SURVEY_OAUTH_OPEN_PLATFORM_ID", "platform")
	t.Setenv("AICRM_SURVEY_OAUTH_SCOPE", "snsapi_base")
	if _, err := Load(); err == nil {
		t.Fatal("non-interactive OAuth scope accepted")
	}
	t.Setenv("AICRM_SURVEY_OAUTH_SCOPE", "snsapi_userinfo")
	cfg, err := Load()
	if err != nil || !cfg.Survey.OAuthEnabled || cfg.Survey.OAuthScope != "snsapi_userinfo" {
		t.Fatalf("config=%+v err=%v", cfg.Survey, err)
	}
	t.Setenv("AICRM_SURVEY_OAUTH_OPEN_PLATFORM_ID", " platform")
	if _, err = Load(); err == nil {
		t.Fatal("unsafe OAuth scope identifier accepted")
	}
	t.Setenv("AICRM_SURVEY_OAUTH_OPEN_PLATFORM_ID", "platform")
	t.Setenv("AICRM_IDENTITY_PHONE_DATA_KEY", "not-base64")
	if _, err = Load(); err == nil {
		t.Fatal("invalid phone key accepted")
	}
}

func TestIdentityPhoneDataKeyLoadsOnlyValidDedicatedKey(t *testing.T) {
	valid := base64.RawStdEncoding.EncodeToString(make([]byte, 32))
	t.Setenv("AICRM_IDENTITY_PHONE_DATA_KEY", valid)
	if got, err := IdentityPhoneDataKey(); err != nil || got != valid {
		t.Fatalf("key=%q err=%v", got, err)
	}
	t.Setenv("AICRM_IDENTITY_PHONE_DATA_KEY", "invalid")
	if _, err := IdentityPhoneDataKey(); err == nil {
		t.Fatal("invalid phone key accepted")
	}
}

func TestHXCIdentityWritesRequireExplicitVaultAndSync(t *testing.T) {
	t.Setenv("AICRM_DATABASE_URL", "postgres://aicrm:test@localhost/aicrm")
	t.Setenv("AICRM_HXC_IDENTITY_WRITE_ENABLED", "true")
	if _, err := Load(); err == nil {
		t.Fatal("HXC identity writes accepted without HXC sync")
	}
	for key, value := range map[string]string{
		"AICRM_HXC_SYNC_ENABLED": "true", "AICRM_HXC_SOURCE_DSN": "reader:secret@tcp(mysql:3306)/hxc",
		"AICRM_HXC_UNIONID_SCOPE": "wechat-open-platform:hxc", "AICRM_HXC_SUBJECT_HMAC_KEY": strings.Repeat("h", 32),
		"AICRM_IDENTITY_OBSERVATION_VAULT_KEY": base64.StdEncoding.EncodeToString(make([]byte, 32)),
		"AICRM_HXC_UNIONID_VERIFIED":           "true",
	} {
		t.Setenv(key, value)
	}
	cfg, err := Load()
	if err != nil || !cfg.HXCDashboard.IdentityWriteEnabled || !cfg.HXCDashboard.UnionIDVerified {
		t.Fatalf("HXC identity config=%+v err=%v", cfg.HXCDashboard, err)
	}
	t.Setenv("AICRM_IDENTITY_OBSERVATION_VAULT_KEY", "invalid")
	if _, err = Load(); err == nil {
		t.Fatal("invalid HXC observation vault key accepted")
	}
}

func TestLoadValidatesBootstrapAndWeComAsClosedConfigurationGroups(t *testing.T) {
	t.Setenv("AICRM_DATABASE_URL", "postgres://aicrm:test@localhost/aicrm")
	t.Setenv("AICRM_BOOTSTRAP_USERNAME", "admin")
	if _, err := Load(); err == nil {
		t.Fatal("expected partial bootstrap configuration error")
	}

	t.Setenv("AICRM_BOOTSTRAP_PASSWORD", "this-is-a-strong-password")
	t.Setenv("AICRM_BOOTSTRAP_DISPLAY_NAME", "CRM Admin")
	t.Setenv("AICRM_WECOM_ENABLED", "true")
	if _, err := Load(); err == nil {
		t.Fatal("expected partial WeCom configuration error")
	}

	t.Setenv("AICRM_WECOM_CORP_ID", "ww-corp")
	t.Setenv("AICRM_WECOM_AGENT_ID", "1000002")
	t.Setenv("AICRM_WECOM_SECRET", "provider-secret")
	t.Setenv("AICRM_WECOM_CONTEXT_SIGNING_KEY", "0123456789abcdef0123456789abcdef")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Bootstrap.Enabled || !cfg.WeCom.Enabled {
		t.Fatalf("config=%+v", cfg)
	}
}

func TestLoadValidatesCallbackConfigurationIndependently(t *testing.T) {
	t.Setenv("AICRM_DATABASE_URL", "postgres://aicrm:test@localhost/aicrm")
	t.Setenv("AICRM_WECOM_CALLBACK_ENABLED", "true")
	if _, err := Load(); err == nil {
		t.Fatal("expected partial callback configuration error")
	}

	t.Setenv("AICRM_WECOM_CORP_ID", "ww-corp")
	t.Setenv("AICRM_WECOM_CALLBACK_TOKEN", "callback-token")
	t.Setenv("AICRM_WECOM_CALLBACK_AES_KEY", strings.Repeat("a", 43))
	t.Setenv("AICRM_CHANNEL_STATE_HMAC_KEY", strings.Repeat("b", 32))
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.WeCom.CallbackEnabled || cfg.WeCom.Enabled {
		t.Fatalf("config=%+v", cfg.WeCom)
	}
}

func TestLoadRejectsInsecureOriginAndLooseBoolean(t *testing.T) {
	t.Setenv("AICRM_DATABASE_URL", "postgres://aicrm:test@localhost/aicrm")
	t.Setenv("AICRM_PUBLIC_ORIGIN", "http://id-dev.youcangogogo.com")
	if _, err := Load(); err == nil {
		t.Fatal("expected insecure origin error")
	}
	t.Setenv("AICRM_PUBLIC_ORIGIN", "https://id-dev.youcangogogo.com")
	t.Setenv("AICRM_WECOM_ENABLED", "1")
	if _, err := Load(); err == nil {
		t.Fatal("expected strict boolean error")
	}
	t.Setenv("AICRM_WECOM_ENABLED", "false")
	t.Setenv("AICRM_WECOM_CALLBACK_ENABLED", "1")
	if _, err := Load(); err == nil {
		t.Fatal("expected callback strict boolean error")
	}
	t.Setenv("AICRM_WECOM_CALLBACK_ENABLED", "false")
	t.Setenv("AICRM_OUTBOUND_PROVIDER_ENABLED", "1")
	if _, err := Load(); err == nil {
		t.Fatal("expected outbound provider loose boolean error")
	}
}

func TestLoadAcceptsEffectsWorkerRole(t *testing.T) {
	t.Setenv("AICRM_DATABASE_URL", "postgres://aicrm:test@localhost/aicrm")
	t.Setenv("AICRM_ROLE", "effects-worker")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Role != RoleEffectsWorker || cfg.Effects.ProviderEnabled {
		t.Fatalf("config=%+v", cfg)
	}
}

func TestLoadPaymentReconcileRoleRequiresAnExplicitPositivePaymentID(t *testing.T) {
	t.Setenv("AICRM_DATABASE_URL", "postgres://aicrm:test@localhost/aicrm")
	t.Setenv("AICRM_ROLE", string(RolePaymentReconcile))
	if _, err := Load(); err == nil {
		t.Fatal("payment reconciliation role accepted without an explicit payment ID")
	}
	for _, invalid := range []string{"0", "-1", " 930", "930 ", "not-a-number"} {
		t.Setenv("AICRM_PAYMENT_RECONCILE_ID", invalid)
		if _, err := Load(); err == nil {
			t.Fatalf("payment reconciliation role accepted invalid ID %q", invalid)
		}
	}
	t.Setenv("AICRM_PAYMENT_RECONCILE_ID", "930")
	cfg, err := Load()
	if err != nil || cfg.Role != RolePaymentReconcile || cfg.PaymentReconcileID != 930 {
		t.Fatalf("payment reconciliation config=%+v err=%v", cfg, err)
	}
}

func TestChannelProviderCapabilitiesAreIndependentAndFailClosed(t *testing.T) {
	t.Setenv("AICRM_DATABASE_URL", "postgres://aicrm:test@localhost/aicrm")
	t.Setenv("AICRM_CHANNEL_PROVIDER_READ_ENABLED", "true")
	if _, err := Load(); err == nil {
		t.Fatal("channel read accepted without shared provider prerequisites")
	}
	t.Setenv("AICRM_OUTBOUND_PROVIDER_ENABLED", "true")
	t.Setenv("AICRM_WECOM_ENABLED", "true")
	t.Setenv("AICRM_WECOM_CORP_ID", "ww-corp")
	t.Setenv("AICRM_WECOM_AGENT_ID", "1000002")
	t.Setenv("AICRM_WECOM_SECRET", "provider-secret")
	t.Setenv("AICRM_WECOM_CONTEXT_SIGNING_KEY", strings.Repeat("k", 32))
	t.Setenv("AICRM_WECOM_CONTACT_SECRET", "contact-secret")
	t.Setenv("AICRM_WECOM_CONTEXT_SIGNING_KEY", strings.Repeat("k", 32))
	cfg, err := Load()
	if err != nil || !cfg.WeCom.ChannelProviderReadEnabled || cfg.WeCom.ChannelQRProviderEnabled || cfg.GroupOps.ProviderEnabled || cfg.WeCom.StaffDirectoryRefreshInterval != 15*time.Minute {
		t.Fatalf("config=%+v err=%v", cfg, err)
	}
	t.Setenv("AICRM_CHANNEL_STAFF_REFRESH_INTERVAL", "4m")
	if _, err = Load(); err == nil {
		t.Fatal("accepted unsafe staff refresh interval")
	}
	t.Setenv("AICRM_CHANNEL_STAFF_REFRESH_INTERVAL", "30m")
	cfg, err = Load()
	if err != nil || cfg.WeCom.StaffDirectoryRefreshInterval != 30*time.Minute {
		t.Fatalf("staff refresh interval=%s err=%v", cfg.WeCom.StaffDirectoryRefreshInterval, err)
	}
	t.Setenv("AICRM_CHANNEL_QR_PROVIDER_ENABLED", "true")
	if _, err = Load(); err == nil {
		t.Fatal("channel QR accepted without callback/state scope")
	}
	t.Setenv("AICRM_WECOM_CALLBACK_ENABLED", "true")
	t.Setenv("AICRM_WECOM_CALLBACK_TOKEN", "callback-token")
	t.Setenv("AICRM_WECOM_CALLBACK_AES_KEY", strings.Repeat("a", 43))
	t.Setenv("AICRM_CHANNEL_STATE_HMAC_KEY", strings.Repeat("h", 32))
	cfg, err = Load()
	if err != nil || !cfg.WeCom.ChannelQRProviderEnabled || cfg.WeCom.ChannelWelcomeProviderEnabled || cfg.WeCom.ChannelTagProviderEnabled || cfg.GroupOps.ProviderEnabled {
		t.Fatalf("config=%+v err=%v", cfg, err)
	}
}

func TestWeChatShopProviderIsIndependentAndClosedConfiguration(t *testing.T) {
	t.Setenv("AICRM_DATABASE_URL", "postgres://aicrm:test@localhost/aicrm")
	t.Setenv("AICRM_WECHAT_SHOP_PROVIDER_ENABLED", "true")
	if _, err := Load(); err == nil {
		t.Fatal("expected partial WeChat Shop configuration error")
	}
	t.Setenv("AICRM_WECHAT_SHOP_APP_ID", "shop-app")
	t.Setenv("AICRM_WECHAT_SHOP_APP_SECRET", "shop-secret")
	t.Setenv("AICRM_WECHAT_SHOP_CALLBACK_TOKEN", "callback-token")
	t.Setenv("AICRM_WECHAT_SHOP_CALLBACK_AES_KEY", strings.Repeat("a", 43))
	cfg, err := Load()
	if err != nil || !cfg.WeChatShop.Enabled || cfg.WeChatPay.Enabled {
		t.Fatalf("config=%+v err=%v", cfg.WeChatShop, err)
	}
}

func TestWeChatPayProviderRequiresMiniProgramIdentityCredential(t *testing.T) {
	t.Setenv("AICRM_DATABASE_URL", "postgres://aicrm:test@localhost/aicrm")
	t.Setenv("AICRM_WECHAT_PAY_PROVIDER_ENABLED", "true")
	t.Setenv("AICRM_WECHAT_PAY_APP_ID", "wx-app")
	t.Setenv("AICRM_WECHAT_PAY_APP_SCOPE", "wechat-app:wx-app")
	t.Setenv("AICRM_WECHAT_PAY_MERCHANT_ID", "merchant")
	t.Setenv("AICRM_WECHAT_PAY_MERCHANT_SERIAL", "serial")
	t.Setenv("AICRM_WECHAT_PAY_PRIVATE_KEY_PATH", "/keys/merchant.pem")
	t.Setenv("AICRM_WECHAT_PAY_PLATFORM_CERT_PATH", "/keys/platform.pem")
	t.Setenv("AICRM_WECHAT_PAY_API_V3_KEY", strings.Repeat("k", 32))
	if _, err := Load(); err == nil {
		t.Fatal("expected missing mini program AppSecret to fail closed")
	}
	t.Setenv("AICRM_WECHAT_PAY_APP_SECRET", "app-secret")
	cfg, err := Load()
	if err != nil || !cfg.WeChatPay.Enabled || cfg.WeChatPay.AppSecret != "app-secret" {
		t.Fatalf("config=%+v err=%v", cfg.WeChatPay, err)
	}
}

func TestWeChatPayProfitSharingIsIndependentlyDisabledAndFailsClosed(t *testing.T) {
	t.Setenv("AICRM_DATABASE_URL", "postgres://aicrm:test@localhost/aicrm")
	for key, value := range map[string]string{
		"AICRM_WECHAT_PAY_PROVIDER_ENABLED": "true", "AICRM_WECHAT_PAY_APP_ID": "wx-app", "AICRM_WECHAT_PAY_APP_SECRET": "app-secret", "AICRM_WECHAT_PAY_APP_SCOPE": "wechat-app:wx-app",
		"AICRM_WECHAT_PAY_MERCHANT_ID": "merchant", "AICRM_WECHAT_PAY_MERCHANT_SERIAL": "serial", "AICRM_WECHAT_PAY_PRIVATE_KEY_PATH": "/keys/merchant.pem", "AICRM_WECHAT_PAY_PLATFORM_CERT_PATH": "/keys/platform.pem", "AICRM_WECHAT_PAY_API_V3_KEY": strings.Repeat("k", 32),
	} {
		t.Setenv(key, value)
	}
	cfg, err := Load()
	if err != nil || !cfg.WeChatPay.Enabled || cfg.WeChatPay.ProfitSharingEnabled {
		t.Fatalf("ordinary payment must not enable profit sharing: cfg=%+v err=%v", cfg.WeChatPay, err)
	}
	t.Setenv("AICRM_WECHAT_PAY_PROFIT_SHARING_ENABLED", "true")
	if _, err = Load(); err == nil {
		t.Fatal("profit sharing enabled without External Effects and an explicit authentication mode")
	}
	t.Setenv("AICRM_OUTBOUND_PROVIDER_ENABLED", "true")
	if _, err = Load(); err == nil {
		t.Fatal("profit sharing enabled without explicit authentication mode")
	}
	t.Setenv("AICRM_WECHAT_PAY_PROFIT_SHARING_AUTH_MODE", "certificate")
	cfg, err = Load()
	if err != nil || !cfg.WeChatPay.ProfitSharingEnabled || cfg.WeChatPay.ProfitSharingAuthMode != "certificate" {
		t.Fatalf("certificate profit-sharing configuration=%+v err=%v", cfg.WeChatPay, err)
	}
	t.Setenv("AICRM_WECHAT_PAY_PROFIT_SHARING_AUTH_MODE", "public_key")
	if _, err = Load(); err == nil {
		t.Fatal("public-key profit sharing authentication requires a key id")
	}
	t.Setenv("AICRM_WECHAT_PAY_PROFIT_SHARING_PUBLIC_KEY_ID", "PUB_KEY_ID")
	cfg, err = Load()
	if err != nil || !cfg.WeChatPay.ProfitSharingEnabled || cfg.WeChatPay.ProfitSharingAuthMode != "public_key" || cfg.WeChatPay.ProfitSharingPublicKeyID != "PUB_KEY_ID" {
		t.Fatalf("independent profit-sharing configuration=%+v err=%v", cfg.WeChatPay, err)
	}
	t.Setenv("AICRM_WECHAT_PAY_PROFIT_SHARING_AUTH_MODE", "certificate")
	if _, err = Load(); err == nil {
		t.Fatal("certificate profit sharing authentication must reject a public key id")
	}
}

func TestWeChatPayH5OAuthIsIndependentAndFailClosed(t *testing.T) {
	t.Setenv("AICRM_DATABASE_URL", "postgres://aicrm:test@localhost/aicrm")
	t.Setenv("AICRM_WECHAT_PAY_H5_OAUTH_ENABLED", "true")
	if _, err := Load(); err == nil {
		t.Fatal("H5 OAuth enabled without payment provider")
	}
	for key, value := range map[string]string{
		"AICRM_WECHAT_PAY_PROVIDER_ENABLED": "true", "AICRM_WECHAT_PAY_APP_ID": "wx-mini", "AICRM_WECHAT_PAY_APP_SECRET": "mini-secret", "AICRM_WECHAT_PAY_APP_SCOPE": "wechat-app:wx-mini",
		"AICRM_WECHAT_PAY_MERCHANT_ID": "merchant", "AICRM_WECHAT_PAY_MERCHANT_SERIAL": "serial", "AICRM_WECHAT_PAY_PRIVATE_KEY_PATH": "/keys/merchant.pem", "AICRM_WECHAT_PAY_PLATFORM_CERT_PATH": "/keys/platform.pem", "AICRM_WECHAT_PAY_API_V3_KEY": strings.Repeat("k", 32),
		"AICRM_WECHAT_PAY_H5_APP_ID": "wx-oa", "AICRM_WECHAT_PAY_H5_APP_SECRET": "oa-secret", "AICRM_WECHAT_PAY_H5_APP_SCOPE": "wechat-app:wx-oa", "AICRM_ORDER_CONTACT_DATA_KEY": base64.RawStdEncoding.EncodeToString(make([]byte, 32)),
	} {
		t.Setenv(key, value)
	}
	cfg, err := Load()
	if err != nil || !cfg.WeChatPay.H5OAuthEnabled || cfg.WeChatPay.H5AppID != "wx-oa" {
		t.Fatalf("config=%+v err=%v", cfg.WeChatPay, err)
	}
	t.Setenv("AICRM_WECHAT_PAY_H5_APP_SCOPE", "wechat-app:attacker")
	if _, err = Load(); err == nil {
		t.Fatal("accepted mismatched H5 AppScope")
	}
}

func TestTagCatalogProviderRequiresNarrowExplicitPermission(t *testing.T) {
	t.Setenv("AICRM_DATABASE_URL", "postgres://aicrm:test@localhost/aicrm")
	t.Setenv("AICRM_WECOM_TAG_CATALOG_PROVIDER_ENABLED", "true")
	if _, err := Load(); err == nil {
		t.Fatal("expected disabled WeCom rejection")
	}
	t.Setenv("AICRM_WECOM_ENABLED", "true")
	t.Setenv("AICRM_WECOM_CORP_ID", "ww-corp")
	t.Setenv("AICRM_WECOM_AGENT_ID", "1000002")
	t.Setenv("AICRM_WECOM_SECRET", "provider-secret")
	t.Setenv("AICRM_WECOM_CONTEXT_SIGNING_KEY", "0123456789abcdef0123456789abcdef")
	if _, err := Load(); err == nil {
		t.Fatal("expected permission rejection")
	}
	t.Setenv("AICRM_WECOM_TAG_CATALOG_PROVIDER_PERMISSION", "catalog-read-authorized")
	cfg, err := Load()
	if err != nil || !cfg.TagCatalog.Enabled || cfg.Effects.ProviderEnabled {
		t.Fatalf("config=%+v err=%v", cfg.TagCatalog, err)
	}
	t.Setenv("AICRM_WECOM_TAG_CATALOG_MUTATION_PROVIDER_ENABLED", "true")
	if _, err = Load(); err == nil {
		t.Fatal("expected mutation without External Effects and write grant to fail")
	}
	t.Setenv("AICRM_OUTBOUND_PROVIDER_ENABLED", "true")
	t.Setenv("AICRM_WECOM_CONTACT_SECRET", "contact-secret")
	t.Setenv("AICRM_WECOM_TAG_CATALOG_MUTATION_PROVIDER_PERMISSION", "catalog-write-authorized")
	cfg, err = Load()
	if err != nil || !cfg.TagCatalog.MutationEnabled || cfg.TagCatalog.MutationPermission != "catalog-write-authorized" {
		t.Fatalf("mutation config=%+v err=%v", cfg.TagCatalog, err)
	}
	t.Setenv("AICRM_WECOM_TAG_CATALOG_MUTATION_PROVIDER_PERMISSION", "catalog-read-authorized")
	if _, err = Load(); err == nil {
		t.Fatal("accepted read permission for catalog mutation")
	}
}

func TestAutomationOperationsProviderIsIndependentAndFailClosed(t *testing.T) {
	t.Setenv("AICRM_DATABASE_URL", "postgres://aicrm:test@localhost/aicrm")
	t.Setenv("AICRM_AUTOMATION_OPS_PROVIDER_MODE", "probe")
	if _, err := Load(); err == nil {
		t.Fatal("expected probe without provider prerequisites to fail")
	}
	t.Setenv("AICRM_OUTBOUND_PROVIDER_ENABLED", "true")
	t.Setenv("AICRM_WECOM_ENABLED", "true")
	t.Setenv("AICRM_WECOM_CORP_ID", "ww-corp")
	t.Setenv("AICRM_WECOM_AGENT_ID", "1000002")
	t.Setenv("AICRM_WECOM_SECRET", "provider-secret")
	t.Setenv("AICRM_WECOM_CONTEXT_SIGNING_KEY", "0123456789abcdef0123456789abcdef")
	t.Setenv("AICRM_AUTOMATION_OPS_PROVIDER_PERMISSION", "fixed-script-send-authorized")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.AutomationOperations.ProviderEnabled() || cfg.AutomationOperations.MaxRecipientsPerRun != 1 {
		t.Fatalf("automation operations config=%+v", cfg.AutomationOperations)
	}
	t.Setenv("AICRM_AUTOMATION_OPS_MAX_RECIPIENTS", "2")
	if _, err = Load(); err == nil {
		t.Fatal("probe must remain limited to one recipient")
	}
	t.Setenv("AICRM_AUTOMATION_OPS_PROVIDER_MODE", "limited")
	cfg, err = Load()
	if err != nil || cfg.AutomationOperations.MaxRecipientsPerRun != 2 {
		t.Fatalf("limited config=%+v err=%v", cfg.AutomationOperations, err)
	}
	t.Setenv("AICRM_AUTOMATION_OPS_PROVIDER_MODE", "enabled")
	if _, err = Load(); err == nil {
		t.Fatal("unknown provider mode must fail closed")
	}
}

func TestAIAssistantIntakeAndDispatchFailClosed(t *testing.T) {
	t.Setenv("AICRM_DATABASE_URL", "postgres://aicrm:test@localhost/aicrm")
	t.Setenv("AICRM_AI_ASSISTANT_INTAKE_ENABLED", "true")
	if _, err := Load(); err == nil {
		t.Fatal("expected incomplete integration configuration rejection")
	}
	t.Setenv("AICRM_AI_ASSISTANT_INTEGRATION_KEY", "automation")
	t.Setenv("AICRM_AI_ASSISTANT_INTEGRATION_SECRET", strings.Repeat("s", 32))
	t.Setenv("AICRM_AI_ASSISTANT_INTEGRATION_ACTOR_ID", "7")
	cfg, err := Load()
	if err != nil || !cfg.AIAssistant.IntakeEnabled || cfg.AIAssistant.DispatchEnabled {
		t.Fatalf("config=%+v err=%v", cfg.AIAssistant, err)
	}

	t.Setenv("AICRM_AI_ASSISTANT_DISPATCH_ENABLED", "true")
	if _, err = Load(); err == nil {
		t.Fatal("expected dispatch without provider permission to fail closed")
	}
	t.Setenv("AICRM_OUTBOUND_PROVIDER_ENABLED", "true")
	t.Setenv("AICRM_WECOM_ENABLED", "true")
	t.Setenv("AICRM_WECOM_CORP_ID", "ww-corp")
	t.Setenv("AICRM_WECOM_AGENT_ID", "1000002")
	t.Setenv("AICRM_WECOM_SECRET", "provider-secret")
	t.Setenv("AICRM_WECOM_CONTEXT_SIGNING_KEY", strings.Repeat("k", 32))
	t.Setenv("AICRM_WECOM_CONTACT_SECRET", "contact-secret")
	t.Setenv("AICRM_WECOM_CONTEXT_SIGNING_KEY", strings.Repeat("k", 32))
	t.Setenv("AICRM_AI_ASSISTANT_PROVIDER_PERMISSION", "private-message-authorized")
	cfg, err = Load()
	if err != nil || !cfg.AIAssistant.DispatchEnabled || !cfg.Effects.ProviderEnabled || !cfg.WeCom.Enabled {
		t.Fatalf("config=%+v err=%v", cfg, err)
	}
}

func TestAIGenerationProviderConfigurationFailsClosed(t *testing.T) {
	t.Setenv("AICRM_DATABASE_URL", "postgres://aicrm:test@localhost/aicrm")
	cfg, err := Load()
	if err != nil || cfg.AIGeneration.Enabled || cfg.AIGeneration.Timeout != 30*time.Second {
		t.Fatalf("disabled default=%+v err=%v", cfg.AIGeneration, err)
	}
	t.Setenv("AICRM_AI_GENERATION_ENABLED", "true")
	if _, err = Load(); err == nil {
		t.Fatal("enabled AI generation without protected provider inputs accepted")
	}
	t.Setenv("AICRM_OUTBOUND_PROVIDER_ENABLED", "true")
	t.Setenv("AICRM_AI_GENERATION_API_KEY", "provider-key")
	t.Setenv("AICRM_AI_GENERATION_BASE_URL", "https://models.example/v1")
	t.Setenv("AICRM_AI_GENERATION_MODEL", "provider-neutral-model")
	t.Setenv("AICRM_AI_GENERATION_TIMEOUT_SECONDS", "45")
	cfg, err = Load()
	if err != nil || !cfg.AIGeneration.Enabled || cfg.AIGeneration.Timeout != 45*time.Second || cfg.AIGeneration.Model != "provider-neutral-model" {
		t.Fatalf("enabled config=%+v err=%v", cfg.AIGeneration, err)
	}
	t.Setenv("AICRM_AI_GENERATION_BASE_URL", "http://models.example/v1")
	if _, err = Load(); err == nil {
		t.Fatal("insecure AI generation endpoint accepted")
	}
}

func TestWeComMaterialUploadTimeoutConfiguration(t *testing.T) {
	t.Setenv("AICRM_DATABASE_URL", "postgres://aicrm:test@localhost/aicrm")
	cfg, err := Load()
	if err != nil || cfg.WeCom.MaterialUploadTimeout != 120*time.Second {
		t.Fatalf("default timeout=%s err=%v", cfg.WeCom.MaterialUploadTimeout, err)
	}
	t.Setenv("AICRM_WECOM_MATERIAL_UPLOAD_TIMEOUT_SECONDS", "45")
	cfg, err = Load()
	if err != nil || cfg.WeCom.MaterialUploadTimeout != 45*time.Second {
		t.Fatalf("configured timeout=%s err=%v", cfg.WeCom.MaterialUploadTimeout, err)
	}
	t.Setenv("AICRM_WECOM_MATERIAL_UPLOAD_TIMEOUT_SECONDS", "0")
	if _, err = Load(); err == nil {
		t.Fatal("zero material upload timeout accepted")
	}
}

func TestDatabaseURLPrecedenceAndValidation(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://fallback")
	t.Setenv("AICRM_DATABASE_URL", "postgres://canonical")

	url, err := DatabaseURL()
	if err != nil {
		t.Fatal(err)
	}
	if url != "postgres://canonical" {
		t.Fatalf("DatabaseURL()=%q", url)
	}

	t.Setenv("AICRM_DATABASE_URL", " postgres://invalid")
	if _, err = DatabaseURL(); err == nil {
		t.Fatal("expected surrounding whitespace to be rejected")
	}
}

func TestDatabaseURLRequiresConfiguration(t *testing.T) {
	t.Setenv("AICRM_DATABASE_URL", "")
	t.Setenv("DATABASE_URL", "")
	if _, err := DatabaseURL(); err == nil {
		t.Fatal("expected missing database URL error")
	}
}

func TestChromiumJourneyRequiredUsesConfigurationBoundary(t *testing.T) {
	t.Setenv("AICRM_REQUIRE_CHROMIUM_JOURNEY", "")
	if ChromiumJourneyRequired() {
		t.Fatal("missing Chromium journey flag is required")
	}
	t.Setenv("AICRM_REQUIRE_CHROMIUM_JOURNEY", "1")
	if !ChromiumJourneyRequired() {
		t.Fatal("explicit Chromium journey flag is not required")
	}
}

func TestProductExternalPushDarwinChromiumDiagnosticUsesConfigurationBoundary(t *testing.T) {
	t.Setenv("AICRM_PRODUCT_PUSH_ALLOW_DARWIN_CHROMIUM", "")
	if ProductExternalPushDarwinChromiumDiagnosticAllowed() {
		t.Fatal("missing Darwin diagnostic flag allowed the journey")
	}
	t.Setenv("AICRM_PRODUCT_PUSH_ALLOW_DARWIN_CHROMIUM", "1")
	if !ProductExternalPushDarwinChromiumDiagnosticAllowed() {
		t.Fatal("explicit Darwin diagnostic flag did not allow the journey")
	}
}

func TestAdminLayoutScreenshotDirectoryUsesConfigurationBoundary(t *testing.T) {
	t.Setenv("AICRM_ADMIN_LAYOUT_SCREENSHOT_DIR", "")
	if value := AdminLayoutScreenshotDirectory(); value != "" {
		t.Fatalf("missing screenshot directory=%q", value)
	}
	t.Setenv("AICRM_ADMIN_LAYOUT_SCREENSHOT_DIR", "/tmp/aicrm-layout-evidence")
	if value := AdminLayoutScreenshotDirectory(); value != "/tmp/aicrm-layout-evidence" {
		t.Fatalf("screenshot directory=%q", value)
	}
}

func TestChannelCenterScreenshotDirectoryUsesConfigurationBoundary(t *testing.T) {
	t.Setenv("AICRM_CHANNEL_CENTER_SCREENSHOT_DIR", "")
	if value := ChannelCenterScreenshotDirectory(); value != "" {
		t.Fatalf("missing screenshot directory=%q", value)
	}
	t.Setenv("AICRM_CHANNEL_CENTER_SCREENSHOT_DIR", "/tmp/aicrm-channel-center-evidence")
	if value := ChannelCenterScreenshotDirectory(); value != "/tmp/aicrm-channel-center-evidence" {
		t.Fatalf("screenshot directory=%q", value)
	}
}

func TestAccessGovernanceScreenshotDirectoryUsesConfigurationBoundary(t *testing.T) {
	t.Setenv("AICRM_ACCESS_UI_SCREENSHOT_DIR", "")
	if value := AccessGovernanceScreenshotDirectory(); value != "" {
		t.Fatalf("missing screenshot directory=%q", value)
	}
	t.Setenv("AICRM_ACCESS_UI_SCREENSHOT_DIR", "/tmp/aicrm-access-evidence")
	if value := AccessGovernanceScreenshotDirectory(); value != "/tmp/aicrm-access-evidence" {
		t.Fatalf("screenshot directory=%q", value)
	}
}

func TestTagPickerScreenshotDirectoryUsesConfigurationBoundary(t *testing.T) {
	t.Setenv("AICRM_TAG_PICKER_SCREENSHOT_DIR", "")
	if value := TagPickerScreenshotDirectory(); value != "" {
		t.Fatalf("missing screenshot directory=%q", value)
	}
	t.Setenv("AICRM_TAG_PICKER_SCREENSHOT_DIR", "/tmp/aicrm-tag-picker-evidence")
	if value := TagPickerScreenshotDirectory(); value != "/tmp/aicrm-tag-picker-evidence" {
		t.Fatalf("screenshot directory=%q", value)
	}
}

func TestComponentStatesScreenshotDirectoryUsesConfigurationBoundary(t *testing.T) {
	t.Setenv("AICRM_COMPONENT_STATES_SCREENSHOT_DIR", "")
	if value := ComponentStatesScreenshotDirectory(); value != "" {
		t.Fatalf("missing component state screenshot directory=%q", value)
	}
	t.Setenv("AICRM_COMPONENT_STATES_SCREENSHOT_DIR", "/tmp/aicrm-component-states-evidence")
	if value := ComponentStatesScreenshotDirectory(); value != "/tmp/aicrm-component-states-evidence" {
		t.Fatalf("component state screenshot directory=%q", value)
	}
}

func TestAutomationFixedContentScreenshotDirectoryUsesConfigurationBoundary(t *testing.T) {
	t.Setenv("AICRM_AUTOMATION_CONTENT_SCREENSHOT_DIR", "")
	if value := AutomationFixedContentScreenshotDirectory(); value != "" {
		t.Fatalf("missing automation fixed-content screenshot directory=%q", value)
	}
	t.Setenv("AICRM_AUTOMATION_CONTENT_SCREENSHOT_DIR", "/tmp/aicrm-automation-content-evidence")
	if value := AutomationFixedContentScreenshotDirectory(); value != "/tmp/aicrm-automation-content-evidence" {
		t.Fatalf("automation fixed-content screenshot directory=%q", value)
	}
}

func TestAudienceConfirmationScreenshotDirectoryUsesConfigurationBoundary(t *testing.T) {
	t.Setenv("AICRM_AUDIENCE_CONFIRMATION_SCREENSHOT_DIR", "")
	if value := AudienceConfirmationScreenshotDirectory(); value != "" {
		t.Fatalf("missing screenshot directory=%q", value)
	}
	t.Setenv("AICRM_AUDIENCE_CONFIRMATION_SCREENSHOT_DIR", "/tmp/aicrm-audience-confirmation-evidence")
	if value := AudienceConfirmationScreenshotDirectory(); value != "/tmp/aicrm-audience-confirmation-evidence" {
		t.Fatalf("screenshot directory=%q", value)
	}
}

func TestPublicCommerceScreenshotDirectoryUsesConfigurationBoundary(t *testing.T) {
	t.Setenv("AICRM_PUBLIC_COMMERCE_SCREENSHOT_DIR", "")
	if value := PublicCommerceScreenshotDirectory(); value != "" {
		t.Fatalf("missing public commerce screenshot directory=%q", value)
	}
	t.Setenv("AICRM_PUBLIC_COMMERCE_SCREENSHOT_DIR", "/tmp/aicrm-public-commerce-evidence")
	if value := PublicCommerceScreenshotDirectory(); value != "/tmp/aicrm-public-commerce-evidence" {
		t.Fatalf("public commerce screenshot directory=%q", value)
	}
}

func TestPublicSurveyScreenshotDirectoryUsesConfigurationBoundary(t *testing.T) {
	t.Setenv("AICRM_PUBLIC_SURVEY_SCREENSHOT_DIR", "")
	if value := PublicSurveyScreenshotDirectory(); value != "" {
		t.Fatalf("missing public Survey screenshot directory=%q", value)
	}
	t.Setenv("AICRM_PUBLIC_SURVEY_SCREENSHOT_DIR", "/tmp/aicrm-public-survey-evidence")
	if value := PublicSurveyScreenshotDirectory(); value != "/tmp/aicrm-public-survey-evidence" {
		t.Fatalf("public Survey screenshot directory=%q", value)
	}
}

func TestQuestionnaireListScreenshotDirectoryUsesConfigurationBoundary(t *testing.T) {
	t.Setenv("AICRM_QUESTIONNAIRE_LIST_SCREENSHOT_DIR", "")
	if value := QuestionnaireListScreenshotDirectory(); value != "" {
		t.Fatalf("missing questionnaire list screenshot directory=%q", value)
	}
	t.Setenv("AICRM_QUESTIONNAIRE_LIST_SCREENSHOT_DIR", "/tmp/aicrm-questionnaire-list-evidence")
	if value := QuestionnaireListScreenshotDirectory(); value != "/tmp/aicrm-questionnaire-list-evidence" {
		t.Fatalf("questionnaire list screenshot directory=%q", value)
	}
}

func TestRemainingPagesChromiumScreenshotDirectoryUsesConfigurationBoundary(t *testing.T) {
	t.Setenv("AICRM_REMAINING_PAGES_CHROMIUM_SCREENSHOT_DIR", "")
	if value := RemainingPagesChromiumScreenshotDirectory(); value != "" {
		t.Fatalf("missing remaining-page screenshot directory=%q", value)
	}
	t.Setenv("AICRM_REMAINING_PAGES_CHROMIUM_SCREENSHOT_DIR", "/tmp/aicrm-remaining-pages-evidence")
	if value := RemainingPagesChromiumScreenshotDirectory(); value != "/tmp/aicrm-remaining-pages-evidence" {
		t.Fatalf("remaining-page screenshot directory=%q", value)
	}
}

func TestNamedDatabaseURLUsesClosedMigrationAllowlist(t *testing.T) {
	allowed := map[string]string{
		"AICRM_DATABASE_URL":                 "postgres://target@localhost/aicrm",
		"AICRM_V2_AUTOMATION_DATABASE_URL":   "postgres://readonly@source/automation",
		"AICRM_AUDIENCE_SOURCE_DATABASE_URL": "postgres://readonly@source/audience",
	}
	for name, want := range allowed {
		t.Setenv(name, want)
		got, err := NamedDatabaseURL(name)
		if err != nil || got != want {
			t.Fatalf("name=%s value=%q err=%v", name, got, err)
		}
	}
	t.Setenv("AICRM_AUDIENCE_SOURCE_DATABASE_URL", "")
	if _, err := NamedDatabaseURL("AICRM_AUDIENCE_SOURCE_DATABASE_URL"); err == nil {
		t.Fatal("expected empty audience source URL rejection")
	}
	const secret = "postgres://secret-user:secret-password@source/audience"
	t.Setenv("ARBITRARY_SECRET", secret)
	if _, err := NamedDatabaseURL("ARBITRARY_SECRET"); err == nil {
		t.Fatal("expected unsupported environment name")
	} else if strings.Contains(err.Error(), secret) {
		t.Fatal("unsupported environment error exposed value")
	}
}

func TestLoadChannelHistoryMigrationOwnsReadbackConfiguration(t *testing.T) {
	t.Setenv("AICRM_CHANNEL_SOURCE_DATABASE_URL", "postgres://readonly@source/aicrm")
	t.Setenv("AICRM_CHANNEL_SNAPSHOT_KEY", "snapshot-key")
	t.Setenv("AICRM_WECOM_CORP_ID", "ww-corp")
	t.Setenv("AICRM_WECOM_AGENT_ID", "1000002")
	t.Setenv("AICRM_WECOM_SECRET", "provider-secret")
	t.Setenv("AICRM_WECOM_CONTACT_SECRET", "contact-secret")
	t.Setenv("AICRM_WECOM_CONTEXT_SIGNING_KEY", strings.Repeat("k", 32))
	t.Setenv("AICRM_CHANNEL_STATE_HMAC_KEY", strings.Repeat("h", 32))
	t.Setenv("AICRM_OUTBOUND_PROVIDER_ENABLED", "true")
	t.Setenv("AICRM_CHANNEL_PROVIDER_READ_ENABLED", "true")

	cfg, err := LoadChannelHistoryMigration()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WeComCorpID != "ww-corp" || cfg.WeComAgentID != "1000002" || !cfg.ProviderEnabled || !cfg.ProviderReadEnabled {
		t.Fatalf("unexpected channel migration config: %+v", cfg)
	}

	t.Setenv("AICRM_CHANNEL_PROVIDER_READ_ENABLED", "yes")
	if _, err = LoadChannelHistoryMigration(); err == nil {
		t.Fatal("expected invalid provider read boolean to fail closed")
	}
}

func TestCommercePushProviderDefaultsDisabledAndFailsClosed(t *testing.T) {
	t.Setenv("AICRM_DATABASE_URL", "postgres://aicrm:test@localhost/aicrm")
	cfg, err := Load()
	if err != nil || cfg.CommercePush.ProviderEnabled || cfg.CommercePush.TargetsJSON != "" || cfg.CommercePush.PayloadDataKey != "" {
		t.Fatalf("default commerce push=%+v err=%v", cfg.CommercePush, err)
	}
	t.Setenv("AICRM_COMMERCE_PUSH_PROVIDER_ENABLED", "true")
	if _, err = Load(); err == nil {
		t.Fatal("commerce push provider accepted without External Effects, target configuration, and payload key")
	}
	t.Setenv("AICRM_OUTBOUND_PROVIDER_ENABLED", "true")
	t.Setenv("AICRM_COMMERCE_PUSH_TARGETS_JSON", `{"paid-target":{"endpoint":"https://push.example.test"}}`)
	t.Setenv("AICRM_COMMERCE_PUSH_PAYLOAD_DATA_KEY", "invalid")
	if _, err = Load(); err == nil {
		t.Fatal("commerce push provider accepted invalid payload key")
	}
	key := base64.RawStdEncoding.EncodeToString(make([]byte, 32))
	t.Setenv("AICRM_COMMERCE_PUSH_PAYLOAD_DATA_KEY", key)
	cfg, err = Load()
	if err != nil || !cfg.CommercePush.ProviderEnabled || cfg.CommercePush.PayloadDataKey != key {
		t.Fatalf("commerce push runtime=%+v err=%v", cfg.CommercePush, err)
	}
}

func TestGroupOpsDirectoryReadDoesNotEnableDispatch(t *testing.T) {
	t.Setenv("AICRM_DATABASE_URL", "postgres://aicrm:test@localhost/aicrm")
	t.Setenv("AICRM_GROUP_OPS_PROVIDER_ENABLED", "false")
	t.Setenv("AICRM_OUTBOUND_PROVIDER_ENABLED", "false")
	t.Setenv("AICRM_GROUP_OPS_PROVIDER_READ_ENABLED", "true")
	if _, err := Load(); err == nil {
		t.Fatal("accepted reads without contact credentials")
	}
	t.Setenv("AICRM_WECOM_ENABLED", "true")
	t.Setenv("AICRM_WECOM_CORP_ID", "corp")
	t.Setenv("AICRM_WECOM_AGENT_ID", "1000001")
	t.Setenv("AICRM_WECOM_SECRET", "app-secret")
	t.Setenv("AICRM_WECOM_CONTACT_SECRET", "contact-secret")
	t.Setenv("AICRM_WECOM_CONTEXT_SIGNING_KEY", strings.Repeat("k", 32))
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.GroupOps.ProviderReadEnabled || cfg.GroupOps.ProviderEnabled || cfg.Effects.ProviderEnabled {
		t.Fatal("directory read changed dispatch eligibility")
	}
	t.Setenv("AICRM_GROUP_OPS_PROVIDER_READ_ENABLED", "invalid")
	if _, err := Load(); err == nil {
		t.Fatal("accepted invalid read gate")
	}
}
