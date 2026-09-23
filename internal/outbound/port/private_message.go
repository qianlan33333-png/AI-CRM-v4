// Package port contains stable Outbound contracts. Business domains submit
// immutable intents here and never call a WeCom provider directly.
package port

import (
	"context"
	"errors"
	"strings"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
)

var ErrInvalidPrivateMessageIntent = errors.New("invalid private message intent")

type PrivateMessageIntentCommand struct {
	DeferredTargetReference string
	SourceReference         string
	CustomerID              customerdomain.CustomerID
	StaffID                 int64
	PayloadReference        string
	SourceDigest            effectport.Digest
	TargetDigest            effectport.Digest
	PayloadDigest           effectport.Digest
	PolicyHash              effectport.Digest
	ReceiptKey              effectport.Digest
	Lane                    effectport.Lane
}

func (c PrivateMessageIntentCommand) Valid() bool {
	return strings.TrimSpace(c.SourceReference) != "" && len(c.SourceReference) <= 200 &&
		((c.CustomerID > 0 && c.StaffID > 0 && c.DeferredTargetReference == "") || (c.CustomerID == 0 && c.StaffID == 0 && c.DeferredTargetReference == c.PayloadReference && strings.HasPrefix(c.DeferredTargetReference, "aiassistant:"))) && strings.TrimSpace(c.PayloadReference) != "" && len(c.PayloadReference) <= 200 &&
		effectport.ValidDigest(c.SourceDigest) && effectport.ValidDigest(c.TargetDigest) &&
		effectport.ValidDigest(c.PayloadDigest) && effectport.ValidDigest(c.PolicyHash) && effectport.ValidDigest(c.ReceiptKey) && (c.Lane == "" || c.Lane == effectport.LaneOutboundExcel)
}

type PrivateMessageIntentResult struct {
	IntentID int64
	EffectID string
	Replayed bool
}

// PrivateMessageIntentWriter must participate in the caller's PostgreSQL UoW.
// Implementations persist the Outbound-owned payload reference and call the
// External Effects TransactionalAccepter without starting another transaction.
type PrivateMessageIntentWriter interface {
	WritePrivateMessageIntentWithin(context.Context, PrivateMessageIntentCommand) (PrivateMessageIntentResult, error)
}

// Provider-side contracts keep the WeCom adapter behind the stable Outbound
// boundary. Raw channel identifiers exist only in memory after effect
// acceptance and are never part of an AI Assistant DTO or table.
type PrivateMessageIntent struct {
	DeferredTargetReference string
	CustomerID              customerdomain.CustomerID
	StaffID                 int64
	PayloadReference        string
	PayloadDigest           effectport.Digest
}
type PrivateMessageTarget struct{ ExternalUserID, StaffUserID string }
type PrivateMessageAttachment struct {
	Kind                                                                           string
	Content                                                                        []byte
	FileName, MediaType, MediaID, AppID, PagePath, Title, URL, Description, PicURL string
}
type PrivateMessageMediaPreflighter interface {
	PreparePrivateMessageMedia(context.Context, string, effectport.Digest) error
}
type PrivateMessagePayload struct {
	Text        string
	Attachments []PrivateMessageAttachment
}
type PrivateMessageProviderReceipt struct{ MessageID string }

type PrivateMessageIntentReader interface {
	PrivateMessageIntentForEnvelope(context.Context, effectport.Envelope) (PrivateMessageIntent, error)
}
type PrivateMessageTargetResolver interface {
	ResolvePrivateMessageTarget(context.Context, customerdomain.CustomerID, int64) (PrivateMessageTarget, error)
}
type PrivateMessagePayloadReader interface {
	LoadPrivateMessagePayload(context.Context, string, effectport.Digest) (PrivateMessagePayload, error)
}
type PrivateMessageSender interface {
	SendPrivateMessage(context.Context, PrivateMessageTarget, PrivateMessagePayload) (PrivateMessageProviderReceipt, bool, error)
}
type PrivateMessageSendError interface {
	error
	OutcomeUnknown() bool
}

// PrivateMessageRetryableRejection is implemented only for a completed
// Provider response that proves no message task was created and explicitly
// asks the caller to retry (for example WeCom errcode 45009). Transport
// ambiguity must continue to use OutcomeUnknown instead.
type PrivateMessageRetryableRejection interface {
	PrivateMessageSendError
	Retryable() bool
	FailureCode() string
}

// DeferredTargetResolver is only used for a persisted, reviewed import reference.
type DeferredTargetResolver interface {
	ResolveDeferredPrivateMessageTarget(context.Context, string) (PrivateMessageTarget, error)
}
type PrivateMessageReceiptRecorder interface {
	RecordPrivateMessageReceipt(context.Context, string, PrivateMessageTarget, string, string) error
}

type PrivateMessageDelivery struct {
	MessageID      string     `json:"-"`
	SenderUserID   string     `json:"-"`
	ExternalUserID string     `json:"-"`
	Reason         string     `json:"reason"`
	Status         *int       `json:"status"`
	SentAt         *time.Time `json:"sent_at"`
	ObservedAt     time.Time  `json:"observed_at"`
}
type PrivateMessageDeliveryStore interface {
	PrivateMessageReceipt(context.Context, string) (PrivateMessageDelivery, bool, error)
	SavePrivateMessageDelivery(context.Context, string, PrivateMessageDelivery) error
}
type PrivateMessageDeliveryPage struct {
	Items      []PrivateMessageDelivery
	NextCursor string
}
type PrivateMessageDeliveryReader interface {
	GetPrivateMessageSendResult(context.Context, string, string, string) (PrivateMessageDeliveryPage, error)
}

// TargetResolutionError exposes only a closed failure category, never an ID.
type TargetResolutionError string

func (e TargetResolutionError) Error() string { return "deferred target unavailable" }
func (e TargetResolutionError) FailureCode() string {
	switch string(e) {
	case "unionid_not_unique", "unionid_unverified", "wecom_identity_unavailable":
		return string(e)
	}
	return "target_unavailable"
}

// PayloadPreparationError exposes a closed category without message content.
type PayloadPreparationError string

func (e PayloadPreparationError) Error() string { return "message content unavailable" }
func (e PayloadPreparationError) FailureCode() string {
	switch string(e) {
	case "title_missing", "cover_missing":
		return string(e)
	}
	return "payload_unavailable"
}
