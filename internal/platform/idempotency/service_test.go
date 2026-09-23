package idempotency

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

type fakeStore struct {
	receipt Receipt
	created bool
	claims  []Receipt
	outcome Outcome
}

func (store *fakeStore) PutIfAbsent(_ context.Context, receipt Receipt) (Receipt, bool, error) {
	if store.receipt.Key == "" {
		store.receipt = receipt
	}
	return store.receipt, store.created, nil
}

func (store *fakeStore) Claim(_ context.Context, _ Claim) ([]Receipt, error) {
	return store.claims, nil
}

func (store *fakeStore) RecordOutcome(_ context.Context, outcome Outcome) (Receipt, error) {
	store.outcome = outcome
	return store.receipt, nil
}

func TestBeginReturnsReplayForSameCanonicalPayload(t *testing.T) {
	key, err := Parse("platform:test:0001")
	if err != nil {
		t.Fatal(err)
	}
	storedHash, err := CanonicalPayloadHash(json.RawMessage(`{"a":1,"b":2}`))
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeStore{receipt: Receipt{Key: key, PayloadHash: storedHash}, created: false}
	service, err := NewService(store)
	if err != nil {
		t.Fatal(err)
	}

	result, err := service.Begin(context.Background(), Begin{
		Key:     key,
		Payload: json.RawMessage(`{ "b": 2, "a": 1 }`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Replay {
		t.Fatal("expected replay")
	}
}

func TestBeginRejectsPayloadMismatch(t *testing.T) {
	key, _ := Parse("platform:test:0002")
	storedHash, _ := CanonicalPayloadHash(json.RawMessage(`{"value":1}`))
	service, _ := NewService(&fakeStore{receipt: Receipt{Key: key, PayloadHash: storedHash}})

	_, err := service.Begin(context.Background(), Begin{Key: key, Payload: json.RawMessage(`{"value":2}`)})
	if !errors.Is(err, ErrPayloadMismatch) {
		t.Fatalf("expected ErrPayloadMismatch, got %v", err)
	}
}

func TestClaimAndOutcomeValidation(t *testing.T) {
	service, _ := NewService(&fakeStore{})
	if _, err := service.Claim(context.Background(), Claim{Owner: " worker", Limit: 1, LeaseDuration: time.Minute}); !errors.Is(err, ErrInvalidClaim) {
		t.Fatalf("expected ErrInvalidClaim, got %v", err)
	}
	if _, err := service.RecordOutcome(context.Background(), Outcome{Status: StatusAccepted}); !errors.Is(err, ErrInvalidPayload) {
		t.Fatalf("expected ErrInvalidPayload, got %v", err)
	}
}
