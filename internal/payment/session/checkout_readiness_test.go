package session

import (
	"context"
	"crypto/sha256"
	"errors"
	paymentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	"testing"
	"time"
)

func TestConsumedSessionCanReadButCannotCreateCheckout(t *testing.T) {
	now := time.Now().UTC()
	token := "pays_test_session_long_enough"
	digest := sha256.Sum256([]byte(token))
	store := &memoryStore{records: map[[32]byte]Record{digest: {PayerCustomerID: 11, PayerIdentityID: 4, Channel: paymentdomain.ChannelH5Official, UnionIDVerified: true, ExpiresAt: now.Add(time.Hour)}}}
	svc := &Service{store: store}
	if ready, err := svc.CanCreateCheckoutWithin(context.Background(), token, now); err != nil || !ready {
		t.Fatalf("fresh %v %v", ready, err)
	}
	rec := store.records[digest]
	rec.ConsumedAt = &now
	store.records[digest] = rec
	if ready, err := svc.CanCreateCheckoutWithin(context.Background(), token, now); err != nil || ready {
		t.Fatalf("consumed %v %v", ready, err)
	}
	if _, err := svc.LookupWithin(context.Background(), token, now); err != nil {
		t.Fatal("consumed session cannot read", err)
	}
	if _, err := svc.CanCreateCheckoutWithin(context.Background(), token, now.Add(2*time.Hour)); !errors.Is(err, paymentport.ErrSessionRequired) {
		t.Fatal(err)
	}
}
