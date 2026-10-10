package app

import (
	"context"
	"testing"
	"time"

	distributiondomain "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/domain"
	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
	distributionstore "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/store"
)

type promotionExpiryPrecisionStore struct {
	promotionPaginationStore
	credential distributiondomain.PromotionCredential
	receipt    distributionstore.OperationReceipt
	hasReceipt bool
	inserts    int
}

func (s *promotionExpiryPrecisionStore) InsertPromotionCredentialWithin(_ context.Context, credential distributiondomain.PromotionCredential) (distributiondomain.PromotionCredential, error) {
	s.inserts++
	credential.ID = 91
	// Match the precision returned by PostgreSQL when timestamp values are
	// inserted and read back.
	credential.CreatedAt = credential.CreatedAt.UTC().Truncate(time.Microsecond)
	credential.ExpiresAt = credential.ExpiresAt.UTC().Truncate(time.Microsecond)
	s.credential = credential
	return credential, nil
}

func (s *promotionExpiryPrecisionStore) ReadPromotionCredentialWithin(_ context.Context, id int64, _ bool) (distributiondomain.PromotionCredential, error) {
	if !s.hasReceipt || id != s.credential.ID {
		return distributiondomain.PromotionCredential{}, distributionport.ErrNotFound
	}
	return s.credential, nil
}

func (s *promotionExpiryPrecisionStore) ReadDistributorWithin(context.Context, int64, bool) (distributiondomain.Distributor, distributionport.ReceiverReadiness, error) {
	return distributiondomain.Distributor{ID: 1, CustomerID: 11}, distributionport.ReceiverReadiness{}, nil
}

func (s *promotionExpiryPrecisionStore) ReadOperationReceiptWithin(context.Context, string, string, string) (distributionstore.OperationReceipt, bool, error) {
	return s.receipt, s.hasReceipt, nil
}

func (s *promotionExpiryPrecisionStore) AppendOperationReceiptWithin(_ context.Context, _, _, _ string, digest [32]byte, resultKind string, resultID int64, _ time.Time) error {
	s.receipt = distributionstore.OperationReceipt{PayloadDigest: digest, ResultKind: resultKind, ResultID: resultID}
	s.hasReceipt = true
	return nil
}

func TestIssuePromotionLinkReturnsMicrosecondStableExpiry(t *testing.T) {
	now := time.Date(2026, 11, 9, 1, 13, 39, 344606180, time.UTC)
	service, _, actor := promotionPaginationFixture(t, 1)
	service.now = func() time.Time { return now }
	service.qualification.now = func() time.Time { return now }
	store := &promotionExpiryPrecisionStore{promotionPaginationStore: promotionPaginationStore{now: now}}
	service.store = store
	command := distributionport.IssuePromotionCommand{
		Actor: actor, ProductID: 11, ProductType: distributiondomain.ProductTypeStandard,
		IdempotencyKey: "promotion-expiry-precision-key",
	}

	first, err := service.IssuePromotionLink(context.Background(), command)
	if err != nil {
		t.Fatal("issue promotion link:", err)
	}
	if first.ExpiresAt.Nanosecond()%int(time.Microsecond) != 0 || !first.ExpiresAt.Equal(store.credential.ExpiresAt) {
		t.Fatalf("first expiry is not PostgreSQL-stable: returned=%s stored=%s", first.ExpiresAt.Format(time.RFC3339Nano), store.credential.ExpiresAt.Format(time.RFC3339Nano))
	}

	replay, err := service.IssuePromotionLink(context.Background(), command)
	if err != nil || replay.URL != first.URL || !replay.ExpiresAt.Equal(first.ExpiresAt) || store.inserts != 1 {
		t.Fatalf("exact replay changed promotion link: first=%+v replay=%+v inserts=%d err=%v", first, replay, store.inserts, err)
	}
}
