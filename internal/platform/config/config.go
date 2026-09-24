// Package config owns all environment-based runtime configuration.
package config

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Role string

const (
	RoleAPI           Role = "api"
	RoleWorker        Role = "worker"
	RoleEffectsWorker Role = "effects-worker"
	// RolePaymentReconcile is a deliberately single-purpose, one-shot runtime
	// for an explicitly selected existing WeChat Pay payment.  It has no HTTP
	// listener and cannot create a payment or call a Provider write API.
	RolePaymentReconcile Role = "payment-reconcile"

	// These defaults are the deployment policy used when a Runtime is assembled
	// without an environment loader, for example by an isolated composition
	// fixture. Keep them aligned with Load: the Config Center's closed catalog
	// must receive the same valid baseline in either construction path.
	DefaultWorkerLimit                          = 25
	DefaultMessageArchivePageLimit       uint32 = 100
	DefaultMessageArchivePageBudget             = 10
	DefaultContextTokenTTL                      = 5 * time.Minute
	DefaultAutomationMaxRecipientsPerRun        = 1
	DefaultWeComMaterialUploadTimeout           = 120 * time.Second
	MaximumWeComMaterialUploadTimeout           = 4 * time.Minute
)

type Runtime struct {
	ListenAddress              string
	ReleaseSHA                 string
	Role                       Role
	DatabaseURL                string
	PublicOrigin               string
	H5PublicOrigin             string
	Bootstrap                  Bootstrap
	WeCom                      WeCom
	GroupOps                   GroupOps
	AutomationOperations       AutomationOperations
	Effects                    Effects
	TagCatalog                 TagCatalogProvider
	Survey                     Survey
	Referral                   Referral
	CommercePush               CommercePush
	WeChatPay                  WeChatPay
	Alipay                     Alipay
	WeChatShop                 WeChatShop
	WorkerOwner                string
	WorkerLimit                int
	PaymentReconcileID         int64
	CustomerSyncTrigger        string
	HXCDashboard               HXCDashboard
	OperationCycleServiceToken string
	AIAssistant                AIAssistant
	AIGeneration               AIGeneration
	OpenPlatform               OpenPlatform
	Ops                        Ops
}

type Bootstrap struct {
	Enabled     bool
	Username    string
	Password    string
	DisplayName string
}

type WeCom struct {
	Enabled                           bool
	CallbackEnabled                   bool
	CustomerSyncEnabled               bool
	ContactDescriptionProviderEnabled bool
	CorpID                            string
	AgentID                           string
	Secret                            string
	ContactSecret                     string
	CallbackToken                     string
	CallbackAESKey                    string
	ContextSigningKey                 string
	ChannelStateHMACKey               string
	ContextTokenTTL                   time.Duration
	MaterialUploadTimeout             time.Duration
	ChannelProviderReadEnabled        bool
	ChannelQRProviderEnabled          bool
	ChannelMediaPrepProviderEnabled   bool
	ChannelWelcomeProviderEnabled     bool
	ChannelTagProviderEnabled         bool
	CustomerTagProviderEnabled        bool
	StaffDirectoryRefreshInterval     time.Duration
	MessageArchiveEnabled             bool
	MessageArchiveSecret              string
	MessageArchiveRunnerPath          string
	MessageArchiveLibraryPath         string
	MessageArchivePrivateKeyPaths     map[uint32]string
	MessageArchivePageLimit           uint32
	MessageArchivePageBudget          int
	// APIBase and HTTPClient are composition-test injection only. Load never
	// populates them, so a deployed runtime continues to use the fixed provider
	// origin and default HTTP client.
	APIBase    string
	HTTPClient *http.Client
}

// GroupOps contains only the inbound protocol secret for the local Group Ops
// webhook. It is independent from WeCom/customer credentials and is never
// exposed through a descriptor or structured log.
type GroupOps struct {
	WebhookSecret       string
	ProviderReadEnabled bool
	ProviderEnabled     bool
}

type AutomationProviderMode string

const (
	AutomationProviderDisabled AutomationProviderMode = "disabled"
	AutomationProviderProbe    AutomationProviderMode = "probe"
	AutomationProviderLimited  AutomationProviderMode = "limited"
)

// AutomationOperations is deliberately narrower than the global External
// Effects switch. Enabling the effects worker must never implicitly authorize
// an Automation Operations send.
type AutomationOperations struct {
	WebhookSecret             string
	AudiencePushWebhookSecret string
	ProviderMode              AutomationProviderMode
	ProviderPermission        string
	MaxRecipientsPerRun       int
}

func (c AutomationOperations) ProviderEnabled() bool {
	return c.ProviderMode == AutomationProviderProbe || c.ProviderMode == AutomationProviderLimited
}

// NormalizeRuntimePolicyDefaults fills only omitted policy values in a Runtime
// supplied directly by a composition fixture. It does not repair malformed
// explicit values: validation remains responsible for rejecting those values.
func NormalizeRuntimePolicyDefaults(cfg Runtime) Runtime {
	if cfg.WorkerLimit == 0 {
		cfg.WorkerLimit = DefaultWorkerLimit
	}
	if cfg.WeCom.MessageArchivePageLimit == 0 {
		cfg.WeCom.MessageArchivePageLimit = DefaultMessageArchivePageLimit
	}
	if cfg.WeCom.MessageArchivePageBudget == 0 {
		cfg.WeCom.MessageArchivePageBudget = DefaultMessageArchivePageBudget
	}
	if cfg.WeCom.ContextTokenTTL == 0 {
		cfg.WeCom.ContextTokenTTL = DefaultContextTokenTTL
	}
	if cfg.WeCom.MaterialUploadTimeout == 0 {
		cfg.WeCom.MaterialUploadTimeout = DefaultWeComMaterialUploadTimeout
	}
	if cfg.AutomationOperations.ProviderMode == "" {
		cfg.AutomationOperations.ProviderMode = AutomationProviderDisabled
	}
	if cfg.AutomationOperations.MaxRecipientsPerRun == 0 {
		cfg.AutomationOperations.MaxRecipientsPerRun = DefaultAutomationMaxRecipientsPerRun
	}
	return cfg
}

type Effects struct{ ProviderEnabled bool }

// CommercePush keeps only deployment-owned opaque target configuration. Product
// stores references, while this runtime section keeps endpoints and signing
// material out of Product rows and HTTP responses.
type CommercePush struct {
	ProviderEnabled bool
	TargetsJSON     string
	PayloadDataKey  string
}

// Referral keeps the independent secret used to bind opaque invitation
// capabilities. It must never reuse a Survey, payment, or Provider key.
type Referral struct{ TokenDataKey string }

type HXCDashboard struct {
	Enabled                     bool
	IdentityWriteEnabled        bool
	UnionIDVerified             bool
	SourceDSN                   string
	UnionIDScope                string
	SubjectHMACKey              string
	IdentityObservationVaultKey string
	SyncTrigger                 string
}
type ChannelHistoryMigration struct {
	SourceDatabaseURL   string
	SnapshotKey         string
	WeComCorpID         string
	WeComAgentID        string
	WeComSecret         string
	WeComContactSecret  string
	ChannelStateHMACKey string
	ProviderEnabled     bool
	ProviderReadEnabled bool
}

type RadarMigration struct{ SourceDatabaseURL string }

// OpenPlatformMigration holds only the trusted corporate boundary needed to
// decide whether a donor owner_userid/external_userid scope may be retained.
// It deliberately has no source credential and no provider side effect.
type OpenPlatformMigration struct{ WeComCorpID string }

func LoadOpenPlatformMigration() (OpenPlatformMigration, error) {
	value := OpenPlatformMigration{WeComCorpID: os.Getenv("AICRM_WECOM_CORP_ID")}
	if strings.TrimSpace(value.WeComCorpID) != value.WeComCorpID {
		return OpenPlatformMigration{}, errors.New("invalid open platform migration configuration")
	}
	return value, nil
}

func LoadRadarMigration() (RadarMigration, error) {
	value := RadarMigration{SourceDatabaseURL: os.Getenv("AICRM_RADAR_SOURCE_DATABASE_URL")}
	if strings.TrimSpace(value.SourceDatabaseURL) != value.SourceDatabaseURL {
		return RadarMigration{}, errors.New("invalid radar migration configuration")
	}
	return value, nil
}

