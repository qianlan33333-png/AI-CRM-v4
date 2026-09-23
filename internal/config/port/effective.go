package port

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// RuntimeSettingKey is a closed business-runtime setting catalog. Deployment,
// identity and secret inputs are deliberately absent.
type RuntimeSettingKey string

const (
	AutomationOperationsMaxRecipientsPerRun RuntimeSettingKey = "automation.operations.max_recipients_per_run"
	AutomationOperationsProviderMode        RuntimeSettingKey = "automation.operations.provider_mode"
	AIAssistantUIEnabled                    RuntimeSettingKey = "ai_assistant.ui_enabled"
	AIAssistantIntakeEnabled                RuntimeSettingKey = "ai_assistant.intake_enabled"
	AIAssistantDispatchEnabled              RuntimeSettingKey = "ai_assistant.dispatch_enabled"
	AIAgentGenerationEnabled                RuntimeSettingKey = "ai_agent_generation.enabled"
	WeComEnabled                            RuntimeSettingKey = "wecom.enabled"
	RuntimeWeComCorpID                      RuntimeSettingKey = "wecom.corp_id"
	RuntimeWeComAgentID                     RuntimeSettingKey = "wecom.agent_id"
	WeComCallbackEnabled                    RuntimeSettingKey = "wecom.callback_enabled"
	WeComCustomerSyncEnabled                RuntimeSettingKey = "wecom.customer_sync_enabled"
	MessageArchiveEnabled                   RuntimeSettingKey = "message_archive.enabled"
	MessageArchivePageLimit                 RuntimeSettingKey = "message_archive.page_limit"
	MessageArchivePageBudget                RuntimeSettingKey = "message_archive.page_budget"
	SidebarContextTokenTTLSeconds           RuntimeSettingKey = "sidebar.context_token_ttl_seconds"
	GroupOpsDirectoryReadEnabled            RuntimeSettingKey = "groupops.directory_read_enabled"
	GroupOpsDispatchEnabled                 RuntimeSettingKey = "groupops.dispatch_enabled"
	EffectsProviderEnabled                  RuntimeSettingKey = "effects.provider_enabled"
	SurveyCompletionProviderEnabled         RuntimeSettingKey = "survey.completion_provider_enabled"
	CommercePushProviderEnabled             RuntimeSettingKey = "commerce.push.provider_enabled"
	WorkerLimit                             RuntimeSettingKey = "stability.worker_limit"
	WeChatPayProviderEnabled                RuntimeSettingKey = "wechat_pay.provider_enabled"
	WeChatPayAppID                          RuntimeSettingKey = "wechat_pay.app_id"
	WeChatPayAppScope                       RuntimeSettingKey = "wechat_pay.app_scope"
	WeChatPayH5OAuthEnabled                 RuntimeSettingKey = "wechat_pay.h5_oauth_enabled"
	WeChatPayH5AppID                        RuntimeSettingKey = "wechat_pay.h5_app_id"
	WeChatPayH5AppScope                     RuntimeSettingKey = "wechat_pay.h5_app_scope"
	WeChatPayMerchantID                     RuntimeSettingKey = "wechat_pay.merchant_id"
	WeChatPayMerchantSerial                 RuntimeSettingKey = "wechat_pay.merchant_serial"
	WeChatShopProviderEnabled               RuntimeSettingKey = "wechat_shop.provider_enabled"
	WeChatShopAppID                         RuntimeSettingKey = "wechat_shop.app_id"
	AlipayProviderEnabled                   RuntimeSettingKey = "alipay.provider_enabled"
	AlipayAppID                             RuntimeSettingKey = "alipay.app_id"
	AlipayProduction                        RuntimeSettingKey = "alipay.production"
	SurveyOAuthEnabled                      RuntimeSettingKey = "survey.oauth_enabled"
	SurveyOAuthAppID                        RuntimeSettingKey = "survey.oauth_app_id"
	SurveyOAuthOpenPlatformID               RuntimeSettingKey = "survey.oauth_open_platform_id"
	SurveyOAuthScope                        RuntimeSettingKey = "survey.oauth_scope"
)

var (
	ErrRuntimeReleaseNotFound = errors.New("runtime configuration release not found")
	ErrRuntimeReleaseConflict = errors.New("runtime configuration release conflict")
	ErrRuntimeReleaseInvalid  = errors.New("invalid runtime configuration release")
)

type RuntimeSetting struct {
	Key   RuntimeSettingKey `json:"key"`
	Value json.RawMessage   `json:"value"`
}

type RuntimeSource string

const (
	RuntimeSourceEnvironmentDefault RuntimeSource = "environment_default"
	RuntimeSourcePublished          RuntimeSource = "published"
)

// EffectiveSnapshot is immutable once returned. Revision 0 identifies the
// process environment default; positive revisions are immutable Config releases.
type EffectiveSnapshot struct {
	Revision                int64         `json:"revision"`
	Source                  RuntimeSource `json:"source"`
	AutomationMaxRecipients int           `json:"automation_max_recipients_per_run"`
	PublishedAt             *time.Time    `json:"published_at,omitempty"`
	// Settings is the closed, non-secret effective configuration snapshot. It
	// is sorted by key so it can be safely reused as the base for a new draft.
	Settings []RuntimeSetting `json:"settings"`
	Checksum string           `json:"checksum"`
}

