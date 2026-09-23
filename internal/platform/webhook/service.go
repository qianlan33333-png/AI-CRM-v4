// Package webhook provides durable, idempotent webhook ingestion. Provider
// adapters verify signatures before calling this service.
package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/idempotency"
)

var (
	ErrInvalidDelivery  = errors.New("invalid webhook delivery")
	ErrPayloadMismatch  = errors.New("webhook payload mismatch")
	ErrInvalidClaim     = errors.New("invalid webhook claim")
	ErrInvalidRetry     = errors.New("invalid webhook retry")
	ErrRetryUnavailable = errors.New("webhook retry unavailable")
	ErrConcurrentUpdate = errors.New("webhook delivery changed concurrently")
)

type Status string

const (
	StatusReceived   Status = "received"
	StatusProcessing Status = "processing"
	StatusProcessed  Status = "processed"
	StatusRetryable  Status = "retryable"
	StatusFailed     Status = "failed"
)

type Delivery struct {
	ID             int64
	Provider       string
	IdempotencyKey idempotency.Key
	PayloadHash    [32]byte
	Payload        json.RawMessage
	Status         Status
	AttemptCount   int
	MaxAttempts    int
	NextAttemptAt  time.Time
	LeaseOwner     string
	LeaseExpiresAt *time.Time
	LastErrorCode  string
	ReceivedAt     time.Time
	ProcessedAt    *time.Time
	UpdatedAt      time.Time
}

type Store interface {
	PutIfAbsent(context.Context, Delivery) (stored Delivery, created bool, err error)
	Claim(context.Context, Claim) ([]Delivery, error)
	Complete(context.Context, Completion) (Delivery, error)
}

// DeliveryReader is intentionally optional so existing ingest-only adapters do
// not gain a new obligation. A durable follow-up worker may use it to reload
// an already authenticated Inbox payload by its internal ID; it never exposes
// payloads through HTTP.
type DeliveryReader interface {
	Get(context.Context, int64) (Delivery, error)
}

// RetryStore is intentionally separate from Store so existing provider and
// in-memory stores remain ingest-only unless they explicitly support the
// privileged operational retry transition.
type RetryStore interface {
	Retry(context.Context, Retry) (Delivery, error)
}

type Service struct {
	store Store
	now   func() time.Time
}

func NewService(store Store) (*Service, error) {
	if store == nil {
		return nil, errors.New("webhook store is required")
	}
	return &Service{store: store, now: time.Now}, nil
}

type Ingest struct {
	Provider       string
	IdempotencyKey idempotency.Key
	Payload        json.RawMessage
	MaxAttempts    int
}

type IngestResult struct {
	Delivery Delivery
	Replay   bool
}

func (service *Service) Ingest(ctx context.Context, input Ingest) (IngestResult, error) {
	if _, err := idempotency.Parse(string(input.IdempotencyKey)); err != nil {
		return IngestResult{}, ErrInvalidDelivery
	}
	if !validProvider(input.Provider) {
		return IngestResult{}, ErrInvalidDelivery
	}
	payloadHash, err := idempotency.CanonicalPayloadHash(input.Payload)
	if err != nil {
		return IngestResult{}, ErrInvalidDelivery
	}
	if input.MaxAttempts == 0 {
		input.MaxAttempts = 8
	}
	if input.MaxAttempts < 1 {
		return IngestResult{}, ErrInvalidDelivery
	}
	now := service.now().UTC()
	stored, created, err := service.store.PutIfAbsent(ctx, Delivery{
		Provider:       input.Provider,
		IdempotencyKey: input.IdempotencyKey,
		PayloadHash:    payloadHash,
		Payload:        input.Payload,
		Status:         StatusReceived,
		MaxAttempts:    input.MaxAttempts,
		NextAttemptAt:  now,
	})
	if err != nil {
		return IngestResult{}, err
	}
	if !bytes.Equal(stored.PayloadHash[:], payloadHash[:]) {
		return IngestResult{}, ErrPayloadMismatch
	}
	return IngestResult{Delivery: stored, Replay: !created}, nil
}

type Claim struct {
	Provider      string
	Owner         string
	Limit         int
	LeaseDuration time.Duration
	Now           time.Time
}

func (service *Service) Claim(ctx context.Context, claim Claim) ([]Delivery, error) {
	if !validProvider(claim.Provider) || strings.TrimSpace(claim.Owner) != claim.Owner || claim.Owner == "" ||
		claim.Limit < 1 || claim.Limit > 100 || claim.LeaseDuration <= 0 {
		return nil, ErrInvalidClaim
	}
	if claim.Now.IsZero() {
		claim.Now = service.now().UTC()
	}
	return service.store.Claim(ctx, claim)
}

type Completion struct {
	ID              int64
	ExpectedAttempt int
	Status          Status
	LastErrorCode   string
	NextAttemptAt   *time.Time
}

// Retry grants exactly one additional processing attempt without erasing the
// delivery's attempt history. ExpectedAttempt makes the transition a CAS.
type Retry struct {
	ID              int64
	Provider        string
	ExpectedAttempt int
	ExpectedStatus  Status
	Now             time.Time
}

func (service *Service) Retry(ctx context.Context, retry Retry) (Delivery, error) {
	if retry.ID < 1 || !validProvider(retry.Provider) || retry.ExpectedAttempt < 1 ||
		(retry.ExpectedStatus != StatusFailed && retry.ExpectedStatus != StatusRetryable) {
		return Delivery{}, ErrInvalidRetry
	}
	if retry.Now.IsZero() {
		retry.Now = service.now().UTC()
	}
	store, ok := service.store.(RetryStore)
	if !ok {
		return Delivery{}, ErrRetryUnavailable
	}
	return store.Retry(ctx, retry)
}

func (service *Service) Complete(ctx context.Context, completion Completion) (Delivery, error) {
	if completion.ID < 1 || completion.ExpectedAttempt < 1 ||
		(completion.Status != StatusProcessed && completion.Status != StatusRetryable && completion.Status != StatusFailed) ||
		(completion.Status == StatusRetryable && completion.NextAttemptAt == nil) ||
		strings.TrimSpace(completion.LastErrorCode) != completion.LastErrorCode {
		return Delivery{}, ErrInvalidDelivery
	}
	return service.store.Complete(ctx, completion)
}

func (service *Service) Get(ctx context.Context, id int64) (Delivery, error) {
	if id < 1 {
		return Delivery{}, ErrInvalidDelivery
	}
	reader, ok := service.store.(DeliveryReader)
	if !ok {
		return Delivery{}, ErrInvalidDelivery
	}
	return reader.Get(ctx, id)
}

func validProvider(value string) bool {
	return value != "" && len(value) <= 80 && strings.TrimSpace(value) == value
}