// LoadChannelHistoryMigration keeps migration credentials inside the sole
// environment-owning package without adding them to the long-lived API
// runtime configuration.
func LoadChannelHistoryMigration() (ChannelHistoryMigration, error) {
	value := ChannelHistoryMigration{
		SourceDatabaseURL:   os.Getenv("AICRM_CHANNEL_SOURCE_DATABASE_URL"),
		SnapshotKey:         os.Getenv("AICRM_CHANNEL_SNAPSHOT_KEY"),
		WeComCorpID:         os.Getenv("AICRM_WECOM_CORP_ID"),
		WeComAgentID:        os.Getenv("AICRM_WECOM_AGENT_ID"),
		WeComSecret:         os.Getenv("AICRM_WECOM_SECRET"),
		WeComContactSecret:  os.Getenv("AICRM_WECOM_CONTACT_SECRET"),
		ChannelStateHMACKey: os.Getenv("AICRM_CHANNEL_STATE_HMAC_KEY"),
	}
	var err error
	if value.ProviderEnabled, err = strictBool("AICRM_OUTBOUND_PROVIDER_ENABLED", false); err != nil {
		return ChannelHistoryMigration{}, err
	}
	if value.ProviderReadEnabled, err = strictBool("AICRM_CHANNEL_PROVIDER_READ_ENABLED", false); err != nil {
		return ChannelHistoryMigration{}, err
	}
	if strings.TrimSpace(value.SourceDatabaseURL) != value.SourceDatabaseURL || strings.TrimSpace(value.SnapshotKey) != value.SnapshotKey {
		return ChannelHistoryMigration{}, errors.New("invalid channel history migration configuration")
	}
	return value, nil
}

type AIAssistant struct {
	ExcelBatchURL                             string
	ExcelBatchToken                           string
	UIEnabled, IntakeEnabled, DispatchEnabled bool
	IntegrationKey, IntegrationSecret         string
	IntegrationActorID                        int64
	ProviderPermission                        string
}

// AIGeneration holds the provider-neutral OpenAI-compatible generation
// endpoint. Its key remains deployment-owned and never enters Config releases
// or Automation-owned prompt snapshots.
type AIGeneration struct {
	Enabled bool
	BaseURL string
	APIKey  string
	Model   string
	Timeout time.Duration
}

// Ready reports whether the deployment-owned portion of a generation
// provider configuration is safe to activate. Callers use it when a Config
// Center release can turn the feature on after environment loading; it never
// returns or records the secret itself.
func (c AIGeneration) Ready() bool {
	values := []string{c.BaseURL, c.APIKey, c.Model}
	if nonEmptyCount(values) != len(values) || !validAIGenerationBaseURL(c.BaseURL) || c.Timeout < time.Second || c.Timeout > 2*time.Minute {
		return false
	}
	for _, value := range values {
		if strings.TrimSpace(value) != value || strings.ContainsAny(value, "\r\n\x00") {
			return false
		}
	}
	return true
}

// OpenPlatform contains only the signing and proxy trust boundary for Access
// machine credentials. It is deliberately separate from AI Assistant HMAC and
// from ordinary Config/AdminOps secret projections.
type OpenPlatform struct {
	JWTSigningKey     string
	TrustedProxyCIDRs []string
}
type Survey struct {
	DataKey                   string
	IdentityPhoneDataKey      string
	CompletionProviderEnabled bool
	CompletionTargetsJSON     string
	// CompletionNavigationTargetsJSON is deliberately separate from
	// CompletionTargetsJSON. The latter is an External Effects provider
	// configuration and must never be exposed to a browser. This value only
	// maps Survey-owned opaque navigation references to public, allowlisted
	// HTTPS destinations.
	CompletionNavigationTargetsJSON string
	OAuthEnabled                    bool
	OAuthAppID                      string
	OAuthSecret                     string
	OAuthOpenPlatformID             string
	OAuthScope                      string
}

// TagCatalogProvider separates the read-only catalog projection from an
// official directory write. Both remain disabled until their own explicit
// authorization is present; a catalog read grant can never enable mutation.
type TagCatalogProvider struct {
	Enabled            bool
	Permission         string
	MutationEnabled    bool
	MutationPermission string
}

type WeChatPay struct {
	Enabled                          bool
	AppID, AppSecret, AppScope       string
	H5OAuthEnabled                   bool
	H5AppID, H5AppSecret, H5AppScope string
	OrderContactDataKey              string
	MerchantID, MerchantSerial       string
	PrivateKeyPath, PlatformCertPath string
	APIV3Key                         string
	// ProfitSharingEnabled is a separate money-moving capability. It defaults
	// closed even when ordinary WeChat Pay is enabled, so a payment deployment
	// cannot begin receiver registration, frozen funds, or split instructions.
	ProfitSharingEnabled bool
	// ProfitSharingAuthMode is explicit whenever the money-moving capability is
	// enabled. It is never inferred from a certificate serial or an optional
	// public-key id: Composition must select the matching SDK trust material.
	ProfitSharingAuthMode    string
	ProfitSharingPublicKeyID string
}

type WeChatShop struct {
	Enabled                               bool
	AppID, AppSecret                      string
	CallbackToken, CallbackEncodingAESKey string
}

// Alipay contains deployment-owned credentials for web payments. The private
// key and certificates are referenced by path so they never enter runtime
// snapshots or API responses.
type Alipay struct {
	Enabled        bool
	Production     bool
	AppID          string
	PrivateKeyPath string
	// Optional protected file containing the base64 AES key configured in the
	// Alipay application. It is not required for web payment flows.
	ContentEncryptionKeyPath string
	AlipayPublicKey          string
	AppCertPath              string
	AlipayCertPath           string
	AlipayRootPath           string
	Gateway                  string
	NotifyURL                string
	ReturnURL                string
}

