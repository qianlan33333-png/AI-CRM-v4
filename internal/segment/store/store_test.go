package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"testing"
	"time"

	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	segmentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/domain"
)

func TestRepositoryRequiresExplicitTransaction(t *testing.T) {
	repo := &Repository{}
	_, err := repo.CreateGroup(context.Background(), groupFixture())
	if !errors.Is(err, platformpostgres.ErrTransactionNeeded) {
		t.Fatalf("CreateGroup without transaction=%v", err)
	}
	_, _, err = repo.Reserve(context.Background(), Reservation{Operation: "create", ActorScope: "staff:1", CreatedAt: time.Now(), KeyDigest: sha256.Sum256([]byte("key")), PayloadDigest: sha256.Sum256([]byte("payload"))})
	if !errors.Is(err, platformpostgres.ErrTransactionNeeded) {
		t.Fatalf("Reserve without transaction=%v", err)
	}
	_, err = repo.AppendMutationFacts(context.Background(), MutationFact{ResourceKind: "package", ResourceID: 1, Operation: "create", EventType: "created", ActorID: 1, Payload: json.RawMessage(`{"id":1}`), IdempotencyKey: "key", OccurredAt: time.Now()})
	if !errors.Is(err, platformpostgres.ErrTransactionNeeded) {
		t.Fatalf("AppendMutationFacts without transaction=%v", err)
	}
}

func TestMutationFactsRejectPIIFreeShapeViolations(t *testing.T) {
	repo := &Repository{}
	for _, fact := range []MutationFact{
		{ResourceKind: "customer", ResourceID: 1, Operation: "create", EventType: "created", ActorID: 1, Payload: json.RawMessage(`{"id":1}`), IdempotencyKey: "key", OccurredAt: time.Now()},
		{ResourceKind: "package", ResourceID: 1, Operation: "create", EventType: "created", ActorID: 1, Payload: json.RawMessage(`[]`), IdempotencyKey: "key", OccurredAt: time.Now()},
	} {
		if _, err := repo.AppendMutationFacts(context.Background(), fact); !errors.Is(err, ErrInvalid) {
			t.Fatalf("fact=%+v err=%v", fact, err)
		}
	}
}

func groupFixture() segmentdomain.Group {
	return segmentdomain.Group{}
}
