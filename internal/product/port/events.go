package port

import (
	"context"
	"encoding/json"
	"time"
)

// Event is the narrow Product-owned fact shape used by the local application
// behavior. Terra can later adapt it to the v3 versioned event port at the
// composition boundary without importing a central event implementation here.
type Event struct {
	Type           string
	Payload        json.RawMessage
	OccurredAt     time.Time
	IdempotencyKey string
}

type EventID int64

// EventAppender persists a Product-local fact within the caller's UnitOfWork.
// It must not dispatch work or call an external provider.
type EventAppender interface {
	Append(context.Context, Event) (EventID, error)
}

const (
	EventProductCreated                 = "product.created"
	EventProductUpdated                 = "product.updated"
	EventExternalPushConfigurationSaved = "product.external_push.configuration_saved"
	EventExternalPushTestAccepted       = "product.external_push.test_accepted"
)
