package port

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	channeldomain "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/domain"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
)

// ErrEntrantActionsSkippedInactiveChannel is a completed local-policy result,
// not a transient action failure. Channel returns it only after it has read an
// inactive or archived channel after it has verified the State's asset,
// configuration, and selectable assignee.
var ErrEntrantActionsSkippedInactiveChannel = errors.New("channel entrant actions skipped for inactive channel")

var (
	// ErrWelcomeMessageCustomerNameUnavailable means the existing local
	// Customer presentation Port failed. It is intentionally distinct from a
	// missing Customer or blank display name, both of which render "朋友".
	ErrWelcomeMessageCustomerNameUnavailable = errors.New("channel welcome customer name unavailable")
	// ErrWelcomeMessageTemplateInvalid blocks a legacy unsupported template
	// without removing its content or sending the raw marker to a Provider.
	ErrWelcomeMessageTemplateInvalid = errors.New("channel welcome template invalid")
	// ErrWelcomeMessageTooLong blocks a frozen Provider body over the WeCom
	// 4,000-rune limit before the one-time welcome grant is redeemed.
	ErrWelcomeMessageTooLong = errors.New("channel welcome message too long")
	// ErrWelcomeMessageUnavailable is a fail-closed immutable snapshot or
	// cipher failure. Callers must not redeem a welcome code after this error.
	ErrWelcomeMessageUnavailable = errors.New("channel welcome message unavailable")
)

// MaxWelcomeMessageUTF8Bytes is the maximum UTF-8 encoding of the Provider's
// 4,000-rune text contract. Cipher storage must accept every legal body.
const MaxWelcomeMessageUTF8Bytes = channeldomain.WelcomeMessageMaxRunes * 4

type WelcomeMaterialPlan struct {
	ImageIDs, MiniProgramIDs, AttachmentIDs, GroupInviteIDs []int64
}

// WelcomeMaterialSnapshotResolver is implemented by the Composition Root
// over Media's stable capture/freezer ports. It must be called in the same
// Unit of Work that accepts the entrant actions, so mutable library records
// can never be reopened by an Outbound worker.
type WelcomeMaterialSnapshotResolver interface {
	ResolveWelcomeMaterialSnapshot(context.Context, WelcomeMaterialPlan, time.Time) (json.RawMessage, string, error)
}

type EntrantActionCommand struct {
	CallbackID      string
	CustomerID      customerdomain.CustomerID
	Resolution      channeldomain.StateResolution
	WelcomeGrantRef string
	OccurredAt      time.Time
}

type EntrantActionAccepter interface {
	AcceptEntrantActions(context.Context, EntrantActionCommand) error
}

// CallbackWelcomeCommand is accepted at the authenticated callback boundary.
// It intentionally has no customer_id: customer provisioning and assignment
// continue through the normal Inbox lifecycle and may only link the immutable
// intent afterwards.
type CallbackWelcomeCommand struct {
	CallbackID      string
	CorpID          string
	Resolution      channeldomain.StateResolution
	WelcomeGrantRef string
	OccurredAt      time.Time
	FirstReceivedAt time.Time
	SendDeadlineAt  time.Time
}

type CallbackWelcomeAccepter interface {
	AcceptCallbackWelcome(context.Context, CallbackWelcomeCommand) error
}

type PublishedEntrantAction struct {
	ActionID, ChannelID, ConfigVersion, CustomerID, StaffID int64
	Kind, EffectRef, WelcomeGrantRef, WelcomeMessage        string
	WelcomeMaterialSnapshot                                 json.RawMessage
	LocalTagID                                              int64
	FirstReceivedAt, SendDeadlineAt                         time.Time
}

type PublishedEntrantActionReader interface {
	ReadPublishedEntrantAction(context.Context, string) (PublishedEntrantAction, error)
}

// WelcomeMessageCipher protects the once-rendered Channel-owned welcome text.
// The caller supplies stable effect facts as AAD; implementations must never
// serialize the plaintext, Customer data, or callback material.
type WelcomeMessageCipher interface {
	EncryptChannelWelcomeMessage([]byte, []byte) ([]byte, int16, error)
	DecryptChannelWelcomeMessage([]byte, int16, []byte) ([]byte, error)
}

// WelcomeMessageFreezer runs in a short local Unit of Work before the first
// Provider call. It locks/creates exactly one immutable rendered body for the
// accepted envelope and returns that same body on every retry.
type WelcomeMessageFreezer interface {
	FreezePublishedWelcomeMessage(context.Context, WelcomeMessageFreezeRequest) (string, error)
}

type WelcomeMessageFreezeRequest struct {
	EffectRef string
	Envelope  effectport.Envelope
}

type EntrantActionCompletion struct {
	EffectRef, State, ResultDigest, ResultReason string
	// ProviderErrorCode is a nonzero numeric code from a completed Provider
	// rejection. It never carries a response body, credentials, or identity.
	ProviderErrorCode int64
	Attempt           int32
	CompletedAt       time.Time
}

type EntrantActionCompletionWriter interface {
	CompleteEntrantAction(context.Context, EntrantActionCompletion) error
}
