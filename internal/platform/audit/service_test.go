package audit

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/idempotency"
)

type recordingStore struct {
	event Event
	err   error
}

func (store *recordingStore) Append(_ context.Context, event Event) (Event, error) {
	store.event = event
	return event, store.err
}

func TestServiceValidatesAndDefaultsAuditEvent(t *testing.T) {
	store := &recordingStore{}
	service, err := NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	fixed := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return fixed }
	key, err := idempotency.Parse("audit:test:0001")
	if err != nil {
		t.Fatal(err)
	}

	event, err := service.Append(context.Background(), Event{
		IdempotencyKey: key,
		Action:         "platform.tested",
		ActorType:      "service",
		ResourceType:   "platform",
	})
	if err != nil {
		t.Fatal(err)
	}
	if event.OccurredAt != fixed || string(event.Payload) != "{}" {
		t.Fatalf("event=%+v", event)
	}

	_, err = service.Append(context.Background(), Event{
		IdempotencyKey: key,
		Action:         " platform.tested",
		ActorType:      "service",
		ResourceType:   "platform",
	})
	if !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("expected ErrInvalidEvent, got %v", err)
	}
}
