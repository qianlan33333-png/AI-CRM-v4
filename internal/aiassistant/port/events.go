package port

import (
	"context"
	"encoding/json"
	"time"
)

const (
	EventPlanCreated         = "aiassistant.plan_created.v1"
	EventIntegrationResolved = "aiassistant.integration_resolved.v1"
	EventContentUpdated      = "aiassistant.content_updated.v1"
	EventRecipientReviewed   = "aiassistant.recipient_reviewed.v1"
	EventPlanApproved        = "aiassistant.plan_approved.v1"
	EventPlanRejected        = "aiassistant.plan_rejected.v1"
	EventEffectProjection    = "aiassistant.effect_projected.v1"
	EventEffectReconciled    = "aiassistant.effect_reconciled.v1"
)

type Event struct {
	Type           string
	AggregateID    PlanID
	RecipientID    RecipientID
	ActorID        int64
	IdempotencyKey string
	Payload        json.RawMessage
	OccurredAt     time.Time
}

type EventAppender interface {
	AppendEvent(context.Context, Event) error
}
