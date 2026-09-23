package app

import (
	"context"
	"errors"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	paymentprovider "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/provider"
	"strings"
	"testing"
	"time"
)

type abandonStore struct {
	*storeStub
	abandoned bool
	handoff   bool
}

func (s *abandonStore) GetHandoff(context.Context, int64) (paymentport.Handoff, error) {
	if s.handoff {
		return paymentport.Handoff{Payload: []byte(`{}`)}, nil
	}
	return paymentport.Handoff{}, paymentport.ErrNotFound
}
func (s *abandonStore) CheckoutAbandoned(context.Context, int64) (bool, error) {
	return s.abandoned, nil
}
func (s *abandonStore) RecordCheckoutAbandonment(ctx context.Context, _ paymentport.AbandonCheckoutCommand, _ time.Time) error {
	if ctx.Value(txKey{}) != true {
		return errors.New("missing UoW")
	}
	s.abandoned = true
	return nil
}
func TestAbandonCheckoutPreservesProviderAndPaymentFacts(t *testing.T) {
	for _, scenario := range []string{"eligible", "valid_order", "handoff", "paid", "queued", "not_confirmed", "other_prefix"} {
		t.Run(scenario, func(t *testing.T) {
			p := domain.Payment{ID: 7, Provider: domain.ProviderWeChatPay, Channel: domain.ChannelH5Official, MerchantOrderNo: "v3pay_" + strings.Repeat("a", 32), Status: domain.StatusAwaitingPrepay, EffectID: "eer_21", PayerCustomerID: 11, Version: 2}
			st := &abandonStore{storeStub: &storeStub{payment: p}}
			reader := &prepayReadStub{projection: effectport.Projection{ID: p.EffectID, Owner: effectport.OwnerPayment, Kind: effectport.KindWeChatPayPrepay, State: effectport.StateUnknown}}
			c := paymentport.AbandonCheckoutCommand{PaymentID: 7, ActorScope: "admin:1", EvidenceDigest: strings.Repeat("a", 64), ConfirmedNoDebit: true}
			switch scenario {
			case "valid_order":
				st.payment.MerchantOrderNo = strings.Repeat("a", 32)
			case "handoff":
				st.handoff = true
			case "paid":
				st.payment.Status = domain.StatusPaid
			case "queued":
				reader.projection.State = effectport.StateQueued
			case "not_confirmed":
				c.ConfirmedNoDebit = false
			case "other_prefix":
				st.payment.MerchantOrderNo = "wrong_" + strings.Repeat("a", 32)
			}
			before := st.payment
			svc := NewService(uowStub{}, st, orderStub{}, sessionStub{}, &effectStub{}, reader)
			err := svc.AbandonCheckout(context.Background(), c)
			if scenario == "eligible" {
				if err != nil || !st.abandoned {
					t.Fatalf("eligible: %v", err)
				}
			} else if err == nil || st.abandoned {
				t.Fatal("unsafe abandonment allowed")
			}
			if st.payment != before || st.paymentReconciliationID != 0 || st.callbackOutcome != "" {
				t.Fatal("provider/payment facts mutated")
			}
		})
	}
}

type lineageStub []customerdomain.CustomerID

func (l lineageStub) CanonicalLineage(context.Context, customerdomain.CustomerID) ([]customerdomain.CustomerID, error) {
	return l, nil
}
func TestMergedPayerCanReadAbandonedOriginalWithoutReassignment(t *testing.T) {
	p := domain.Payment{ID: 7, Provider: domain.ProviderWeChatPay, Channel: domain.ChannelH5Official, MerchantOrderNo: "legacy", Status: domain.StatusAwaitingPrepay, PayerIdentityID: 4, PayerCustomerID: 11, BeneficiaryCustomerID: 11, EffectID: "eer_21"}
	st := &abandonStore{storeStub: &storeStub{payment: p}, abandoned: true}
	sessions := checkoutReadSessionStub{actors: map[string]paymentport.SessionActor{"authorized-payment-session": {PayerIdentityID: 4, PayerCustomerID: 22, Channel: domain.ChannelH5Official, BeneficiarySelection: paymentport.BeneficiarySelectionUnresolved}}}
	reader := &prepayReadStub{projection: effectport.Projection{ID: p.EffectID, Owner: effectport.OwnerPayment, Kind: effectport.KindWeChatPayPrepay, State: effectport.StateUnknown}}
	svc := NewService(uowStub{}, st, orderStub{}, sessions, &effectStub{}, reader)
	svc.SetCanonicalLineageReader(lineageStub{22, 11})
	got, err := svc.GetCheckout(context.Background(), "legacy", "authorized-payment-session")
	if err != nil || !got.CheckoutAbandoned || got.Status != p.Status || st.payment != p {
		t.Fatalf("merged read: %+v %v", got, err)
	}
	sessions.actors["authorized-payment-session"] = paymentport.SessionActor{PayerIdentityID: 5, PayerCustomerID: 22, Channel: domain.ChannelH5Official, BeneficiarySelection: paymentport.BeneficiarySelectionUnresolved}
	if _, err = svc.GetCheckout(context.Background(), "legacy", "authorized-payment-session"); !errors.Is(err, paymentport.ErrConflict) {
		t.Fatal("another identity on same customer was allowed")
	}
	sessions.actors["authorized-payment-session"] = paymentport.SessionActor{PayerIdentityID: 4, PayerCustomerID: 22, Channel: domain.ChannelH5Official, BeneficiarySelection: paymentport.BeneficiarySelectionUnresolved}
	svc.SetCanonicalLineageReader(lineageStub{22, 33})
	if _, err = svc.GetCheckout(context.Background(), "legacy", "authorized-payment-session"); !errors.Is(err, paymentport.ErrConflict) {
		t.Fatal("unrelated root was allowed")
	}
}

func TestAbandonedCheckoutStillAcceptsLateVerifiedPayment(t *testing.T) {
	now := time.Now().UTC()
	p := domain.Payment{ID: 7, OrderID: 3, Provider: domain.ProviderWeChatPay, Channel: domain.ChannelH5Official, MerchantOrderNo: "v3pay_" + strings.Repeat("a", 32), Status: domain.StatusAwaitingPrepay, EffectID: "eer_21", AmountMinor: 990, Currency: "CNY", Version: 2, UpdatedAt: now}
	st := &abandonStore{storeStub: &storeStub{payment: p}, abandoned: true}
	svc := NewService(uowStub{}, st, orderStub{nativeOrder()}, sessionStub{}, &effectStub{})
	if err := svc.SetPaymentChannelAppIDs("wx-mini", "wx-oa"); err != nil {
		t.Fatal(err)
	}
	callback := paymentprovider.CallbackResult{Kind: "payment", AppID: "wx-oa", MerchantOrderNo: p.MerchantOrderNo, ProviderTransactionReference: "late-transaction", ProviderTransactionDigest: string(effectport.Hash("wechatpay.transaction", "late-transaction")), AmountMinor: 990, Currency: "CNY", OccurredAt: now.Add(time.Minute)}
	if err := svc.ApplyVerifiedCallback(context.Background(), callback); err != nil || st.payment.Status != domain.StatusPaid || !st.abandoned {
		t.Fatalf("late callback rejected: %v", err)
	}
}
