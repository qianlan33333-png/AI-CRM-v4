package idempotency

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

var (
	ErrInvalidPayload  = errors.New("invalid idempotency payload")
	ErrPayloadMismatch = errors.New("idempotency payload mismatch")
	ErrInvalidClaim    = errors.New("invalid idempotency claim")
)

type Status string

const (
	StatusAccepted       Status = "accepted"
	StatusQueued         Status = "queued"
	StatusAttempted      Status = "attempted"
	StatusExecuted       Status = "executed"
	StatusOutcomeUnknown Status = "outcome_unknown"
	StatusReconciled     Status = "reconciled"
	StatusFailed         Status = "failed"
)

type Receipt struct {
	Key            Key
	PayloadHash    [sha256.Size]byte
	Status         Status
	Response       json.RawMessage
	AttemptCount   int
	MaxAttempts    int
	NextAttemptAt  time.Time
	LeaseOwner     string
	LeaseExpiresAt *time.Time
	LastErrorCode  string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type Store interface {
	PutIfAbsent(context.Context, Receipt) (stored Receipt, created bool, err error)
	Claim(context.Context, Claim) ([]Receipt, error)
	RecordOutcome(context.Context, Outcome) (Receipt, error)
}

type Service struct {
	store Store
	now   func() time.Time
}

func NewService(store Store) (*Service, error) {
	if store == nil {
		return nil, errors.New("idempotency store is required")
	}
	return &Service{store: store, now: time.Now}, nil
}

type Begin struct {
	Key         Key
	Payload     json.RawMessage
	MaxAttempts int
}

type BeginResult struct {
	Receipt Receipt
	Replay  bool
}

func (service *Service) Begin(ctx context.Context, command Begin) (BeginResult, error) {
	if _, err := Parse(string(command.Key)); err != nil {
		return BeginResult{}, ErrInvalidKey
	}
	payloadHash, err := CanonicalPayloadHash(command.Payload)
	if err != nil {
		return BeginResult{}, err
	}
	if command.MaxAttempts == 0 {
		command.MaxAttempts = 8
	}
	if command.MaxAttempts < 1 {
		return BeginResult{}, ErrInvalidPayload
	}
	now := service.now().UTC()
	stored, created, err := service.store.PutIfAbsent(ctx, Receipt{
		Key:           command.Key,
		PayloadHash:   payloadHash,
		Status:        StatusAccepted,
		MaxAttempts:   command.MaxAttempts,
		NextAttemptAt: now,
	})
	if err != nil {
		return BeginResult{}, err
	}
	if !bytes.Equal(stored.PayloadHash[:], payloadHash[:]) {
		return BeginResult{}, ErrPayloadMismatch
	}
	return BeginResult{Receipt: stored, Replay: !created}, nil
}

type Claim struct {
	Owner         string
	Limit         int
	LeaseDuration time.Duration
	Now           time.Time
}

func (service *Service) Claim(ctx context.Context, claim Claim) ([]Receipt, error) {
	if strings.TrimSpace(claim.Owner) != claim.Owner || claim.Owner == "" ||
		claim.Limit < 1 || claim.Limit > 100 || claim.LeaseDuration <= 0 {
		return nil, ErrInvalidClaim
	}
	if claim.Now.IsZero() {
		claim.Now = service.now().UTC()
	}
	return service.store.Claim(ctx, claim)
}

type Outcome struct {
	Key             Key
	Status          Status
	Response        json.RawMessage
	LastErrorCode   string
	NextAttemptAt   *time.Time
	ExpectedAttempt int
}

func (service *Service) RecordOutcome(ctx context.Context, outcome Outcome) (Receipt, error) {
	if _, err := Parse(string(outcome.Key)); err != nil {
		return Receipt{}, ErrInvalidPayload
	}
	if outcome.ExpectedAttempt < 1 || !validOutcomeStatus(outcome.Status) ||
		(outcome.Status == StatusQueued && outcome.NextAttemptAt == nil) {
		return Receipt{}, ErrInvalidPayload
	}
	if len(outcome.Response) > 0 && !json.Valid(outcome.Response) {
		return Receipt{}, ErrInvalidPayload
	}
	if strings.TrimSpace(outcome.LastErrorCode) != outcome.LastErrorCode {
		return Receipt{}, ErrInvalidPayload
	}
	return service.store.RecordOutcome(ctx, outcome)
}

func CanonicalPayloadHash(payload json.RawMessage) ([sha256.Size]byte, error) {
	var zero [sha256.Size]byte
	if len(payload) == 0 {
		return zero, ErrInvalidPayload
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return zero, ErrInvalidPayload
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return zero, ErrInvalidPayload
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return zero, fmt.Errorf("canonicalize idempotency payload: %w", err)
	}
	return sha256.Sum256(canonical), nil
}

func validOutcomeStatus(status Status) bool {
	switch status {
	case StatusQueued, StatusExecuted, StatusOutcomeUnknown, StatusReconciled, StatusFailed:
		return true
	default:
		return false
	}
}
