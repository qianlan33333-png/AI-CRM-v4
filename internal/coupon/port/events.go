package port

import (
	"context"
	"encoding/json"
	"time"
)

// Event is the narrow Coupon-owned rule fact shape used by the local
// application behavior. Terra can adapt it to the v3 versioned event port at
// the composition boundary without importing a central event implementation.
type Event struct {
	Type           string
	Payload        json.RawMessage
	OccurredAt     time.Time
	IdempotencyKey string
}

type EventID int64

// EventAppender persists a Coupon-local rule fact inside the caller's
// UnitOfWork. It must not dispatch work or call a Provider.
type EventAppender interface {
	Append(context.Context, Event) (EventID, error)
}

const (
	EventCouponCreated   = "coupon.created"
	EventCouponUpdated   = "coupon.updated"
	EventCouponPublished = "coupon.published"
	EventCouponStopped   = "coupon.stopped"
	EventCouponArchived  = "coupon.archived"
	EventCouponDeleted   = "coupon.deleted"
	EventCouponCopied    = "coupon.copied"
)