// RuntimeApplication is a role's append-only startup fact. A published
// revision is not represented as effective on a restart-required surface
// until the relevant process has observed it at composition time.
type RuntimeApplication struct {
	Revision         int64         `json:"revision"`
	Source           RuntimeSource `json:"source"`
	Role             string        `json:"role"`
	ReleaseSHA       string        `json:"release_sha"`
	SnapshotChecksum string        `json:"snapshot_checksum"`
	AppliedAt        time.Time     `json:"applied_at"`
}

// ProtectedReferenceStatus reports only whether a closed, deployment-owned secret or
// authorization reference was present when this role started. It never carries a
// secret value or permits callers to name an environment variable.
type ProtectedReferenceStatus struct {
	Reference  string `json:"reference"`
	Configured bool   `json:"configured"`
}

type EffectiveReader interface {
	// EffectiveSnapshot starts a short Config-owned read transaction for a
	// request boundary.
	EffectiveSnapshot(context.Context) (EffectiveSnapshot, error)
	// EffectiveSnapshotWithin uses the caller's already-open PostgreSQL UoW so
	// a consumer can freeze its business record and usage fact atomically.
	EffectiveSnapshotWithin(context.Context) (EffectiveSnapshot, error)
}

type RuntimeUsage struct {
	Snapshot    EffectiveSnapshot `json:"snapshot"`
	Consumer    string            `json:"consumer"`
	Role        string            `json:"role"`
	Operation   string            `json:"operation"`
	SubjectKind string            `json:"subject_kind"`
	SubjectID   int64             `json:"subject_id"`
	UsedAt      time.Time         `json:"used_at"`
}

// UsageRecorder records only an actual business boundary that consumed an
// immutable snapshot. It must participate in the caller's PostgreSQL UoW.
type UsageRecorder interface {
	RecordRuntimeUsage(context.Context, RuntimeUsage) error
}

type RuntimeReleaseReceipt struct {
	ID            int64
	PayloadDigest []byte
	ReleaseID     int64
	State         string
}

type RuntimeReleaseState string

const (
	RuntimeReleaseDraft            RuntimeReleaseState = "draft"
	RuntimeReleaseValidated        RuntimeReleaseState = "validated"
	RuntimeReleaseValidationFailed RuntimeReleaseState = "validation_failed"
	RuntimeReleasePublished        RuntimeReleaseState = "published"
	RuntimeReleaseSuperseded       RuntimeReleaseState = "superseded"
)

type RuntimeValidationIssue struct {
	Key   RuntimeSettingKey `json:"key"`
	Error string            `json:"error"`
}

type RuntimeRelease struct {
	ID                  int64                    `json:"id"`
	State               RuntimeReleaseState      `json:"state"`
	BaseRevision        int64                    `json:"base_revision"`
	RollbackOfReleaseID *int64                   `json:"rollback_of_release_id,omitempty"`
	Settings            []RuntimeSetting         `json:"settings"`
	Checksum            string                   `json:"checksum"`
	ValidationErrors    []RuntimeValidationIssue `json:"validation_errors"`
	CreatedBy           string                   `json:"created_by"`
	CreatedAt           time.Time                `json:"created_at"`
	ValidatedAt         *time.Time               `json:"validated_at,omitempty"`
	PublishedBy         string                   `json:"published_by,omitempty"`
	PublishedAt         *time.Time               `json:"published_at,omitempty"`
}

type RuntimeReleaseDraftCommand struct {
	ExpectedBaseRevision int64
	Settings             []RuntimeSetting
	Actor                string
	IdempotencyKey       string
}
type RuntimeReleaseMutationCommand struct {
	ReleaseID      int64
	Actor          string
	IdempotencyKey string
}
type RuntimeReleasePublishCommand struct {
	ReleaseID            int64
	ExpectedBaseRevision int64
	ExpectedChecksum     string
	Actor                string
	IdempotencyKey       string
}
type RuntimeReleaseRollbackCommand struct {
	ReleaseID            int64
	ExpectedBaseRevision int64
	Actor                string
	IdempotencyKey       string
}

// RuntimeReleaseLegacyRecoveryCommand prepares the one-setting snapshot read
// by the binary that predates the expanded Config catalog. It never writes a
// deployment value or arbitrary environment variable.
type RuntimeReleaseLegacyRecoveryCommand struct {
	ExpectedBaseRevision int64
	Actor                string
	IdempotencyKey       string
}

type RuntimeReleasePage struct {
	ActiveRevision int64             `json:"active_revision"`
	Effective      EffectiveSnapshot `json:"effective"`
	Releases       []RuntimeRelease  `json:"releases"`
}

type RuntimeReleaseApplication interface {
	ListRuntimeReleases(context.Context, int) (RuntimeReleasePage, error)
	RuntimeRelease(context.Context, int64) (RuntimeRelease, error)
	CreateRuntimeReleaseDraft(context.Context, RuntimeReleaseDraftCommand) (RuntimeRelease, error)
	ValidateRuntimeRelease(context.Context, RuntimeReleaseMutationCommand) (RuntimeRelease, error)
	PublishRuntimeRelease(context.Context, RuntimeReleasePublishCommand) (RuntimeRelease, error)
	RollbackRuntimeRelease(context.Context, RuntimeReleaseRollbackCommand) (RuntimeRelease, error)
	PrepareLegacyRuntimeRecovery(context.Context, RuntimeReleaseLegacyRecoveryCommand) (RuntimeRelease, error)
	ListRuntimeUsage(context.Context, int64, int) ([]RuntimeUsage, error)
	RecordRuntimeApplication(context.Context, RuntimeApplication) error
	ListRuntimeApplications(context.Context, int) ([]RuntimeApplication, error)
	ProtectedReferenceStatuses(context.Context) ([]ProtectedReferenceStatus, error)
}