func Load() (Runtime, error) {
	databaseURL, err := DatabaseURL()
	if err != nil {
		return Runtime{}, err
	}
	cfg := Runtime{
		ListenAddress:              valueOrDefault("AICRM_LISTEN_ADDR", "127.0.0.1:8080"),
		ReleaseSHA:                 valueOrDefault("AICRM_RELEASE_SHA", "development"),
		Role:                       Role(valueOrDefault("AICRM_ROLE", string(RoleAPI))),
		DatabaseURL:                databaseURL,
		PublicOrigin:               valueOrDefault("AICRM_PUBLIC_ORIGIN", "https://id-dev.youcangogogo.com"),
		WorkerOwner:                valueOrDefault("AICRM_WORKER_OWNER", "aicrm-wecom-worker"),
		WorkerLimit:                DefaultWorkerLimit,
		CustomerSyncTrigger:        os.Getenv("AICRM_CUSTOMER_SYNC_TRIGGER"),
		HXCDashboard:               HXCDashboard{SourceDSN: os.Getenv("AICRM_HXC_SOURCE_DSN"), UnionIDScope: os.Getenv("AICRM_HXC_UNIONID_SCOPE"), SubjectHMACKey: os.Getenv("AICRM_HXC_SUBJECT_HMAC_KEY"), IdentityObservationVaultKey: os.Getenv("AICRM_IDENTITY_OBSERVATION_VAULT_KEY"), SyncTrigger: os.Getenv("AICRM_HXC_SYNC_TRIGGER")},
		OperationCycleServiceToken: os.Getenv("AICRM_OPERATION_CYCLE_SERVICE_TOKEN"),
		AIAssistant:                AIAssistant{ExcelBatchURL: os.Getenv("EXCEL_BATCH_URL"), ExcelBatchToken: os.Getenv("EXCEL_BATCH_TOKEN"), UIEnabled: true, IntegrationKey: os.Getenv("AICRM_AI_ASSISTANT_INTEGRATION_KEY"), IntegrationSecret: os.Getenv("AICRM_AI_ASSISTANT_INTEGRATION_SECRET"), ProviderPermission: os.Getenv("AICRM_AI_ASSISTANT_PROVIDER_PERMISSION")},
		AIGeneration:               AIGeneration{BaseURL: os.Getenv("AICRM_AI_GENERATION_BASE_URL"), APIKey: os.Getenv("AICRM_AI_GENERATION_API_KEY"), Model: os.Getenv("AICRM_AI_GENERATION_MODEL"), Timeout: 30 * time.Second},
		OpenPlatform:               OpenPlatform{JWTSigningKey: os.Getenv("AICRM_OPEN_PLATFORM_JWT_SIGNING_KEY"), TrustedProxyCIDRs: splitCommaSeparated("AICRM_OPEN_PLATFORM_TRUSTED_PROXY_CIDRS")},
		Bootstrap: Bootstrap{
			Username: os.Getenv("AICRM_BOOTSTRAP_USERNAME"), Password: os.Getenv("AICRM_BOOTSTRAP_PASSWORD"),
			DisplayName: os.Getenv("AICRM_BOOTSTRAP_DISPLAY_NAME"),
		},
		WeCom: WeCom{
			CorpID: os.Getenv("AICRM_WECOM_CORP_ID"), AgentID: os.Getenv("AICRM_WECOM_AGENT_ID"),
			MessageArchiveSecret: os.Getenv("AICRM_WECOM_MESSAGE_ARCHIVE_SECRET"), MessageArchiveRunnerPath: os.Getenv("AICRM_WECOM_MESSAGE_ARCHIVE_RUNNER_PATH"), MessageArchiveLibraryPath: os.Getenv("AICRM_WECOM_MESSAGE_ARCHIVE_LIBRARY_PATH"), MessageArchivePageLimit: DefaultMessageArchivePageLimit, MessageArchivePageBudget: DefaultMessageArchivePageBudget,
			Secret: os.Getenv("AICRM_WECOM_SECRET"), ContactSecret: os.Getenv("AICRM_WECOM_CONTACT_SECRET"), CallbackToken: os.Getenv("AICRM_WECOM_CALLBACK_TOKEN"),
			CallbackAESKey: os.Getenv("AICRM_WECOM_CALLBACK_AES_KEY"), ContextSigningKey: os.Getenv("AICRM_WECOM_CONTEXT_SIGNING_KEY"),
			ChannelStateHMACKey:           os.Getenv("AICRM_CHANNEL_STATE_HMAC_KEY"),
			StaffDirectoryRefreshInterval: 15 * time.Minute,
			ContextTokenTTL:               DefaultContextTokenTTL,
			MaterialUploadTimeout:         DefaultWeComMaterialUploadTimeout,
		},
		GroupOps: GroupOps{WebhookSecret: os.Getenv("AICRM_GROUP_OPS_WEBHOOK_SECRET")},
		AutomationOperations: AutomationOperations{
			WebhookSecret:             os.Getenv("AICRM_AUTOMATION_OPS_WEBHOOK_SECRET"),
			AudiencePushWebhookSecret: os.Getenv("AICRM_AUDIENCE_PUSH_WEBHOOK_SECRET"),
			ProviderMode:              AutomationProviderMode(valueOrDefault("AICRM_AUTOMATION_OPS_PROVIDER_MODE", string(AutomationProviderDisabled))),
			ProviderPermission:        os.Getenv("AICRM_AUTOMATION_OPS_PROVIDER_PERMISSION"),
			MaxRecipientsPerRun:       DefaultAutomationMaxRecipientsPerRun,
		},
		Survey:       Survey{DataKey: os.Getenv("AICRM_SURVEY_DATA_KEY"), IdentityPhoneDataKey: os.Getenv("AICRM_IDENTITY_PHONE_DATA_KEY"), CompletionTargetsJSON: os.Getenv("AICRM_SURVEY_COMPLETION_TARGETS_JSON"), CompletionNavigationTargetsJSON: os.Getenv("AICRM_SURVEY_COMPLETION_NAVIGATION_TARGETS_JSON"), OAuthAppID: os.Getenv("AICRM_SURVEY_OAUTH_APP_ID"), OAuthSecret: os.Getenv("AICRM_SURVEY_OAUTH_SECRET"), OAuthOpenPlatformID: os.Getenv("AICRM_SURVEY_OAUTH_OPEN_PLATFORM_ID"), OAuthScope: valueOrDefault("AICRM_SURVEY_OAUTH_SCOPE", "snsapi_userinfo")},
		Referral:     Referral{TokenDataKey: os.Getenv("AICRM_REFERRAL_TOKEN_DATA_KEY")},
		CommercePush: CommercePush{TargetsJSON: os.Getenv("AICRM_COMMERCE_PUSH_TARGETS_JSON"), PayloadDataKey: os.Getenv("AICRM_COMMERCE_PUSH_PAYLOAD_DATA_KEY")},
	}
	if cfg.Ops, err = loadOps(); err != nil {
		return Runtime{}, err
	}
	if cfg.Survey.OAuthEnabled, err = strictBool("AICRM_SURVEY_OAUTH_ENABLED", false); err != nil {
		return Runtime{}, err
	}
	if cfg.Survey.CompletionProviderEnabled, err = strictBool("AICRM_SURVEY_COMPLETION_PROVIDER_ENABLED", false); err != nil {
		return Runtime{}, err
	}
	if cfg.CommercePush.ProviderEnabled, err = strictBool("AICRM_COMMERCE_PUSH_PROVIDER_ENABLED", false); err != nil {
		return Runtime{}, err
	}
	if cfg.Effects.ProviderEnabled, err = strictBool("AICRM_OUTBOUND_PROVIDER_ENABLED", false); err != nil {
		return Runtime{}, err
	}
	if cfg.HXCDashboard.Enabled, err = strictBool("AICRM_HXC_SYNC_ENABLED", false); err != nil {
		return Runtime{}, err
	}
	if cfg.HXCDashboard.IdentityWriteEnabled, err = strictBool("AICRM_HXC_IDENTITY_WRITE_ENABLED", false); err != nil {
		return Runtime{}, err
	}
	if cfg.HXCDashboard.UnionIDVerified, err = strictBool("AICRM_HXC_UNIONID_VERIFIED", false); err != nil {
		return Runtime{}, err
	}
	if cfg.AIAssistant.UIEnabled, err = strictBool("AICRM_AI_ASSISTANT_UI_ENABLED", true); err != nil {
		return Runtime{}, err
	}
	if cfg.AIAssistant.IntakeEnabled, err = strictBool("AICRM_AI_ASSISTANT_INTAKE_ENABLED", false); err != nil {
		return Runtime{}, err
	}
	if cfg.AIAssistant.DispatchEnabled, err = strictBool("AICRM_AI_ASSISTANT_DISPATCH_ENABLED", false); err != nil {
		return Runtime{}, err
	}
	if cfg.AIGeneration.Enabled, err = strictBool("AICRM_AI_GENERATION_ENABLED", false); err != nil {
		return Runtime{}, err
	}
	if raw := os.Getenv("AICRM_AI_GENERATION_TIMEOUT_SECONDS"); raw != "" {
		seconds, parseErr := strconv.Atoi(raw)
		if parseErr != nil || seconds < 1 || seconds > 120 {
			return Runtime{}, errors.New("invalid AICRM_AI_GENERATION_TIMEOUT_SECONDS")
		}
		cfg.AIGeneration.Timeout = time.Duration(seconds) * time.Second
	}
	if raw := os.Getenv("AICRM_AI_ASSISTANT_INTEGRATION_ACTOR_ID"); raw != "" {
		cfg.AIAssistant.IntegrationActorID, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || cfg.AIAssistant.IntegrationActorID < 1 {
			return Runtime{}, errors.New("invalid AICRM_AI_ASSISTANT_INTEGRATION_ACTOR_ID")
		}
	}
	if cfg.TagCatalog.Enabled, err = strictBool("AICRM_WECOM_TAG_CATALOG_PROVIDER_ENABLED", false); err != nil {
		return Runtime{}, err
	}
	if cfg.TagCatalog.MutationEnabled, err = strictBool("AICRM_WECOM_TAG_CATALOG_MUTATION_PROVIDER_ENABLED", false); err != nil {
		return Runtime{}, err
	}
	if cfg.WeChatPay.Enabled, err = strictBool("AICRM_WECHAT_PAY_PROVIDER_ENABLED", false); err != nil {
		return Runtime{}, err
	}
	if cfg.WeChatPay.H5OAuthEnabled, err = strictBool("AICRM_WECHAT_PAY_H5_OAUTH_ENABLED", false); err != nil {
		return Runtime{}, err
	}
	if cfg.WeChatPay.ProfitSharingEnabled, err = strictBool("AICRM_WECHAT_PAY_PROFIT_SHARING_ENABLED", false); err != nil {
		return Runtime{}, err
	}
	cfg.WeChatPay.AppID = os.Getenv("AICRM_WECHAT_PAY_APP_ID")
	cfg.WeChatPay.AppSecret = os.Getenv("AICRM_WECHAT_PAY_APP_SECRET")
	cfg.WeChatPay.AppScope = os.Getenv("AICRM_WECHAT_PAY_APP_SCOPE")
	cfg.WeChatPay.H5AppID = os.Getenv("AICRM_WECHAT_PAY_H5_APP_ID")
	cfg.WeChatPay.H5AppSecret = os.Getenv("AICRM_WECHAT_PAY_H5_APP_SECRET")
	cfg.WeChatPay.H5AppScope = os.Getenv("AICRM_WECHAT_PAY_H5_APP_SCOPE")
	cfg.WeChatPay.OrderContactDataKey = os.Getenv("AICRM_ORDER_CONTACT_DATA_KEY")
	cfg.WeChatPay.MerchantID = os.Getenv("AICRM_WECHAT_PAY_MERCHANT_ID")
	cfg.WeChatPay.MerchantSerial = os.Getenv("AICRM_WECHAT_PAY_MERCHANT_SERIAL")
	cfg.WeChatPay.PrivateKeyPath = os.Getenv("AICRM_WECHAT_PAY_PRIVATE_KEY_PATH")
	cfg.WeChatPay.PlatformCertPath = os.Getenv("AICRM_WECHAT_PAY_PLATFORM_CERT_PATH")
	cfg.WeChatPay.APIV3Key = os.Getenv("AICRM_WECHAT_PAY_API_V3_KEY")
	cfg.WeChatPay.ProfitSharingAuthMode = os.Getenv("AICRM_WECHAT_PAY_PROFIT_SHARING_AUTH_MODE")
	cfg.WeChatPay.ProfitSharingPublicKeyID = os.Getenv("AICRM_WECHAT_PAY_PROFIT_SHARING_PUBLIC_KEY_ID")
	if cfg.Alipay.Enabled, err = strictBool("AICRM_ALIPAY_PROVIDER_ENABLED", false); err != nil {
		return Runtime{}, err
	}
	if cfg.Alipay.Production, err = strictBool("AICRM_ALIPAY_PRODUCTION", true); err != nil {
		return Runtime{}, err
	}
	cfg.Alipay.AppID = os.Getenv("AICRM_ALIPAY_APP_ID")
	cfg.Alipay.PrivateKeyPath = os.Getenv("AICRM_ALIPAY_PRIVATE_KEY_PATH")
	cfg.Alipay.ContentEncryptionKeyPath = os.Getenv("AICRM_ALIPAY_CONTENT_ENCRYPTION_KEY_PATH")
	cfg.Alipay.AlipayPublicKey = os.Getenv("AICRM_ALIPAY_PUBLIC_KEY")
	cfg.Alipay.AppCertPath = os.Getenv("AICRM_ALIPAY_APP_CERT_PATH")
	cfg.Alipay.AlipayCertPath = os.Getenv("AICRM_ALIPAY_ALIPAY_CERT_PATH")
	cfg.Alipay.AlipayRootPath = os.Getenv("AICRM_ALIPAY_ROOT_CERT_PATH")
	cfg.Alipay.Gateway = valueOrDefault("AICRM_ALIPAY_GATEWAY", "https://openapi.alipay.com/gateway.do")
	cfg.Alipay.NotifyURL = os.Getenv("AICRM_ALIPAY_NOTIFY_URL")
	cfg.Alipay.ReturnURL = os.Getenv("AICRM_ALIPAY_RETURN_URL")
	if cfg.WeChatShop.Enabled, err = strictBool("AICRM_WECHAT_SHOP_PROVIDER_ENABLED", false); err != nil {
		return Runtime{}, err
	}
	cfg.WeChatShop.AppID = os.Getenv("AICRM_WECHAT_SHOP_APP_ID")
	cfg.WeChatShop.AppSecret = os.Getenv("AICRM_WECHAT_SHOP_APP_SECRET")
	cfg.WeChatShop.CallbackToken = os.Getenv("AICRM_WECHAT_SHOP_CALLBACK_TOKEN")
	cfg.WeChatShop.CallbackEncodingAESKey = os.Getenv("AICRM_WECHAT_SHOP_CALLBACK_AES_KEY")
	cfg.TagCatalog.Permission = os.Getenv("AICRM_WECOM_TAG_CATALOG_PROVIDER_PERMISSION")
	cfg.TagCatalog.MutationPermission = os.Getenv("AICRM_WECOM_TAG_CATALOG_MUTATION_PROVIDER_PERMISSION")
	if cfg.WeCom.Enabled, err = strictBool("AICRM_WECOM_ENABLED", false); err != nil {
		return Runtime{}, err
	}
	if cfg.WeCom.CallbackEnabled, err = strictBool("AICRM_WECOM_CALLBACK_ENABLED", false); err != nil {
		return Runtime{}, err
	}
	if cfg.WeCom.CustomerSyncEnabled, err = strictBool("AICRM_WECOM_CUSTOMER_SYNC_ENABLED", false); err != nil {
		return Runtime{}, err
	}
	if cfg.WeCom.ContactDescriptionProviderEnabled, err = strictBool("AICRM_WECOM_CONTACT_DESCRIPTION_PROVIDER_ENABLED", false); err != nil {
		return Runtime{}, err
	}
	if cfg.WeCom.MessageArchiveEnabled, err = strictBool("AICRM_WECOM_MESSAGE_ARCHIVE_ENABLED", false); err != nil {
		return Runtime{}, err
	}
	if raw := os.Getenv("AICRM_WECOM_MATERIAL_UPLOAD_TIMEOUT_SECONDS"); raw != "" {
		seconds, parseErr := strconv.Atoi(raw)
		if parseErr != nil || seconds < 1 || time.Duration(seconds)*time.Second > MaximumWeComMaterialUploadTimeout {
			return Runtime{}, errors.New("invalid AICRM_WECOM_MATERIAL_UPLOAD_TIMEOUT_SECONDS")
		}
		cfg.WeCom.MaterialUploadTimeout = time.Duration(seconds) * time.Second
	}
	if raw := os.Getenv("AICRM_WECOM_MESSAGE_ARCHIVE_PAGE_LIMIT"); raw != "" {
		value, parseErr := strconv.ParseUint(raw, 10, 32)
		if parseErr != nil || value < 1 || value > 1000 {
			return Runtime{}, errors.New("invalid AICRM_WECOM_MESSAGE_ARCHIVE_PAGE_LIMIT")
		}
		cfg.WeCom.MessageArchivePageLimit = uint32(value)
	}
	if raw := os.Getenv("AICRM_WECOM_MESSAGE_ARCHIVE_PAGE_BUDGET"); raw != "" {
		value, parseErr := strconv.Atoi(raw)
		if parseErr != nil || value < 1 || value > 1000 {
			return Runtime{}, errors.New("invalid AICRM_WECOM_MESSAGE_ARCHIVE_PAGE_BUDGET")
		}
		cfg.WeCom.MessageArchivePageBudget = value
	}
	if raw := os.Getenv("AICRM_WECOM_MESSAGE_ARCHIVE_PRIVATE_KEY_PATHS"); raw != "" {
		var paths map[string]string
		if json.Unmarshal([]byte(raw), &paths) != nil || len(paths) == 0 {
			return Runtime{}, errors.New("invalid AICRM_WECOM_MESSAGE_ARCHIVE_PRIVATE_KEY_PATHS")
		}
		cfg.WeCom.MessageArchivePrivateKeyPaths = map[uint32]string{}
		for version, path := range paths {
			parsed, parseErr := strconv.ParseUint(version, 10, 32)
			if parseErr != nil || parsed == 0 || path == "" || strings.TrimSpace(path) != path {
				return Runtime{}, errors.New("invalid AICRM_WECOM_MESSAGE_ARCHIVE_PRIVATE_KEY_PATHS")
			}
			cfg.WeCom.MessageArchivePrivateKeyPaths[uint32(parsed)] = path
		}
	}
	if cfg.WeCom.ChannelProviderReadEnabled, err = strictBool("AICRM_CHANNEL_PROVIDER_READ_ENABLED", false); err != nil {
		return Runtime{}, err
	}
	if cfg.WeCom.ChannelQRProviderEnabled, err = strictBool("AICRM_CHANNEL_QR_PROVIDER_ENABLED", false); err != nil {
		return Runtime{}, err
	}
	if cfg.WeCom.ChannelMediaPrepProviderEnabled, err = strictBool("AICRM_CHANNEL_MEDIA_PREP_PROVIDER_ENABLED", false); err != nil {
		return Runtime{}, err
	}
	if cfg.WeCom.ChannelWelcomeProviderEnabled, err = strictBool("AICRM_CHANNEL_WELCOME_PROVIDER_ENABLED", false); err != nil {
		return Runtime{}, err
	}
	if cfg.WeCom.ChannelTagProviderEnabled, err = strictBool("AICRM_CHANNEL_TAG_PROVIDER_ENABLED", false); err != nil {
		return Runtime{}, err
	}
	if cfg.WeCom.CustomerTagProviderEnabled, err = strictBool("AICRM_CUSTOMER_TAG_PROVIDER_ENABLED", false); err != nil {
		return Runtime{}, err
	}
	if raw := os.Getenv("AICRM_CHANNEL_STAFF_REFRESH_INTERVAL"); raw != "" {
		cfg.WeCom.StaffDirectoryRefreshInterval, err = time.ParseDuration(raw)
		if err != nil || cfg.WeCom.StaffDirectoryRefreshInterval < 5*time.Minute || cfg.WeCom.StaffDirectoryRefreshInterval > 24*time.Hour {
			return Runtime{}, errors.New("invalid AICRM_CHANNEL_STAFF_REFRESH_INTERVAL")
		}
	}
	if cfg.GroupOps.ProviderEnabled, err = strictBool("AICRM_GROUP_OPS_PROVIDER_ENABLED", false); err != nil {
		return Runtime{}, err
	}
	if cfg.GroupOps.ProviderReadEnabled, err = strictBool("AICRM_GROUP_OPS_PROVIDER_READ_ENABLED", false); err != nil {
		return Runtime{}, err
	}
	if raw := os.Getenv("AICRM_SIDEBAR_CONTEXT_TOKEN_TTL_SECONDS"); raw != "" {
		seconds, parseErr := strconv.Atoi(raw)
		if parseErr != nil || seconds < 60 || seconds > 86400 {
			return Runtime{}, errors.New("invalid AICRM_SIDEBAR_CONTEXT_TOKEN_TTL_SECONDS")
		}
		cfg.WeCom.ContextTokenTTL = time.Duration(seconds) * time.Second
	}
	if raw := os.Getenv("AICRM_WORKER_LIMIT"); raw != "" {
		cfg.WorkerLimit, err = strconv.Atoi(raw)
		if err != nil || cfg.WorkerLimit < 1 || cfg.WorkerLimit > 100 {
			return Runtime{}, errors.New("invalid AICRM_WORKER_LIMIT")
		}
	}
	if raw := os.Getenv("AICRM_AUTOMATION_OPS_MAX_RECIPIENTS"); raw != "" {
		cfg.AutomationOperations.MaxRecipientsPerRun, err = strconv.Atoi(raw)
		if err != nil || cfg.AutomationOperations.MaxRecipientsPerRun < 1 || cfg.AutomationOperations.MaxRecipientsPerRun > 100000 {
			return Runtime{}, errors.New("invalid AICRM_AUTOMATION_OPS_MAX_RECIPIENTS")
		}
	}
	if strings.TrimSpace(cfg.ListenAddress) != cfg.ListenAddress || cfg.ListenAddress == "" {
		return Runtime{}, errors.New("invalid AICRM_LISTEN_ADDR")
	}
	if _, _, err := net.SplitHostPort(cfg.ListenAddress); err != nil {
		return Runtime{}, errors.New("invalid AICRM_LISTEN_ADDR")
	}
	if strings.TrimSpace(cfg.ReleaseSHA) != cfg.ReleaseSHA || cfg.ReleaseSHA == "" {
		return Runtime{}, errors.New("invalid AICRM_RELEASE_SHA")
	}
	switch cfg.Role {
	case RoleAPI, RoleWorker, RoleEffectsWorker, RolePaymentReconcile:
	default:
		return Runtime{}, errors.New("invalid AICRM_ROLE")
	}
	if cfg.Role == RolePaymentReconcile {
		raw, ok := os.LookupEnv("AICRM_PAYMENT_RECONCILE_ID")
		if !ok || raw == "" || strings.TrimSpace(raw) != raw {
			return Runtime{}, errors.New("invalid AICRM_PAYMENT_RECONCILE_ID")
		}
		cfg.PaymentReconcileID, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || cfg.PaymentReconcileID < 1 {
			return Runtime{}, errors.New("invalid AICRM_PAYMENT_RECONCILE_ID")
		}
	}
	if strings.TrimSpace(cfg.DatabaseURL) != cfg.DatabaseURL || cfg.DatabaseURL == "" {
		return Runtime{}, errors.New("invalid database URL")
	}
	if !validPublicOrigin(cfg.PublicOrigin) {
		return Runtime{}, errors.New("invalid AICRM_PUBLIC_ORIGIN")
	}
	cfg.H5PublicOrigin = valueOrDefault("AICRM_H5_PUBLIC_ORIGIN", cfg.PublicOrigin)
	if !validPublicOrigin(cfg.H5PublicOrigin) {
		return Runtime{}, errors.New("invalid AICRM_H5_PUBLIC_ORIGIN")
	}
	if strings.TrimSpace(cfg.WorkerOwner) != cfg.WorkerOwner || cfg.WorkerOwner == "" || len(cfg.WorkerOwner) > 120 {
		return Runtime{}, errors.New("invalid AICRM_WORKER_OWNER")
	}
	if cfg.CustomerSyncTrigger != "" && cfg.CustomerSyncTrigger != "daily" && cfg.CustomerSyncTrigger != "initial" {
		return Runtime{}, errors.New("invalid AICRM_CUSTOMER_SYNC_TRIGGER")
	}
	if cfg.HXCDashboard.SyncTrigger != "" && cfg.HXCDashboard.SyncTrigger != "scheduled" && cfg.HXCDashboard.SyncTrigger != "initial" {
		return Runtime{}, errors.New("invalid AICRM_HXC_SYNC_TRIGGER")
	}
	if cfg.HXCDashboard.Enabled {
		if strings.TrimSpace(cfg.HXCDashboard.SourceDSN) != cfg.HXCDashboard.SourceDSN || cfg.HXCDashboard.SourceDSN == "" || !strings.HasPrefix(cfg.HXCDashboard.UnionIDScope, "wechat-open-platform:") || len(cfg.HXCDashboard.UnionIDScope) <= len("wechat-open-platform:") || strings.TrimSpace(cfg.HXCDashboard.UnionIDScope) != cfg.HXCDashboard.UnionIDScope || len(cfg.HXCDashboard.SubjectHMACKey) < 32 {
			return Runtime{}, errors.New("enabled HXC dashboard configuration is incomplete")
		}
	}
	if cfg.HXCDashboard.IdentityWriteEnabled && (!cfg.HXCDashboard.Enabled || cfg.HXCDashboard.IdentityObservationVaultKey == "") {
		return Runtime{}, errors.New("enabled HXC identity writes require sync and observation vault key")
	}
	if cfg.HXCDashboard.UnionIDVerified && !cfg.HXCDashboard.Enabled {
		return Runtime{}, errors.New("HXC UnionID verification requires HXC sync")
	}
	if cfg.HXCDashboard.IdentityObservationVaultKey != "" {
		if decoded, decodeErr := base64.StdEncoding.DecodeString(cfg.HXCDashboard.IdentityObservationVaultKey); decodeErr != nil || len(decoded) != 32 {
			return Runtime{}, errors.New("invalid AICRM_IDENTITY_OBSERVATION_VAULT_KEY")
		}
	}
	if cfg.OperationCycleServiceToken != "" && (strings.TrimSpace(cfg.OperationCycleServiceToken) != cfg.OperationCycleServiceToken || len(cfg.OperationCycleServiceToken) < 32) {
		return Runtime{}, errors.New("invalid AICRM_OPERATION_CYCLE_SERVICE_TOKEN")
	}
	bootstrapValues := []string{cfg.Bootstrap.Username, cfg.Bootstrap.Password, cfg.Bootstrap.DisplayName}
	bootstrapCount := nonEmptyCount(bootstrapValues)
	if bootstrapCount != 0 && bootstrapCount != len(bootstrapValues) {
		return Runtime{}, errors.New("bootstrap administrator configuration must be all-or-none")
	}
	cfg.Bootstrap.Enabled = bootstrapCount == len(bootstrapValues)
	if cfg.Bootstrap.Enabled && (strings.TrimSpace(cfg.Bootstrap.Username) != cfg.Bootstrap.Username ||
		strings.TrimSpace(cfg.Bootstrap.DisplayName) != cfg.Bootstrap.DisplayName || cfg.Bootstrap.Password == "") {
		return Runtime{}, errors.New("invalid bootstrap administrator configuration")
	}
	if cfg.WeCom.Enabled {
		values := []string{cfg.WeCom.CorpID, cfg.WeCom.AgentID, cfg.WeCom.Secret, cfg.WeCom.ContextSigningKey}
		if nonEmptyCount(values) != len(values) || len(cfg.WeCom.ContextSigningKey) < 32 {
			return Runtime{}, errors.New("enabled WeCom configuration is incomplete")
		}
		for _, value := range values {
			if strings.TrimSpace(value) != value {
				return Runtime{}, errors.New("invalid enabled WeCom configuration")
			}
		}
		if strings.TrimSpace(cfg.Alipay.ContentEncryptionKeyPath) != cfg.Alipay.ContentEncryptionKeyPath || strings.ContainsAny(cfg.Alipay.ContentEncryptionKeyPath, "\r\n\x00") {
			return Runtime{}, errors.New("invalid Alipay content-encryption key path")
		}
	}
	if cfg.WeCom.MaterialUploadTimeout < time.Second || cfg.WeCom.MaterialUploadTimeout > MaximumWeComMaterialUploadTimeout {
		return Runtime{}, errors.New("invalid WeCom material upload timeout")
	}
	if cfg.WeCom.CallbackEnabled {
		values := []string{cfg.WeCom.CorpID, cfg.WeCom.CallbackToken, cfg.WeCom.CallbackAESKey, cfg.WeCom.ChannelStateHMACKey}
		if nonEmptyCount(values) != len(values) || len(cfg.WeCom.ChannelStateHMACKey) < 32 {
			return Runtime{}, errors.New("enabled WeCom callback configuration is incomplete")
		}
		for _, value := range values {
			if strings.TrimSpace(value) != value {
				return Runtime{}, errors.New("invalid enabled WeCom callback configuration")
			}
		}
	}
	if cfg.WeCom.CustomerSyncEnabled && (!cfg.WeCom.Enabled || strings.TrimSpace(cfg.WeCom.ContactSecret) != cfg.WeCom.ContactSecret || cfg.WeCom.ContactSecret == "") {
		return Runtime{}, errors.New("enabled WeCom customer sync configuration is incomplete")
	}
	if cfg.WeCom.ContactDescriptionProviderEnabled && (!cfg.Effects.ProviderEnabled || !cfg.WeCom.Enabled || strings.TrimSpace(cfg.WeCom.ContactSecret) != cfg.WeCom.ContactSecret || cfg.WeCom.ContactSecret == "") {
		return Runtime{}, errors.New("enabled WeCom contact description provider requires External Effects, WeCom, and contact credentials")
	}
	if cfg.WeCom.MessageArchiveEnabled {
		values := []string{cfg.WeCom.CorpID, cfg.WeCom.MessageArchiveSecret, cfg.WeCom.MessageArchiveRunnerPath, cfg.WeCom.MessageArchiveLibraryPath}
		if !cfg.WeCom.CallbackEnabled || nonEmptyCount(values) != len(values) || len(cfg.WeCom.MessageArchivePrivateKeyPaths) == 0 || cfg.WeCom.MessageArchivePageLimit < 1 || cfg.WeCom.MessageArchivePageBudget < 1 {
			return Runtime{}, errors.New("enabled WeCom message archive configuration is incomplete")
		}
		for _, value := range values {
			if strings.TrimSpace(value) != value || strings.ContainsAny(value, "\r\n\x00") {
				return Runtime{}, errors.New("invalid enabled WeCom message archive configuration")
			}
		}
	}
	channelProviderEnabled := cfg.WeCom.ChannelProviderReadEnabled || cfg.WeCom.ChannelQRProviderEnabled || cfg.WeCom.ChannelMediaPrepProviderEnabled || cfg.WeCom.ChannelWelcomeProviderEnabled || cfg.WeCom.ChannelTagProviderEnabled || cfg.WeCom.CustomerTagProviderEnabled
	if channelProviderEnabled && (!cfg.Effects.ProviderEnabled || !cfg.WeCom.Enabled || strings.TrimSpace(cfg.WeCom.ContactSecret) != cfg.WeCom.ContactSecret || cfg.WeCom.ContactSecret == "") {
		return Runtime{}, errors.New("enabled channel provider capability requires External Effects, WeCom, and contact credentials")
	}
	if cfg.GroupOps.ProviderReadEnabled && (!cfg.WeCom.Enabled || strings.TrimSpace(cfg.WeCom.ContactSecret) != cfg.WeCom.ContactSecret || cfg.WeCom.ContactSecret == "") {
		return Runtime{}, errors.New("enabled Group Ops provider read requires WeCom and contact credentials")
	}
	if cfg.GroupOps.ProviderEnabled && !cfg.Effects.ProviderEnabled {
		return Runtime{}, errors.New("enabled Group Ops provider requires External Effects")
	}
	if (cfg.WeCom.ChannelQRProviderEnabled || cfg.WeCom.ChannelWelcomeProviderEnabled || cfg.WeCom.ChannelTagProviderEnabled) && !cfg.WeCom.CallbackEnabled {
		return Runtime{}, errors.New("enabled channel QR/welcome/tag capability requires verified WeCom callback")
	}
	if cfg.TagCatalog.Enabled {
		if !cfg.WeCom.Enabled || cfg.TagCatalog.Permission != "catalog-read-authorized" {
			return Runtime{}, errors.New("enabled tag catalog provider requires WeCom and explicit permission")
		}
	}
	if cfg.TagCatalog.MutationEnabled {
		if !cfg.TagCatalog.Enabled || !cfg.Effects.ProviderEnabled || !cfg.WeCom.Enabled || strings.TrimSpace(cfg.WeCom.ContactSecret) != cfg.WeCom.ContactSecret || cfg.WeCom.ContactSecret == "" || cfg.TagCatalog.MutationPermission != "catalog-write-authorized" {
			return Runtime{}, errors.New("enabled tag catalog mutation provider requires catalog read, External Effects, WeCom contact credentials, and explicit write permission")
		}
	}
	if cfg.AIAssistant.IntakeEnabled && (strings.TrimSpace(cfg.AIAssistant.IntegrationKey) != cfg.AIAssistant.IntegrationKey || cfg.AIAssistant.IntegrationKey == "" || len(cfg.AIAssistant.IntegrationSecret) < 32 || cfg.AIAssistant.IntegrationActorID < 1) {
		return Runtime{}, errors.New("enabled AI Assistant intake configuration is incomplete")
	}
	if cfg.AIAssistant.DispatchEnabled && (!cfg.Effects.ProviderEnabled || !cfg.WeCom.Enabled || cfg.WeCom.ContactSecret == "" || cfg.AIAssistant.ProviderPermission != "private-message-authorized") {
		return Runtime{}, errors.New("enabled AI Assistant dispatch requires External Effects, WeCom contact credentials, and explicit permission")
	}
	if cfg.AIGeneration.Enabled {
		if !cfg.Effects.ProviderEnabled || !cfg.AIGeneration.Ready() {
			return Runtime{}, errors.New("enabled AI generation configuration is incomplete")
		}
	}
	if cfg.WeChatPay.Enabled {
		values := []string{cfg.WeChatPay.AppID, cfg.WeChatPay.AppSecret, cfg.WeChatPay.AppScope, cfg.WeChatPay.MerchantID, cfg.WeChatPay.MerchantSerial, cfg.WeChatPay.PrivateKeyPath, cfg.WeChatPay.PlatformCertPath, cfg.WeChatPay.APIV3Key}
		if nonEmptyCount(values) != len(values) || len(cfg.WeChatPay.APIV3Key) != 32 || !strings.HasPrefix(cfg.WeChatPay.AppScope, "wechat-app:") {
			return Runtime{}, errors.New("enabled WeChat Pay configuration is incomplete")
		}
		for _, value := range values {
			if strings.TrimSpace(value) != value {
				return Runtime{}, errors.New("invalid enabled WeChat Pay configuration")
			}
		}
	}
	if cfg.Alipay.Enabled {
		values := []string{cfg.Alipay.AppID, cfg.Alipay.PrivateKeyPath, cfg.Alipay.Gateway, cfg.Alipay.NotifyURL, cfg.Alipay.ReturnURL}
		if nonEmptyCount(values) != len(values) || !validHTTPSURL(cfg.Alipay.Gateway) || !validHTTPSURL(cfg.Alipay.NotifyURL) || !validHTTPSURL(cfg.Alipay.ReturnURL) {
			return Runtime{}, errors.New("enabled Alipay configuration is incomplete")
		}
		for _, value := range values {
			if strings.TrimSpace(value) != value || strings.ContainsAny(value, "\r\n\x00") {
				return Runtime{}, errors.New("invalid enabled Alipay configuration")
			}
		}
		certMode := cfg.Alipay.AppCertPath != "" || cfg.Alipay.AlipayCertPath != "" || cfg.Alipay.AlipayRootPath != ""
		if certMode {
			if cfg.Alipay.AppCertPath == "" || cfg.Alipay.AlipayCertPath == "" || cfg.Alipay.AlipayRootPath == "" || cfg.Alipay.AlipayPublicKey != "" {
				return Runtime{}, errors.New("Alipay certificate mode requires all three certificates and no public key")
			}
		} else if strings.TrimSpace(cfg.Alipay.AlipayPublicKey) == "" {
			return Runtime{}, errors.New("Alipay public-key mode requires ALIPAY_PUBLIC_KEY")
		}
	}
	if cfg.WeChatPay.H5OAuthEnabled {
		values := []string{cfg.WeChatPay.H5AppID, cfg.WeChatPay.H5AppSecret, cfg.WeChatPay.H5AppScope, cfg.WeChatPay.OrderContactDataKey}
		if !cfg.WeChatPay.Enabled || nonEmptyCount(values) != len(values) || cfg.WeChatPay.H5AppScope != "wechat-app:"+cfg.WeChatPay.H5AppID {
			return Runtime{}, errors.New("enabled WeChat Pay H5 OAuth configuration is incomplete")
		}
		for _, value := range values[:3] {
			if strings.TrimSpace(value) != value || strings.ContainsAny(value, "\r\n\x00") {
				return Runtime{}, errors.New("invalid enabled WeChat Pay H5 OAuth configuration")
			}
		}
		if decoded, decodeErr := base64.RawStdEncoding.DecodeString(cfg.WeChatPay.OrderContactDataKey); decodeErr != nil || len(decoded) != 32 {
			return Runtime{}, errors.New("invalid AICRM_ORDER_CONTACT_DATA_KEY")
		}
	}
	if cfg.WeChatPay.ProfitSharingEnabled {
		if !cfg.WeChatPay.Enabled || !cfg.Effects.ProviderEnabled {
			return Runtime{}, errors.New("enabled WeChat Pay profit sharing requires payment and External Effects")
		}
		switch cfg.WeChatPay.ProfitSharingAuthMode {
		case "certificate":
			if cfg.WeChatPay.ProfitSharingPublicKeyID != "" {
				return Runtime{}, errors.New("certificate profit sharing authentication must not include a public-key id")
			}
		case "public_key":
			if strings.TrimSpace(cfg.WeChatPay.ProfitSharingPublicKeyID) != cfg.WeChatPay.ProfitSharingPublicKeyID || cfg.WeChatPay.ProfitSharingPublicKeyID == "" {
				return Runtime{}, errors.New("public-key profit sharing authentication requires an explicit verified public-key id")
			}
		default:
			return Runtime{}, errors.New("enabled WeChat Pay profit sharing requires explicit authentication mode")
		}
	}
	if cfg.WeChatShop.Enabled {
		values := []string{cfg.WeChatShop.AppID, cfg.WeChatShop.AppSecret, cfg.WeChatShop.CallbackToken, cfg.WeChatShop.CallbackEncodingAESKey}
		if nonEmptyCount(values) != len(values) || len(cfg.WeChatShop.CallbackEncodingAESKey) != 43 {
			return Runtime{}, errors.New("enabled WeChat Shop configuration is incomplete")
		}
		for _, value := range values {
			if strings.TrimSpace(value) != value {
				return Runtime{}, errors.New("invalid enabled WeChat Shop configuration")
			}
		}
	}
	if cfg.GroupOps.WebhookSecret != "" && (strings.TrimSpace(cfg.GroupOps.WebhookSecret) != cfg.GroupOps.WebhookSecret || len(cfg.GroupOps.WebhookSecret) < 32 || len(cfg.GroupOps.WebhookSecret) > 4096) {
		return Runtime{}, errors.New("invalid Group Ops webhook secret")
	}
	if cfg.AutomationOperations.WebhookSecret != "" && (strings.TrimSpace(cfg.AutomationOperations.WebhookSecret) != cfg.AutomationOperations.WebhookSecret || len(cfg.AutomationOperations.WebhookSecret) < 32 || len(cfg.AutomationOperations.WebhookSecret) > 4096) {
		return Runtime{}, errors.New("invalid Automation Operations webhook secret")
	}
	if cfg.AutomationOperations.AudiencePushWebhookSecret != "" && (strings.TrimSpace(cfg.AutomationOperations.AudiencePushWebhookSecret) != cfg.AutomationOperations.AudiencePushWebhookSecret || len(cfg.AutomationOperations.AudiencePushWebhookSecret) < 32 || len(cfg.AutomationOperations.AudiencePushWebhookSecret) > 4096) {
		return Runtime{}, errors.New("invalid AICRM_AUDIENCE_PUSH_WEBHOOK_SECRET")
	}
	switch cfg.AutomationOperations.ProviderMode {
	case AutomationProviderDisabled:
		// A stale permission value is harmless while disabled. The actual writer
		// remains closed and the execution precheck reports provider_disabled.
	case AutomationProviderProbe:
		if !cfg.Effects.ProviderEnabled || !cfg.WeCom.Enabled || cfg.AutomationOperations.ProviderPermission != "fixed-script-send-authorized" || cfg.AutomationOperations.MaxRecipientsPerRun != 1 {
			return Runtime{}, errors.New("Automation Operations probe requires External Effects, WeCom, explicit permission, and a one-recipient limit")
		}
	case AutomationProviderLimited:
		if !cfg.Effects.ProviderEnabled || !cfg.WeCom.Enabled || cfg.AutomationOperations.ProviderPermission != "fixed-script-send-authorized" {
			return Runtime{}, errors.New("Automation Operations limited provider requires External Effects, WeCom, and explicit permission")
		}
	default:
		return Runtime{}, errors.New("invalid AICRM_AUTOMATION_OPS_PROVIDER_MODE")
	}
	if cfg.Survey.DataKey != "" {
		if decoded, decodeErr := base64.RawStdEncoding.DecodeString(cfg.Survey.DataKey); decodeErr != nil || len(decoded) != 32 {
			return Runtime{}, errors.New("invalid AICRM_SURVEY_DATA_KEY")
		}
	}
	if cfg.Referral.TokenDataKey != "" {
		if decoded, decodeErr := base64.RawStdEncoding.DecodeString(cfg.Referral.TokenDataKey); decodeErr != nil || len(decoded) != 32 {
			return Runtime{}, errors.New("invalid AICRM_REFERRAL_TOKEN_DATA_KEY")
		}
	}
	if cfg.OpenPlatform.JWTSigningKey != "" && (len(cfg.OpenPlatform.JWTSigningKey) < 32 || strings.TrimSpace(cfg.OpenPlatform.JWTSigningKey) != cfg.OpenPlatform.JWTSigningKey || strings.ContainsAny(cfg.OpenPlatform.JWTSigningKey, "\r\n\x00")) {
		return Runtime{}, errors.New("invalid AICRM_OPEN_PLATFORM_JWT_SIGNING_KEY")
	}
	for _, raw := range cfg.OpenPlatform.TrustedProxyCIDRs {
		if _, _, parseErr := net.ParseCIDR(raw); parseErr != nil {
			return Runtime{}, errors.New("invalid AICRM_OPEN_PLATFORM_TRUSTED_PROXY_CIDRS")
		}
	}
	if cfg.Survey.IdentityPhoneDataKey != "" {
		if decoded, decodeErr := base64.RawStdEncoding.DecodeString(cfg.Survey.IdentityPhoneDataKey); decodeErr != nil || len(decoded) != 32 {
			return Runtime{}, errors.New("invalid AICRM_IDENTITY_PHONE_DATA_KEY")
		}
	}
	if cfg.Survey.OAuthEnabled {
		values := []string{cfg.Survey.OAuthAppID, cfg.Survey.OAuthSecret, cfg.Survey.OAuthOpenPlatformID}
		if nonEmptyCount(values) != len(values) {
			return Runtime{}, errors.New("enabled survey OAuth configuration is incomplete")
		}
		for _, value := range values {
			if strings.TrimSpace(value) != value || strings.ContainsAny(value, "\r\n\x00") {
				return Runtime{}, errors.New("invalid enabled survey OAuth configuration")
			}
		}
		if cfg.Survey.OAuthScope != "snsapi_userinfo" {
			return Runtime{}, errors.New("survey OAuth scope must be snsapi_userinfo")
		}
	}
	if cfg.Survey.CompletionProviderEnabled && (!cfg.Effects.ProviderEnabled || strings.TrimSpace(cfg.Survey.CompletionTargetsJSON) != cfg.Survey.CompletionTargetsJSON || cfg.Survey.CompletionTargetsJSON == "") {
		return Runtime{}, errors.New("enabled survey completion provider requires External Effects and target configuration")
	}
	if _, parseErr := ParseSurveyCompletionNavigationTargets(cfg.Survey.CompletionNavigationTargetsJSON); parseErr != nil {
		return Runtime{}, fmt.Errorf("invalid AICRM_SURVEY_COMPLETION_NAVIGATION_TARGETS_JSON: %w", parseErr)
	}
	if cfg.CommercePush.PayloadDataKey != "" {
		if decoded, decodeErr := base64.RawStdEncoding.DecodeString(cfg.CommercePush.PayloadDataKey); decodeErr != nil || len(decoded) != 32 {
			return Runtime{}, errors.New("invalid AICRM_COMMERCE_PUSH_PAYLOAD_DATA_KEY")
		}
	}
	if cfg.CommercePush.ProviderEnabled && (!cfg.Effects.ProviderEnabled || strings.TrimSpace(cfg.CommercePush.TargetsJSON) != cfg.CommercePush.TargetsJSON || cfg.CommercePush.TargetsJSON == "" || cfg.CommercePush.PayloadDataKey == "") {
		return Runtime{}, errors.New("enabled commerce push provider requires External Effects, target configuration, and payload key")
	}
	return cfg, nil
}

