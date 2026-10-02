package port

import (
	"context"
	"time"

	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
)

// CollectedPhoneCommand associates a submitted contact with an already-known
// customer. The source domain keeps its encrypted immutable original. Neither
// payment state nor later product/questionnaire status changes this fact.
type CollectedPhoneCommand struct {
	identityport.DeclaredPhoneCommand
	CollectedAt time.Time
	Historical  bool
}

// CollectedPhoneRecorder participates in the caller's existing UoW. It never
// opens a transaction, provisions/merges a customer, or calls a Provider.
type CollectedPhoneRecorder interface {
	CollectPhoneWithin(context.Context, CollectedPhoneCommand) (identityport.DeclaredAttachResult, error)
}
