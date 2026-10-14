package port

import (
	"context"
	effect "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	"time"
)

// Control uses the frozen owner reference, never a caller-provided Provider ID.
type PrivateMessageControlCommand struct {
	PayloadReference, Action string
	ActorID                  int64
	RequestKey               effect.Digest
}
type PrivateMessageControl struct {
	EffectID      string     `json:"effect_id"`
	Action        string     `json:"action"`
	State         string     `json:"state"`
	ReceiptDigest string     `json:"receipt_digest"`
	SentAt        *time.Time `json:"sent_at,omitempty"`
	StoppedAt     *time.Time `json:"stopped_at,omitempty"`
	ObservedAt    *time.Time `json:"observed_at,omitempty"`
	Replayed      bool       `json:"replayed"`
}
type PrivateMessageController interface {
	RequestPrivateMessageControlWithin(context.Context, PrivateMessageControlCommand) (PrivateMessageControl, error)
	ReadPrivateMessageControl(context.Context, string, string, ...string) (PrivateMessageControl, error)
}
type PrivateMessageStopper interface {
	// attempted is true only after crossing the stop boundary. An ambiguous
	// response retains the same External Effects operation for reconciliation.
	StopPrivateMessage(context.Context, string) (attempted bool, err error)
}