func splitCommaSeparated(key string) []string {
	raw := os.Getenv(key)
	if raw == "" {
		return []string{}
	}
	values := strings.Split(raw, ",")
	for index := range values {
		values[index] = strings.TrimSpace(values[index])
	}
	return values
}

// IdentityPhoneDataKey returns the dedicated phone-vault master key without
// forcing maintenance commands to load unrelated runtime/provider settings.
func IdentityPhoneDataKey() (string, error) {
	key := os.Getenv("AICRM_IDENTITY_PHONE_DATA_KEY")
	decoded, err := base64.RawStdEncoding.DecodeString(key)
	if err != nil || len(decoded) != 32 {
		return "", errors.New("invalid AICRM_IDENTITY_PHONE_DATA_KEY")
	}
	return key, nil
}

func strictBool(key string, fallback bool) (bool, error) {
	value, ok := os.LookupEnv(key)
	if !ok || value == "" {
		return fallback, nil
	}
	if value == "true" {
		return true, nil
	}
	if value == "false" {
		return false, nil
	}
	return false, errors.New("invalid " + key)
}

func validPublicOrigin(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.Path == "" && parsed.RawQuery == "" && parsed.Fragment == ""
}

func validHTTPSURL(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == ""
}

func validAIGenerationBaseURL(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.RawQuery == "" && parsed.Fragment == "" && parsed.User == nil
}

