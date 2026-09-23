// Package port freezes the cross-domain non-secret settings contract.
package port

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// RequestReceipt is a Config-owned request-level idempotency fact. It carries
// digests only, never setting values or secret material.
type RequestReceipt struct {
	ID            int64
	PayloadDigest []byte
	State         string
}

type Key string

const (
	WeComCorpID                  Key = "wecom.corp_id"
	WeComAgentID                 Key = "wecom.agent_id"
	OutboundRatePerSecond        Key = "outbound.rate_per_second"
	OutboundMaxAttempts          Key = "outbound.max_attempts"
	AdminAppSettingsEnabled      Key = "adminops.app_settings.enabled"
	AdminPushCapabilitiesEnabled Key = "adminops.push_capabilities.enabled"
	AdminReleasesEnabled         Key = "adminops.releases.enabled"
	AdminDiagnosticsEnabled      Key = "adminops.diagnostics.enabled"

	DatabaseURL           Key = "database.url"
	WeComSecret           Key = "wecom.secret"
	WeComCallbackToken    Key = "wecom.callback_token"
	WeComCallbackAESKey   Key = "wecom.callback_aes_key"
	AIAPIKey              Key = "ai.api_key"
	AuthJWTSecret         Key = "auth.jwt_secret"
	ExtensionAPIKeyPepper Key = "extension.api_key_pepper"
	WebhookMasterKey      Key = "gateway.webhook_master_key"
)

var (
	ErrUnknownSetting      = errors.New("unknown setting key")
	ErrSecretSetting       = errors.New("secret setting forbidden")
	ErrInvalidSetting      = errors.New("invalid setting value")
	ErrSettingNotFound     = errors.New("setting not found")
	ErrIdempotencyConflict = errors.New("setting idempotency conflict")
)

type Setting struct {
	Key       Key
	Value     json.RawMessage
	UpdatedBy string
	UpdatedAt time.Time
}

// Audit is the local configuration mutation receipt used by the application
// layer. Persistence remains a Terra-owned concern; this value deliberately
// carries no database/generated types.
type Audit struct {
	ID        int64
	Key       Key
	OldValue  []byte
	NewValue  []byte
	UpdatedBy string
	RequestID string
	UpdatedAt time.Time
}

// ProjectionSetting and ProjectionAudit are read-only DTOs for the admin
// configuration page. They are intentionally defined at the port boundary so
// the compatibility projection does not import the donor store package.
type ProjectionSetting struct {
	Key            Key
	Value          []byte
	UpdatedAt      time.Time
	LastActionType string
	LastModifiedBy string
	LastModifiedAt *time.Time
}

type ProjectionAudit struct {
	ID         int64
	Operator   string
	ActionType string
	TargetID   Key
	CreatedAt  time.Time
}

// ReleaseProjection and DiagnosticProjection are the only historical facts
// that the Config HTTP compatibility surface may expose. Details are
// deliberately absent: AdminOps owns their source and must provide a
// redacted adapter before any data is mounted here.
type ReleaseProjection struct {
	ID         int64     `json:"id"`
	ReleaseSHA string    `json:"release_sha"`
	Status     string    `json:"status"`
	ObservedAt time.Time `json:"observed_at"`
}

type DiagnosticProjection struct {
	ID         int64     `json:"id"`
	Key        string    `json:"key"`
	Status     string    `json:"status"`
	ObservedAt time.Time `json:"observed_at"`
}

// SafeProjectionReader is a read-only, typed seam for AdminOps-owned
// historical projections. It cannot carry arbitrary JSON details across the
// Config boundary.
type SafeProjectionReader interface {
	ListReleaseProjections(context.Context) ([]ReleaseProjection, error)
	ListDiagnosticSnapshots(context.Context) ([]DiagnosticProjection, error)
}

// Event is a local transactional fact. It is a seam only: Terra must adapt it
// to the v3 versioned event/outbox implementation at composition time.
type Event struct {
	Type           string
	Payload        json.RawMessage
	OccurredAt     time.Time
	IdempotencyKey string
}

type EventID int64

// EventAppender must append only within the caller's UnitOfWork. It must not
// dispatch work or contact a Provider.
type EventAppender interface {
	Append(context.Context, Event) (EventID, error)
}

type SetCommand struct {
	Key       Key
	Value     json.RawMessage
	Actor     string
	RequestID string
}

type Service interface {
	Get(context.Context, Key) (Setting, error)
	Set(context.Context, SetCommand) (Setting, error)
}
