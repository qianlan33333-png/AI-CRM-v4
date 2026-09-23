package port

import (
	"context"
	"time"
)

// CommercePushDeliveryQuery contains only an Order-owned paid-event ID or an
// exact historical source coordinate. It never accepts an arbitrary orders.id
// as a legacy source ID.
type CommercePushDeliveryQuery struct {
	PaidEventID                                                       int64
	HistoricalSourceKind, HistoricalSourceSystem, HistoricalSourceKey string
}

// CommercePushDelivery is a read-only, safe projection for the frozen order
// page. Endpoint, signing material, raw payload/response bodies and customer
// identity values are deliberately absent.
type CommercePushDelivery struct {
	ID, Source, EffectID, State, ErrorMessage string
	// ResultCode is a fixed, safe Provider result code for a current delivery.
	// It is nil where no Provider response is known, including legacy rows.
	ResultCode *string
	// HistoricalDeliveryID and LegacyEffectJobID are source evidence only. They
	// are deliberately separate from EffectID, which names a V3 EER only.
	HistoricalDeliveryID string
	LegacyEffectJobID    *int64
	AttemptCount         int32
	// nil denotes that frozen history provides no evidence for this fact. It is
	// never serialized as false simply because an old source omitted it.
	ProviderCallAttempted, RealExternalCallExecuted, ProviderResultReceived *bool
	ResponseStatus                                                          *int
	ResponseBodyProtected                                                   bool
	CreatedAt, UpdatedAt                                                    time.Time
}

// CommercePushDeliveryReader is implemented by Outbound. It exposes no retry
// command; outcome_unknown remains a read-only fact until explicit supported
// reconciliation exists.
type CommercePushDeliveryReader interface {
	ListCommercePushDeliveries(context.Context, CommercePushDeliveryQuery) ([]CommercePushDelivery, error)
}
