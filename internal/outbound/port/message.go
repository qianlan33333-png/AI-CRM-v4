// Package port defines the stable Outbound message boundary.
package port

import (
	"context"
	"encoding/json"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
)

type MessageIntent struct {
	SourceKind            string
	SourceID              int64
	RunRecipientID        int64
	CustomerID            customerdomain.CustomerID
	IdentityID            int64
	SenderStaffID         int64
	AgentID               int64
	AgentPublishedVersion int64
	ContentReference      string
	SourceDigest          [32]byte
	TargetDigest          [32]byte
	PayloadDigest         [32]byte
	ContentSnapshot       json.RawMessage
	ContentSnapshotDigest [32]byte
	PolicyDigest          [32]byte
	ReceiptKey            string
	ScheduledAt           time.Time
}

type MessageExecution struct {
	MessageIntentID       int64
	RunRecipientID        int64
	CustomerID            customerdomain.CustomerID
	SenderStaffID         int64
	AgentID               int64
	AgentPublishedVersion int64
	ContentReference      string
	PayloadDigest         [32]byte
	ContentSnapshot       json.RawMessage
	ContentSnapshotDigest [32]byte
}

type MessageExecutionReader interface {
	MessageExecution(context.Context, string) (MessageExecution, bool, error)
}

type MessageReceiptRecorder interface {
	RecordAutomationMessageReceipt(context.Context, int64, string, PrivateMessageTarget, string, string) error
}

type MessageDeliveryStore interface {
	AutomationMessageReceipt(context.Context, string) (PrivateMessageDelivery, bool, error)
	SaveAutomationMessageDelivery(context.Context, string, PrivateMessageDelivery) error
}

// FrozenAutomationMessagePayloadReader converts an immutable Outbound intent
// snapshot into the one PrivateMessage payload accepted by the WeCom writer.
// It must fail closed if any frozen Media source no longer verifies.
type FrozenAutomationMessagePayloadReader interface {
	LoadFrozenAutomationMessagePayload(context.Context, json.RawMessage, [32]byte) (PrivateMessagePayload, error)
}
type FrozenAutomationMessageMediaPreflighter interface {
	PrepareFrozenAutomationMessageMedia(context.Context, json.RawMessage, [32]byte) error
}

type MessageAcceptance struct {
	MessageIntentID int64
	EffectID        string
	QueueReceiptID  string
	Replayed        bool
}

// TransactionalMessageAccepter writes the Outbound intent/binding and accepts
// the External Effect inside the caller's transaction-bound context.
type TransactionalMessageAccepter interface {
	AcceptMessageWithin(context.Context, MessageIntent) (MessageAcceptance, error)
}

type CompletionState string

const (
	CompletionProviderAccepted CompletionState = "provider_accepted"
	CompletionDeliveryProven   CompletionState = "delivery_proven"
	CompletionRetryableFailed  CompletionState = "retryable_failed"
	CompletionFinalFailed      CompletionState = "final_failed"
	CompletionOutcomeUnknown   CompletionState = "outcome_unknown"
	CompletionReconciled       CompletionState = "reconciled"
)

type MessageCompletion struct {
	EffectID      string
	State         CompletionState
	ReceiptDigest [32]byte
	AttemptCount  int32
}

type MessageCompletionProjector interface {
	ProjectMessageCompletion(context.Context, MessageCompletion) error
}