func nonEmptyCount(values []string) int {
	count := 0
	for _, value := range values {
		if value != "" {
			count++
		}
	}
	return count
}

func valueOrDefault(key, fallback string) string {
	value, ok := os.LookupEnv(key)
	if !ok || value == "" {
		return fallback
	}
	return value
}

// DatabaseURL returns the PostgreSQL connection URL for database-aware
// commands. AICRM_DATABASE_URL is the canonical runtime name; DATABASE_URL is
// accepted for local tools and integration-test environments.
func DatabaseURL() (string, error) {
	for _, key := range []string{"AICRM_DATABASE_URL", "DATABASE_URL"} {
		if value, ok := os.LookupEnv(key); ok && value != "" {
			if strings.TrimSpace(value) != value {
				return "", errors.New("invalid database URL")
			}
			return value, nil
		}
	}
	return "", errors.New("database URL is not configured")
}

// ReferralTokenDataKey is used by the bounded Referral sales backfill
// composition. The command never issues tokens, but Service construction
// requires the same configured key as the live application.
func ReferralTokenDataKey() (string, error) {
	value := os.Getenv("AICRM_REFERRAL_TOKEN_DATA_KEY")
	decoded, err := base64.RawStdEncoding.DecodeString(value)
	if err != nil || len(decoded) != 32 {
		return "", errors.New("invalid AICRM_REFERRAL_TOKEN_DATA_KEY")
	}
	return value, nil
}

