package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/idempotency"
)

type fakeStore struct {
	delivery Delivery
	created  bool
}

type fakeRetryStore struct {
	fakeStore
	retry Retry
	err   error
}

func (store *fakeRetryStore) Retry(_ context.Context, retry Retry) (Delivery, error) {
	store.retry = retry
	return store.delivery, store.err
}

func (store *fakeStore) PutIfAbsent(_ context.Context, delivery Delivery) (Delivery, bool, error) {
	if store.delivery.ID == 0 {
		store.delivery = delivery
	}
	return store.delivery, store.created, nil
}

func (*fakeStore) Claim(context.Context, Claim) ([]Delivery, error) {
	return nil, nil
}

func (store *fakeStore) Complete(_ context.Context, _ Completion) (Delivery, error) {
	return store.delivery, nil
}

func TestIngestDetectsReplayAndPayloadDrift(t *testing.T) {
	key, _ := idempotency.Parse("wecom:event:0001")
	hash, _ := idempotency.CanonicalPayloadHash(json.RawMessage(`{"event":"change"}`))
	store := &fakeStore{delivery: Delivery{
		ID:             42,
		Provider:       "wecom",
		IdempotencyKey: key,
		PayloadHash:    hash,
	}}
	service, err := NewService(store)
	if err != nil {
		t.Fatal(err)
	}

	result, err := service.Ingest(context.Background(), Ingest{
		Provider:       "wecom",
		IdempotencyKey: key,
		Payload:        json.RawMessage(`{ "event": "change" }`),
	})
	if err != nil || !result.Replay {
		t.Fatalf("result=%+v err=%v", result, err)
	}

	_, err = service.Ingest(context.Background(), Ingest{
		Provider:       "wecom",
		IdempotencyKey: key,
		Payload:        json.RawMessage(`{"event":"different"}`),
	})
	if !errors.Is(err, ErrPayloadMismatch) {
		t.Fatalf("expected ErrPayloadMismatch, got %v", err)
	}
}

func TestClaimValidation(t *testing.T) {
	service, _ := NewService(&fakeStore{})
	_, err := service.Claim(context.Background(), Claim{Owner: "worker", Limit: 0, LeaseDuration: time.Minute})
	if !errors.Is(err, ErrInvalidClaim) {
		t.Fatalf("expected ErrInvalidClaim, got %v", err)
	}
	_, err = service.Claim(context.Background(), Claim{Provider: " wecom", Owner: "worker", Limit: 1, LeaseDuration: time.Minute})
	if !errors.Is(err, ErrInvalidClaim) {
		t.Fatalf("expected invalid provider ErrInvalidClaim, got %v", err)
	}
}

func TestRetryIsExplicitPrivilegedCAS(t *testing.T) {
	now := time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC)
	store := &fakeRetryStore{fakeStore: fakeStore{delivery: Delivery{ID: 42, Status: StatusRetryable}}}
	service, err := NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	got, err := service.Retry(context.Background(), Retry{
		ID: 42, Provider: "wecom.external_contact", ExpectedAttempt: 8, ExpectedStatus: StatusFailed, Now: now,
	})
	if err != nil || got.ID != 42 || store.retry.ExpectedAttempt != 8 || !store.retry.Now.Equal(now) {
		t.Fatalf("delivery=%+v retry=%+v err=%v", got, store.retry, err)
	}

	if _, err = service.Retry(context.Background(), Retry{ID: 42, Provider: "wecom.external_contact"}); !errors.Is(err, ErrInvalidRetry) {
		t.Fatalf("expected invalid retry, got %v", err)
	}
	withoutRetry, _ := NewService(&fakeStore{})
	if _, err = withoutRetry.Retry(context.Background(), Retry{ID: 42, Provider: "wecom.external_contact", ExpectedAttempt: 1, ExpectedStatus: StatusRetryable}); !errors.Is(err, ErrRetryUnavailable) {
		t.Fatalf("expected unavailable retry, got %v", err)
	}
}
