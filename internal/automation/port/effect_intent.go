package port

import (
	"context"
	"time"
)

// EffectIntent is the only future external-effect contract frozen by PR07.
// It contains local Agent/version facts and a digest, never a customer,
// audience, recipient, prompt body, provider credential, or provider payload.
// A later adapter must translate it through outbound/EER and independently
// reconcile any external outcome.
type EffectIntent struct {
	AgentID          AgentID   `json:"agent_id"`
	PublishedVersion int64     `json:"published_version"`
	IntentKind       string    `json:"intent_kind"`
	PayloadDigest    [32]byte  `json:"payload_digest"`
	OccurredAt       time.Time `json:"occurred_at"`
}

type EffectIntentID int64

type EffectIntentAppender interface {
	Append(context.Context, EffectIntent) (EffectIntentID, error)
}
