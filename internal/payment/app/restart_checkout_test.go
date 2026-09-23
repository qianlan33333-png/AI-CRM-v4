package app

import (
	"context"
	"errors"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	"strings"
	"testing"
	"time"
)

type restartStore struct {
	*abandonStore
	allowed   bool
	reviewErr error
}

func (s *restartStore) CheckoutRestartAllowed(context.Context, int64) (bool, error) {
	return s.allowed, nil
}
func (s *restartStore) RecordCheckoutRestart(ctx context.Context, _ paymentport.AbandonCheckoutCommand, _, _ time.Time) error {
	if ctx.Value(txKey{}) != true {
		return errors.New("not atomic")
	}
	if !s.abandoned {
		return paymentport.ErrConflict
	}
	if s.reviewErr != nil {
		return s.reviewErr
	}
	s.allowed = true
	return nil
}

type stoppedReader struct {
	*prepayReadStub
	completed time.Time
	err       error
}

func (s stoppedReader) StoppedAttemptWithin(ctx context.Context, _ string) (effectport.StoppedAttemptEvidence, error) {
	if ctx.Value(txKey{}) != true {
		return effectport.StoppedAttemptEvidence{}, errors.New("missing UoW")
	}
	return effectport.StoppedAttemptEvidence{Projection: s.projection, CompletedAt: s.completed}, s.err
}
func TestRestartReviewRequiresExpiredCompletedNeverDeliveredLegacyAttempt(t *testing.T) {
	for _, name := range []string{"eligible", "too_recent", "not_completed", "queued", "second_attempt", "handoff", "no_human_review", "wrong_evidence", "valid_order", "already_paid"} {
		t.Run(name, func(t *testing.T) {
			now := time.Now().UTC()
			p := domain.Payment{ID: 7, Provider: domain.ProviderWeChatPay, Channel: domain.ChannelH5Official, MerchantOrderNo: "v3pay_" + strings.Repeat("a", 32), Status: domain.StatusAwaitingPrepay, EffectID: "eer_21", PayerCustomerID: 11, Version: 2}
			st := &restartStore{abandonStore: &abandonStore{storeStub: &storeStub{payment: p}, abandoned: true}}
			rd := stoppedReader{prepayReadStub: &prepayReadStub{projection: effectport.Projection{ID: p.EffectID, Owner: effectport.OwnerPayment, Kind: effectport.KindWeChatPayPrepay, State: effectport.StateUnknown, AttemptCount: 1}}, completed: now.Add(-131 * time.Minute)}
			switch name {
			case "too_recent":
				rd.completed = now.Add(-129 * time.Minute)
			case "not_completed":
				rd.completed = time.Time{}
			case "queued":
				rd.projection.State = effectport.StateQueued
			case "second_attempt":
				rd.projection.AttemptCount = 2
			case "handoff":
				st.handoff = true
			case "no_human_review":
				st.abandoned = false
			case "wrong_evidence":
				st.reviewErr = paymentport.ErrConflict
			case "valid_order":
				st.payment.MerchantOrderNo = strings.Repeat("a", 32)
			case "already_paid":
				st.payment.Status = domain.StatusPaid
			}
			before := st.payment
			svc := NewService(uowStub{}, st, orderStub{}, sessionStub{}, &effectStub{}, rd)
			svc.now = func() time.Time { return now }
			err := svc.AllowCheckoutRestart(context.Background(), paymentport.AbandonCheckoutCommand{PaymentID: 7, ActorScope: "admin:1", EvidenceDigest: strings.Repeat("a", 64), ConfirmedNoDebit: true})
			if name == "eligible" {
				if err != nil || !st.allowed {
					t.Fatalf("eligible: %v", err)
				}
			} else if err == nil || st.allowed {
				t.Fatal("unsafe restart allowed")
			}
			if st.payment != before {
				t.Fatal("original payment reassigned or settled")
			}
		})
	}
}
