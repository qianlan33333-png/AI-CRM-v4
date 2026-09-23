package port

import (
	"context"
	"encoding/json"
	"time"
)

// Event is the narrow Automation-owned configuration fact used by this
// preparation slice. It records local mutations only; it never dispatches a
// task, runs an Agent, or calls a Provider.
type Event struct {
	Type           string
	Payload        json.RawMessage
	OccurredAt     time.Time
	IdempotencyKey string
}

type EventID int64

// EventAppender must append inside the caller's UnitOfWork. Terra adapts
// this seam to the v3 versioned event/outbox port without changing Agent
// configuration semantics.
type EventAppender interface {
	Append(context.Context, Event) (EventID, error)
}

const (
	EventAgentCreated        = "automation.agent.created"
	EventAgentUpdated        = "automation.agent.updated"
	EventAgentCopied         = "automation.agent.copied"
	EventAgentPublished      = "automation.agent.published"
	EventAgentStatusChanged  = "automation.agent.status_changed"
	EventFixedContentUpdated = "automation.agent.fixed_content_updated"
)
