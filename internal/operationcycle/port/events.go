package port

import (
	"context"
	"encoding/json"
	"time"
)

// EventID identifies a local operation-cycle fact. The event boundary carries
// no customer identity and cannot invoke a Provider.
type EventID int64

const (
	EvOperationCycleFact       = "operation_cycle.fact_recorded"
	ConsumerOperationCycleFact = "operation-cycle.fact.v1"
)

// Event is the operation-cycle-owned event projection. The composition root
// may adapt it to platform audit/outbox storage in the same UnitOfWork.
type Event struct {
	Type           string
	Payload        json.RawMessage
	OccurredAt     time.Time
	IdempotencyKey string
}

// EventAppender persists a local fact in the transaction supplied by the
// caller. It must not dispatch a Provider call.
type EventAppender interface {
	Append(context.Context, Event) (EventID, error)
}

// DeliveryAcceptor records local fact-delivery acceptance only. It is not an
// external-effect or campaign-recipient execution port.
type DeliveryAcceptor interface {
	Accept(context.Context, EventID, string) error
}
