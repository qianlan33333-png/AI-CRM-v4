package port

import (
	"context"
	"encoding/json"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
)

const MaxDirectPushItems = 1000

type DirectPushInput struct {
	UnionID         string `json:"unionid"`
	Text            string `json:"text"`
	MiniProgramID   int64  `json:"miniprogram_id"`
	SenderUserID    string `json:"sender_userid"`
	ClientReference string `json:"client_reference,omitempty"`
}

type DirectPushCommand struct {
	WebhookReference string
	IdempotencyKey   string
	EventIDDigest    [32]byte
	PayloadDigest    [32]byte
	Items            []DirectPushInput
	AcceptedAt       time.Time
}

type DirectPushItemResult struct {
	Index           int    `json:"index"`
	ClientReference string `json:"client_reference,omitempty"`
	PushID          string `json:"push_id,omitempty"`
	State           string `json:"state"`
	Code            string `json:"code,omitempty"`
}

type DirectPushBatchResult struct {
	BatchID       string                 `json:"batch_id"`
	AcceptedCount int                    `json:"accepted_count"`
	RejectedCount int                    `json:"rejected_count"`
	Items         []DirectPushItemResult `json:"items"`
	Replayed      bool                   `json:"-"`
}

type DirectPushStatus struct {
	PushID            string     `json:"push_id"`
	SendState         string     `json:"send_state"`
	ObservationState  string     `json:"observation_state"`
	FailureCode       string     `json:"failure_code,omitempty"`
	ObservationReason string     `json:"observation_reason,omitempty"`
	SentAt            *time.Time `json:"sent_at,omitempty"`
	ObservationDueAt  *time.Time `json:"observation_due_at,omitempty"`
	ObservedAt        *time.Time `json:"observed_at,omitempty"`
}

type DirectPushDeliveryEvidence struct {
	Ready       bool
	Delivered   bool
	Failed      bool
	SentAt      *time.Time
	FailureCode string
}

type DirectPushDeliveryReconciler interface {
	ReconcileDirectPushDelivery(context.Context, string) (DirectPushDeliveryEvidence, error)
}

type DirectPushOpenResult struct {
	State    string
	Reason   string
	OpenedAt *time.Time
}

type DirectPushOpenReader interface {
	ObserveDirectPushOpen(context.Context, customerdomain.CustomerID, json.RawMessage, time.Time, time.Time) (DirectPushOpenResult, error)
}

type DirectPushResolvedTarget struct {
	CustomerID customerdomain.CustomerID
	IdentityID int64
	StaffID    int64
}

type DirectPushTargetResolver interface {
	ResolveDirectPushTarget(context.Context, string, string) (DirectPushResolvedTarget, string, error)
}

type DirectPushEligibility struct {
	SnapshotID       int64
	SenderSetVersion int64
}

type DirectPushEligibilityReader interface {
	DirectPushEligibility(context.Context, int64, customerdomain.CustomerID, int64) (DirectPushEligibility, string, error)
	DirectPushPackageExists(context.Context, int64) (bool, error)
}

type DirectPushContentFreezer interface {
	FreezeDirectPushContent(context.Context, string, int64) (json.RawMessage, [32]byte, error)
}

type DirectPushService interface {
	AcceptDirectPush(context.Context, DirectPushCommand) (DirectPushBatchResult, error)
	DirectPushStatuses(context.Context, string, []string) ([]DirectPushStatus, error)
}

type DirectPushConfigView struct {
	PackageID         int64  `json:"package_id"`
	WebhookReference  string `json:"webhook_key"`
	WebhookPath       string `json:"webhook_path"`
	ClientID          string `json:"client_id"`
	Enabled           bool   `json:"enabled"`
	MaxPerCustomer24h int    `json:"max_per_customer_24h"`
	Version           int64  `json:"version"`
}

type DirectPushConfigCommand struct {
	PackageID         int64
	Enabled           bool
	MaxPerCustomer24h int
	ExpectedVersion   int64
	Actor             int64
	IdempotencyKey    string
}

type DirectPushAdminRecord struct {
	PushID             string     `json:"push_id"`
	BatchID            string     `json:"batch_id"`
	ClientReference    string     `json:"client_reference,omitempty"`
	SenderStaffID      int64      `json:"sender_staff_id"`
	MiniProgramID      int64      `json:"miniprogram_id"`
	MiniProgramName    string     `json:"miniprogram_name,omitempty"`
	MiniProgramVersion int64      `json:"miniprogram_version,omitempty"`
	SendState          string     `json:"send_state"`
	ObservationState   string     `json:"observation_state"`
	FailureCode        string     `json:"failure_code,omitempty"`
	ObservationReason  string     `json:"observation_reason,omitempty"`
	SentAt             *time.Time `json:"sent_at,omitempty"`
	ObservationDueAt   *time.Time `json:"observation_due_at,omitempty"`
	ObservedAt         *time.Time `json:"observed_at,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
}

type DirectPushAdminService interface {
	DirectPushConfig(context.Context, int64) (DirectPushConfigView, error)
	ConfigureDirectPush(context.Context, DirectPushConfigCommand) (DirectPushConfigView, error)
	DirectPushAdminRecords(context.Context, int64, int) ([]DirectPushAdminRecord, error)
}