// ChromiumJourneyRequired is the configuration boundary for opt-in local
// Chromium journeys. CI sets it explicitly for required browser acceptance.
func ChromiumJourneyRequired() bool {
	value, ok := os.LookupEnv("AICRM_REQUIRE_CHROMIUM_JOURNEY")
	return ok && value == "1"
}

// ProductExternalPushDarwinChromiumDiagnosticAllowed permits an explicitly
// requested developer diagnostic run of the Product external-push Chromium
// journey on Darwin. Linux CI remains the required release browser gate.
// This is test-only configuration; application runtime behavior never reads it.
func ProductExternalPushDarwinChromiumDiagnosticAllowed() bool {
	value, ok := os.LookupEnv("AICRM_PRODUCT_PUSH_ALLOW_DARWIN_CHROMIUM")
	return ok && value == "1"
}

// AdminLayoutScreenshotDirectory returns an explicitly configured CI or local
// evidence directory for the admin-shell Chromium layout journey. It is not a
// runtime setting and the caller still validates that any supplied path is
// absolute before writing screenshots.
func AdminLayoutScreenshotDirectory() string {
	return os.Getenv("AICRM_ADMIN_LAYOUT_SCREENSHOT_DIR")
}

// ChannelCenterScreenshotDirectory returns the optional evidence directory for
// the authenticated Channel Center Chromium journey. It is test-only; callers
// validate an absolute path before writing browser evidence.
func ChannelCenterScreenshotDirectory() string {
	return os.Getenv("AICRM_CHANNEL_CENTER_SCREENSHOT_DIR")
}

