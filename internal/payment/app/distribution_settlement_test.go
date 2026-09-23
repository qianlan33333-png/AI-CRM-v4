package app

import (
	"testing"
	"time"

	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
)

func TestProfitSharingReplayRejectsAnyFrozenCommandMutation(t *testing.T) {
	request := paymentport.ProfitSharingRequest{
		SettlementRef: "settlement-0001", OriginalPaymentRef: paymentport.PaymentReference(71), RecipientCustomerID: 42,
		AmountMinor: 125, Currency: "CNY", IdempotencyKey: "distribution.settlement.v1:settlement-0001",
		SourceDigest: effectport.Hash("test.source", "a"), PayloadDigest: effectport.Hash("test.payload", "a"), PolicyDigest: effectport.Hash("test.policy", "a"),
	}
	existing := domain.ProfitSharingInstruction{
		PaymentID: 71, AmountMinor: 125, Currency: "CNY",
		IdempotencyKeyDigest: string(effectport.Hash("payment.profit-sharing.idempotency.v1", request.IdempotencyKey)),
		SourceRefDigest:      string(request.SourceDigest), PayloadDigest: string(request.PayloadDigest), PolicyVersionHash: string(request.PolicyDigest),
	}
	receiver := domain.ProfitSharingReceiver{CustomerID: 42}
	if !profitSharingReplayMatches(existing, receiver, request) {
		t.Fatal("identical settlement retry must return the immutable instruction")
	}

	cases := []struct {
		name   string
		mutate func(*paymentport.ProfitSharingRequest)
	}{
		{"recipient", func(v *paymentport.ProfitSharingRequest) { v.RecipientCustomerID = 43 }},
		{"amount", func(v *paymentport.ProfitSharingRequest) { v.AmountMinor++ }},
		{"currency", func(v *paymentport.ProfitSharingRequest) { v.Currency = "USD" }},
		{"original-payment", func(v *paymentport.ProfitSharingRequest) { v.OriginalPaymentRef = paymentport.PaymentReference(72) }},
		{"idempotency-key", func(v *paymentport.ProfitSharingRequest) { v.IdempotencyKey = "distribution.settlement.v1:other" }},
		{"source-digest", func(v *paymentport.ProfitSharingRequest) { v.SourceDigest = effectport.Hash("test.source", "changed") }},
		{"payload-digest", func(v *paymentport.ProfitSharingRequest) {
			v.PayloadDigest = effectport.Hash("test.payload", "changed")
		}},
		{"policy-digest", func(v *paymentport.ProfitSharingRequest) { v.PolicyDigest = effectport.Hash("test.policy", "changed") }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			mutated := request
			test.mutate(&mutated)
			if profitSharingReplayMatches(existing, receiver, mutated) {
				t.Fatalf("changed %s was silently accepted", test.name)
			}
		})
	}
}

func TestDistributionPaymentStateSeparatesHistoricalPaymentFactFromSplitCapability(t *testing.T) {
	now := time.Date(2026, 9, 14, 9, 0, 0, 0, time.UTC)
	paidAt := now
	payment := domain.Payment{ID: 7, Provider: domain.ProviderWeChatPay, Status: domain.StatusPaid, AmountMinor: 100, ProfitSharingMarked: true, ProviderTransactionDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", PaidConfirmedAt: &paidAt, UpdatedAt: now}
	withoutReference := distributionPaymentState(payment, domain.ProfitSharingFunding{}, now)
	if !withoutReference.ConfirmedPaid || withoutReference.SplitCapable {
		t.Fatal("a mapped paid order is qualification evidence, but a digest-only import is never split-capable")
	}
	payment.ProviderTransactionReference = "4200000000000000001"
	withReference := distributionPaymentState(payment, domain.ProfitSharingFunding{}, now)
	if !withReference.ConfirmedPaid || !withReference.SplitCapable {
		t.Fatal("a confirmed native payment with a private transaction reference should be eligible before deadline")
	}
	payment.Historical = true
	historical := distributionPaymentState(payment, domain.ProfitSharingFunding{}, now)
	if !historical.ConfirmedPaid || historical.SplitCapable {
		t.Fatal("historical payment facts qualify but must not create retroactive provider splits")
	}
}

func TestDistributionPaymentStateUsesImmutablePaidConfirmationForDeadline(t *testing.T) {
	now := time.Date(2026, 9, 14, 9, 0, 0, 0, time.UTC)
	expiredPaidAt := now.Add(-31 * 24 * time.Hour)
	payment := domain.Payment{ID: 8, Provider: domain.ProviderWeChatPay, Status: domain.StatusPaid, AmountMinor: 100, ProfitSharingMarked: true, ProviderTransactionReference: "4200000000000000002", ProviderTransactionDigest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", PaidConfirmedAt: &expiredPaidAt, UpdatedAt: now}
	state := distributionPaymentState(payment, domain.ProfitSharingFunding{}, now)
	if state.SplitCapable || !state.DeadlineAt.Equal(expiredPaidAt.Add(profitSharingWindow)) {
		t.Fatalf("refund/reconciliation UpdatedAt extended deadline: %+v", state)
	}
	payment.PaidConfirmedAt = nil
	state = distributionPaymentState(payment, domain.ProfitSharingFunding{}, now)
	if state.SplitCapable || !state.DeadlineAt.IsZero() {
		t.Fatalf("missing immutable paid confirmation must fail closed: %+v", state)
	}
}

func TestProfitSharingUnfreezeReplayRejectsFrozenCommandMutation(t *testing.T) {
	request := paymentport.ProfitSharingUnfreezeRequest{OriginalPaymentRef: paymentport.PaymentReference(71), Reason: "commission cancelled", IdempotencyKey: "distribution.unfreeze.v1:71", SourceDigest: effectport.Hash("unfreeze.source", "a"), PayloadDigest: effectport.Hash("unfreeze.payload", "a"), PolicyDigest: effectport.Hash("unfreeze.policy", "a")}
	existing := domain.ProfitSharingUnfreeze{Reason: request.Reason, IdempotencyKeyDigest: string(effectport.Hash("payment.profit-sharing.unfreeze.idempotency.v1", request.IdempotencyKey)), SourceRefDigest: string(request.SourceDigest), PayloadDigest: string(request.PayloadDigest), PolicyVersionHash: string(request.PolicyDigest)}
	if !profitSharingUnfreezeReplayMatches(existing, request) {
		t.Fatal("identical unfreeze retry must replay")
	}
	for _, test := range []struct {
		name   string
		mutate func(*paymentport.ProfitSharingUnfreezeRequest)
	}{
		{"reason", func(v *paymentport.ProfitSharingUnfreezeRequest) { v.Reason = "different reason" }},
		{"key", func(v *paymentport.ProfitSharingUnfreezeRequest) { v.IdempotencyKey = "distribution.unfreeze.v1:other" }},
		{"source", func(v *paymentport.ProfitSharingUnfreezeRequest) {
			v.SourceDigest = effectport.Hash("unfreeze.source", "changed")
		}},
		{"payload", func(v *paymentport.ProfitSharingUnfreezeRequest) {
			v.PayloadDigest = effectport.Hash("unfreeze.payload", "changed")
		}},
		{"policy", func(v *paymentport.ProfitSharingUnfreezeRequest) {
			v.PolicyDigest = effectport.Hash("unfreeze.policy", "changed")
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := request
			test.mutate(&changed)
			if profitSharingUnfreezeReplayMatches(existing, changed) {
				t.Fatalf("changed %s replayed", test.name)
			}
		})
	}
}
