// Package audit records immutable, idempotent platform audit facts.
package audit

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/idempotency"
)

var ErrInvalidEvent = errors.New("invalid audit event")

type Event struct {
	ID             int64
	IdempotencyKey idempotency.Key
	Action         string
	ActorType      string
	ActorID        string
	ResourceType   string
	ResourceID     string
	Payload        json.RawMessage
	OccurredAt     time.Time
	CreatedAt      time.Time
}

type Store interface {
	Append(context.Context, Event) (Event, error)
}

type Service struct {
	store Store
	now   func() time.Time
}

func NewService(store Store) (*Service, error) {
	if store == nil {
		return nil, errors.New("audit store is required")
	}
	return &Service{store: store, now: time.Now}, nil
}

func (service *Service) Append(ctx context.Context, event Event) (Event, error) {
	if _, err := idempotency.Parse(string(event.IdempotencyKey)); err != nil {
		return Event{}, ErrInvalidEvent
	}
	if !validLabel(event.Action, 120) ||
		!validLabel(event.ActorType, 80) || !validLabel(event.ResourceType, 80) {
		return Event{}, ErrInvalidEvent
	}
	if len(event.Payload) == 0 {
		event.Payload = json.RawMessage(`{}`)
	}
	if !json.Valid(event.Payload) {
		return Event{}, ErrInvalidEvent
	}
	if event.OccurredAt.IsZero() {
		event.OccurredAt = service.now().UTC()
	}
	return service.store.Append(ctx, event)
}

func validLabel(value string, maximum int) bool {
	return value != "" && strings.TrimSpace(value) == value && len(value) <= maximum
}