// AccessGovernanceScreenshotDirectory returns the optional evidence directory
// for the Access governance Chromium journey. The test validates filesystem
// constraints before it writes the rendered screenshots.
func AccessGovernanceScreenshotDirectory() string {
	return os.Getenv("AICRM_ACCESS_UI_SCREENSHOT_DIR")
}

// TagPickerScreenshotDirectory returns the optional evidence directory for the
// V3 tag picker Chromium journey. The test validates filesystem constraints
// before it writes rendered screenshots.
func TagPickerScreenshotDirectory() string {
	return os.Getenv("AICRM_TAG_PICKER_SCREENSHOT_DIR")
}

// ComponentStatesScreenshotDirectory returns the optional evidence directory
// for the authenticated component-state Chromium journey. The test validates
// that a supplied path is absolute before it writes its local screenshots.
func ComponentStatesScreenshotDirectory() string {
	return os.Getenv("AICRM_COMPONENT_STATES_SCREENSHOT_DIR")
}

// AutomationFixedContentScreenshotDirectory returns the optional evidence
// directory for the fixed-script content Chromium journey. The test validates
// that a supplied path is absolute before it writes rendered screenshots.
func AutomationFixedContentScreenshotDirectory() string {
	return os.Getenv("AICRM_AUTOMATION_CONTENT_SCREENSHOT_DIR")
}

