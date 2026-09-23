package port

import (
	"context"
	"encoding/json"
	"time"
)

// EventID is the opaque local event identity returned by an event appender.
// Group Ops only needs the identifier to prove that its local fact was
// appended in the same UnitOfWork; delivery is owned by the future events
// integration.
type EventID int64

// Event is the minimal transactional fact emitted by Group Ops plan writes.
// It intentionally has no customer, audience, campaign, or provider fields.
type Event struct {
	Type           string
	Payload        json.RawMessage
	OccurredAt     time.Time
	IdempotencyKey string
}

const EvGroupOpsPlanUpdated = "group_ops.plan_updated"

// EventAppender appends a fact to the transaction supplied by UnitOfWork.
// It must not dispatch work or call an external provider.
type EventAppender interface {
	Append(context.Context, Event) (EventID, error)
}