// AudienceConfirmationScreenshotDirectory returns the optional evidence
// directory for the Audience confirmation Chromium journey. The test validates
// filesystem constraints before it writes rendered screenshots.
func AudienceConfirmationScreenshotDirectory() string {
	return os.Getenv("AICRM_AUDIENCE_CONFIRMATION_SCREENSHOT_DIR")
}

// PublicCommerceScreenshotDirectory returns the optional evidence directory
// for the public product and payment Chromium journey. The test validates that
// a supplied path is absolute before it writes local screenshots.
func PublicCommerceScreenshotDirectory() string {
	return os.Getenv("AICRM_PUBLIC_COMMERCE_SCREENSHOT_DIR")
}

// PublicSurveyScreenshotDirectory returns the optional evidence directory for
// the public Survey Chromium journey. The test validates that a supplied path
// is absolute before it writes local screenshots.
func PublicSurveyScreenshotDirectory() string {
	return os.Getenv("AICRM_PUBLIC_SURVEY_SCREENSHOT_DIR")
}

// QuestionnaireListScreenshotDirectory returns the optional evidence directory
// for the authenticated questionnaire list Chromium journey.
func QuestionnaireListScreenshotDirectory() string {
	return os.Getenv("AICRM_QUESTIONNAIRE_LIST_SCREENSHOT_DIR")
}

// RemainingPagesChromiumScreenshotDirectory returns the optional evidence
// directory for the final admin/public page Chromium acceptance journey.
// Callers still require an absolute path before writing local evidence.
func RemainingPagesChromiumScreenshotDirectory() string {
	return os.Getenv("AICRM_REMAINING_PAGES_CHROMIUM_SCREENSHOT_DIR")
}

// NamedDatabaseURL is restricted to explicit target and read-only source roles
// used by controlled offline migrations. Keeping this allowlist in the
// configuration package prevents commands from treating arbitrary environment
// values as credentials.
func NamedDatabaseURL(name string) (string, error) {
	switch name {
	case "AICRM_DATABASE_URL", "AICRM_V2_AUTOMATION_DATABASE_URL", "AICRM_AUDIENCE_SOURCE_DATABASE_URL":
	default:
		return "", errors.New("unsupported database URL environment")
	}
	value, ok := os.LookupEnv(name)
	if !ok || value == "" {
		return "", fmt.Errorf("database URL environment %s is not set", name)
	}
	if strings.TrimSpace(value) != value {
		return "", errors.New("invalid database URL")
	}
	return value, nil
}

// SourceDatabaseURL is available only to explicit offline migration commands.
// It is deliberately separate from the authoritative v3 runtime database.
func SourceDatabaseURL() (string, error) {
	value, ok := os.LookupEnv("AICRM_SOURCE_DATABASE_URL")
	if !ok || value == "" || strings.TrimSpace(value) != value {
		return "", errors.New("source database URL is not configured")
	}
	return value, nil
}
