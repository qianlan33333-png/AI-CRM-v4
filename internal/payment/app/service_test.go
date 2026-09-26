package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	orderdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	paymentprovider "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/provider"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
	"regexp"
	"testing"
	"time"
)

type txKey struct{}
type uowStub struct{}

func (uowStub) Within(ctx context.Context, fn func(context.Context) error) error {
	return fn(context.WithValue(ctx, txKey{}, true))
}

type orderStub struct{ snapshot orderdomain.Snapshot }

func (o orderStub) ReservePaymentWithin(ctx context.Context, _ int64) (orderdomain.Snapshot, error) {
	if ctx.Value(txKey{}) != true {
		return orderdomain.Snapshot{}, errors.New("not in tx")
	}
	return o.snapshot, nil
}

type checkoutOrderStub struct {
	command  orderport.PaymentOrderCommand
	purchase orderport.StandardPurchaseState
	items    []orderdomain.ItemSnapshot
}

func (*checkoutOrderStub) ReservePaymentWithin(context.Context, int64) (orderdomain.Snapshot, error) {
	return orderdomain.Snapshot{}, errors.New("unexpected existing order")
}
func (stub *checkoutOrderStub) CreatePaymentOrderWithin(ctx context.Context, command orderport.PaymentOrderCommand) (orderdomain.Snapshot, error) {
	if ctx.Value(txKey{}) != true {
		return orderdomain.Snapshot{}, errors.New("not in tx")
	}
	stub.command = command
	order := nativeOrder()
	order.Provider = command.Provider
	order.MerchantOrderNo = command.MerchantOrderNo
	order.Amount = orderdomain.Money{AmountMinor: command.UnitAmountMinor, Currency: command.Currency}
	order.Items = append([]orderdomain.ItemSnapshot(nil), stub.items...)
	payerCustomerID := command.PayerCustomerID
	beneficiaryCustomerID := command.BeneficiaryCustomerID
	order.PayerCustomerID = &payerCustomerID
	order.BeneficiaryCustomerID = &beneficiaryCustomerID
	return order, nil
}
func (*checkoutOrderStub) SettlePaymentWithin(context.Context, orderport.PaymentSettlementCommand) (orderdomain.Snapshot, error) {
	return orderdomain.Snapshot{}, nil
}
func (o orderStub) CreatePaymentOrderWithin(ctx context.Context, _ orderport.PaymentOrderCommand) (orderdomain.Snapshot, error) {
	if ctx.Value(txKey{}) != true {
		return orderdomain.Snapshot{}, errors.New("not in tx")
	}
	return o.snapshot, nil
}
func (o orderStub) SettlePaymentWithin(ctx context.Context, command orderport.PaymentSettlementCommand) (orderdomain.Snapshot, error) {
	if ctx.Value(txKey{}) != true || command.OrderID < 1 {
		return orderdomain.Snapshot{}, errors.New("not in tx")
	}
	return o.snapshot, nil
}

type recordingOrderStub struct {
	orderStub
	settlement      *orderport.PaymentSettlementCommand
	settlementCount int
}

func (stub *recordingOrderStub) SettlePaymentWithin(ctx context.Context, command orderport.PaymentSettlementCommand) (orderdomain.Snapshot, error) {
	if ctx.Value(txKey{}) != true || command.OrderID < 1 {
		return orderdomain.Snapshot{}, errors.New("not in tx")
	}
	stub.settlement = &command
	stub.settlementCount++
	return stub.snapshot, nil
}

type sessionStub struct{ actor paymentport.SessionActor }

func (stub sessionStub) ConsumeWithin(ctx context.Context, token string, _ time.Time) (paymentport.SessionActor, error) {
	if ctx.Value(txKey{}) != true || token == "" {
		return paymentport.SessionActor{}, errors.New("not in tx")
	}
	return stub.actor, nil
}
func (stub sessionStub) LookupWithin(ctx context.Context, token string, _ time.Time) (paymentport.SessionActor, error) {
	if ctx.Value(txKey{}) != true || token == "" {
		return paymentport.SessionActor{}, errors.New("not in tx")
	}
	return stub.actor, nil
}
func (stub sessionStub) SelectPayerSelfWithin(ctx context.Context, token string, _ time.Time) (paymentport.SessionActor, error) {
	if ctx.Value(txKey{}) != true || token == "" {
		return paymentport.SessionActor{}, errors.New("not in tx")
	}
	return stub.actor, nil
}

type checkoutReadSessionStub struct {
	actors map[string]paymentport.SessionActor
}

func (stub checkoutReadSessionStub) actor(ctx context.Context, token string) (paymentport.SessionActor, error) {
	if ctx.Value(txKey{}) != true || token == "" {
		return paymentport.SessionActor{}, errors.New("not in tx")
	}
	actor, ok := stub.actors[token]
	if !ok {
		return paymentport.SessionActor{}, paymentport.ErrSessionRequired
	}
	return actor, nil
}
func (stub checkoutReadSessionStub) ConsumeWithin(ctx context.Context, token string, _ time.Time) (paymentport.SessionActor, error) {
	return stub.actor(ctx, token)
}
func (stub checkoutReadSessionStub) LookupWithin(ctx context.Context, token string, _ time.Time) (paymentport.SessionActor, error) {
	return stub.actor(ctx, token)
}
func (stub checkoutReadSessionStub) SelectPayerSelfWithin(ctx context.Context, token string, _ time.Time) (paymentport.SessionActor, error) {
	return stub.actor(ctx, token)
}

type oneShotSessionStub struct {
	actor    paymentport.SessionActor
	consumed bool
}

func (stub *oneShotSessionStub) ConsumeWithin(ctx context.Context, token string, _ time.Time) (paymentport.SessionActor, error) {
	if ctx.Value(txKey{}) != true || token == "" || stub.consumed {
		return paymentport.SessionActor{}, errors.New("session already consumed")
	}
	stub.consumed = true
	return stub.actor, nil
}
func (stub *oneShotSessionStub) LookupWithin(ctx context.Context, token string, _ time.Time) (paymentport.SessionActor, error) {
	if ctx.Value(txKey{}) != true || token == "" {
		return paymentport.SessionActor{}, errors.New("not in tx")
	}
	return stub.actor, nil
}
func (stub *oneShotSessionStub) SelectPayerSelfWithin(ctx context.Context, token string, _ time.Time) (paymentport.SessionActor, error) {
	if ctx.Value(txKey{}) != true || token == "" {
		return paymentport.SessionActor{}, errors.New("not in tx")
	}
	if stub.actor.BeneficiarySelection == paymentport.BeneficiarySelectionUnresolved {
		stub.actor.BeneficiaryCustomerID = stub.actor.PayerCustomerID
		stub.actor.BeneficiarySelection = paymentport.BeneficiarySelectionPayerSelf
	}
	return stub.actor, nil
}

type effectStub struct {
	fail   bool
	within bool
}

func (e *effectStub) Get(_ context.Context, id string) (effectport.Projection, error) {
	return effectport.Projection{ID: id, Owner: effectport.OwnerPayment, Kind: effectport.KindWeChatPayRefund, State: effectport.StateQueued, AttemptCount: 1, UpdatedAt: time.Date(2026, 9, 3, 2, 0, 0, 0, time.UTC)}, nil
}

func (e *effectStub) AcceptAndQueueWithin(ctx context.Context, c effectport.AcceptCommand) (effectport.Projection, effectport.Receipt, error) {
	e.within = ctx.Value(txKey{}) == true
	if e.fail {
		return effectport.Projection{}, effectport.Receipt{}, errors.New("accept failed")
	}
	if !c.Valid() {
		return effectport.Projection{}, effectport.Receipt{}, errors.New("bad")
	}
	return effectport.Projection{ID: "eer_8", State: effectport.StateQueued}, effectport.Receipt{ID: "eerop_1"}, nil
}

type storeStub struct {
	payment                  domain.Payment
	checkoutPayments         map[domain.Provider]domain.Payment
	paymentIntentSnapshot    map[string]any
	refund                   domain.Refund
	shopMaterial             paymentport.ShopRefundMaterial
	bound                    bool
	reserved                 int64
	nonTerminalRefund        bool
	nonTerminalRefundChecks  int
	refundReconciliationID   int64
	paymentReconciliationID  int64
	callbackOutcome          string
	callbackClaims           int
	callbackReplay           bool
	paymentSettlementUpdates int
	refundSettlementUpdates  int
	handoffCalls             int
	recoveryRefund           domain.Refund
	recoveryFound            bool
	recoveryActor            string
	recoveryKey              [32]byte
}

func (s *storeStub) CreatePayment(_ context.Context, p domain.Payment, _, _ [32]byte, _ string) (domain.Payment, bool, error) {
	if s.payment.ID > 0 {
		return s.payment, false, nil
	}
	p.ID = 7
	s.payment = p
	return p, true, nil
}
func (s *storeStub) ReplayPayment(context.Context, [32]byte, [32]byte, string) (domain.Payment, bool, error) {
	return s.payment, s.payment.ID > 0, nil
}
func (s *storeStub) BindPaymentEffect(_ context.Context, p domain.Payment, _ effectport.PaymentV1Intent, snapshot map[string]any) (domain.Payment, error) {
	s.payment = p
	s.paymentIntentSnapshot = snapshot
	s.bound = true
	return p, nil
}
func (s *storeStub) GetPayment(context.Context, int64, bool) (domain.Payment, error) {
	if s.payment.ID < 1 {
		return domain.Payment{}, paymentport.ErrNotFound
	}
	return s.payment, nil
}
func (s *storeStub) GetHandoff(context.Context, int64) (paymentport.Handoff, error) {
	s.handoffCalls++
	return paymentport.Handoff{PaymentID: s.payment.ID, Payload: []byte(`{"appId":"app"}`), ExpiresAt: time.Now().Add(time.Hour)}, nil
}
func (s *storeStub) ReservedRefundMinor(context.Context, int64) (int64, error) {
	return s.reserved, nil
}
func (s *storeStub) ReservedProfitSharingMinor(context.Context, int64) (int64, error) {
	return 0, nil
}
func (s *storeStub) HasNonTerminalRefund(context.Context, int64) (bool, error) {
	s.nonTerminalRefundChecks++
	return s.nonTerminalRefund, nil
}
func (s *storeStub) CreateRefund(_ context.Context, r domain.Refund, _, _ [32]byte, _ string) (domain.Refund, bool, error) {
	r.ID = 9
	s.refund = r
	return r, true, nil
}
func (s *storeStub) ReplayRefund(context.Context, [32]byte, [32]byte, string) (domain.Refund, bool, error) {
	return s.refund, s.refund.ID > 0, nil
}
func (s *storeStub) FindRefundByIdempotencyKey(_ context.Context, key [32]byte, actor string) (domain.Refund, bool, error) {
	s.recoveryKey, s.recoveryActor = key, actor
	return s.recoveryRefund, s.recoveryFound, nil
}
func (s *storeStub) BindRefundEffect(_ context.Context, r domain.Refund, _ effectport.PaymentV1Intent, _ map[string]any) (domain.Refund, error) {
	s.refund = r
	return r, nil
}
func (s *storeStub) GetRefund(context.Context, int64, bool) (domain.Refund, error) {
	return s.refund, nil
}
func (s *storeStub) GetRefundByProviderReference(context.Context, domain.Provider, string, bool) (domain.Refund, error) {
	return s.refund, nil
}
func (s *storeStub) GetShopRefundMaterial(context.Context, int64) (paymentport.ShopRefundMaterial, error) {
	return s.shopMaterial, nil
}
func (s *storeStub) RecordReconciliation(_ context.Context, id int64, _ effectport.Digest, _ string, _ time.Time) (bool, error) {
	s.refundReconciliationID = id
	return true, nil
}
func (s *storeStub) RecordPaymentReconciliation(_ context.Context, id int64, _ effectport.Digest, _ string, _ time.Time) (bool, error) {
	s.paymentReconciliationID = id
	return true, nil
}
func (s *storeStub) UpdatePaymentSettlement(_ context.Context, p domain.Payment, providerDigest, _ string) (domain.Payment, error) {
	s.paymentSettlementUpdates++
	p.ProviderTransactionDigest = providerDigest
	if p.Status == domain.StatusPaid && p.PaidConfirmedAt == nil {
		confirmed := p.UpdatedAt
		p.PaidConfirmedAt = &confirmed
	}
	s.payment = p
	return p, nil
}
func (s *storeStub) UpdateRefundSettlement(_ context.Context, r domain.Refund, _, _ string) (domain.Refund, error) {
	s.refundSettlementUpdates++
	s.refund = r
	return r, nil
}
func (s *storeStub) GetPaymentByMerchant(ctx context.Context, merchant string, lock bool) (domain.Payment, error) {
	return s.GetPaymentByMerchantProvider(ctx, domain.ProviderWeChatPay, merchant, lock)
}
func (s *storeStub) GetPaymentByMerchantProvider(_ context.Context, provider domain.Provider, merchantOrderNo string, _ bool) (domain.Payment, error) {
	if s.checkoutPayments != nil {
		payment, ok := s.checkoutPayments[provider]
		if !ok || payment.MerchantOrderNo != merchantOrderNo {
			return domain.Payment{}, paymentport.ErrNotFound
		}
		return payment, nil
	}
	if s.payment.ID < 1 || s.payment.Provider != provider || s.payment.MerchantOrderNo != merchantOrderNo {
		return domain.Payment{}, paymentport.ErrNotFound
	}
	return s.payment, nil
}

func TestFindRefundRecoveryReceiptRequiresSamePaymentActorAndKey(t *testing.T) {
	key := "refund-recovery-key-0001"
	store := &storeStub{
		payment:        domain.Payment{ID: 7, Provider: domain.ProviderWeChatPay, MerchantOrderNo: "M-recovery-7"},
		recoveryRefund: domain.Refund{ID: 9, PaymentID: 7, Provider: domain.ProviderWeChatPay, RefundNo: "RF-recovery-9", Status: domain.RefundCompleted},
		recoveryFound:  true,
	}
	service := NewService(uowStub{}, store, orderStub{}, sessionStub{}, &effectStub{})
	refund, found, err := service.FindRefundRecoveryReceipt(context.Background(), domain.ProviderWeChatPay, "M-recovery-7", "admin:17", key)
	wantDigest := sha256.Sum256([]byte(key))
	if err != nil || !found || refund.ID != 9 || store.recoveryActor != "admin:17" || store.recoveryKey != wantDigest {
		t.Fatalf("refund=%+v found=%t actor=%q digest=%x err=%v", refund, found, store.recoveryActor, store.recoveryKey, err)
	}
	store.recoveryRefund.PaymentID = 8
	if refund, found, err = service.FindRefundRecoveryReceipt(context.Background(), domain.ProviderWeChatPay, "M-recovery-7", "admin:17", key); err != nil || found || refund.ID != 0 {
		t.Fatalf("cross-payment receipt was exposed: refund=%+v found=%t err=%v", refund, found, err)
	}
	if _, _, err = service.FindRefundRecoveryReceipt(context.Background(), domain.ProviderWeChatPay, "M-recovery-7", "admin:17", "too-short"); !errors.Is(err, paymentport.ErrInvalid) {
		t.Fatalf("short key err=%v", err)
	}
}
func (s *storeStub) ListRefunds(context.Context, int32, int32) ([]paymentport.RefundProjection, int64, error) {
	return nil, 0, nil
}
func (s *storeStub) ListRefundsForPayment(context.Context, domain.Provider, string, int32, int32) ([]paymentport.RefundProjection, int64, error) {
	return nil, 0, nil
}
func (s *storeStub) ListEffectBindings(context.Context, domain.Provider, string) ([]paymentport.EffectProjection, error) {
	return []paymentport.EffectProjection{{EffectID: "eer_8", Kind: effectport.KindWeChatPayRefund}}, nil
}
func (s *storeStub) GetRefundByNumber(context.Context, string, bool) (domain.Refund, error) {
	return s.refund, nil
}
func (s *storeStub) ClaimCallback(_ context.Context, _ string, _ [32]byte, _ [32]byte, _, outcome string, _ int64) (bool, error) {
	s.callbackOutcome = outcome
	s.callbackClaims++
	return s.callbackReplay && s.callbackClaims > 1, nil
}
func (s *storeStub) ImportTerminalPayment(_ context.Context, payment domain.Payment, _ [32]byte, _ string) (domain.Payment, error) {
	payment.ID = 10
	return payment, nil
}
func (s *storeStub) ImportTerminalRefund(_ context.Context, refund domain.Refund, _ [32]byte, _ string) (domain.Refund, error) {
	refund.ID = 11
	return refund, nil
}
func nativeOrder() orderdomain.Snapshot {
	payer, beneficiary := int64(11), int64(22)
	return orderdomain.Snapshot{ID: 3, Provider: orderdomain.ProviderWeChatPay, MerchantOrderNo: "M-3", PayerCustomerID: &payer, BeneficiaryCustomerID: &beneficiary, Amount: orderdomain.Money{AmountMinor: 1000, Currency: "CNY"}, Status: orderdomain.StatusPendingPayment, RecordOrigin: orderdomain.RecordOriginNative, EffectEligible: true}
}
func TestCreateAcceptsPaymentEffectInSameUOWAndReplays(t *testing.T) {
	store := &storeStub{}
	effects := &effectStub{}
	sessions := &oneShotSessionStub{actor: paymentport.SessionActor{PayerIdentityID: 4, PayerCustomerID: 11, BeneficiaryCustomerID: 22, BeneficiarySelection: paymentport.BeneficiarySelectionAdminAssisted}}
	service := NewService(uowStub{}, store, orderStub{nativeOrder()}, sessions, effects)
	service.now = func() time.Time { return time.Date(2026, 9, 3, 1, 0, 0, 0, time.UTC) }
	command := paymentport.CreateCommand{OrderID: 3, SessionToken: "pays_session_token_0000000001", CheckoutSessionBinding: paymentport.CheckoutSessionBinding("pays_session_token_0000000001"), ActorScope: "customer:11", IdempotencyKey: "payment-create-key-0001"}
	first, err := service.Create(context.Background(), command)
	if err != nil || first.EffectID != "eer_8" || !store.bound || !effects.within {
		t.Fatalf("payment=%+v store=%+v effects=%+v err=%v", first, store, effects, err)
	}
	replay, err := service.Create(context.Background(), command)
	if err != nil || replay.ID != first.ID || replay.EffectID != first.EffectID || !sessions.consumed {
		t.Fatalf("replay=%+v err=%v", replay, err)
	}
}

func TestExistingOrderRejectsLegacySessionNewPaymentButAllowsExactReplay(t *testing.T) {
	command := paymentport.CreateCommand{OrderID: 3, SessionToken: "pays_session_token_legacy_000001", CheckoutSessionBinding: paymentport.CheckoutSessionBinding("pays_session_token_legacy_000001"), ActorScope: "customer:11", IdempotencyKey: "payment-legacy-replay-key-0001"}
	legacyActor := paymentport.SessionActor{PayerIdentityID: 4, PayerCustomerID: 11, BeneficiaryCustomerID: 22, BeneficiarySelection: paymentport.BeneficiarySelectionLegacyPrebound}
	store := &storeStub{}
	service := NewService(uowStub{}, store, orderStub{nativeOrder()}, sessionStub{actor: legacyActor}, &effectStub{})
	if _, err := service.Create(context.Background(), command); !errors.Is(err, paymentport.ErrConflict) {
		t.Fatalf("legacy session created new payment err=%v", err)
	}
	store.payment = domain.Payment{ID: 7, OrderID: 3, PayerIdentityID: 4, PayerCustomerID: 11, BeneficiaryCustomerID: 22, Channel: domain.ChannelMiniProgram}
	replay, err := service.Create(context.Background(), command)
	if err != nil || replay.ID != 7 || replay.BeneficiaryCustomerID != 22 {
		t.Fatalf("legacy replay=%+v err=%v", replay, err)
	}
}
func TestCreateFailsClosedWhenAtomicEffectAcceptanceFails(t *testing.T) {
	store := &storeStub{}
	effects := &effectStub{fail: true}
	service := NewService(uowStub{}, store, orderStub{nativeOrder()}, sessionStub{paymentport.SessionActor{PayerIdentityID: 4, PayerCustomerID: 11, BeneficiaryCustomerID: 22, BeneficiarySelection: paymentport.BeneficiarySelectionAdminAssisted}}, effects)
	_, err := service.Create(context.Background(), paymentport.CreateCommand{OrderID: 3, SessionToken: "pays_session_token_0000000002", CheckoutSessionBinding: paymentport.CheckoutSessionBinding("pays_session_token_0000000002"), ActorScope: "customer:11", IdempotencyKey: "payment-create-key-0002"})
	if !errors.Is(err, paymentport.ErrUnavailable) || store.bound {
		t.Fatalf("bound=%v err=%v", store.bound, err)
	}
}

func TestVerifiedCallbackAppIDMustMatchFrozenPaymentChannel(t *testing.T) {
	now := time.Date(2026, 9, 4, 1, 0, 0, 0, time.UTC)
	store := &storeStub{payment: domain.Payment{ID: 7, OrderID: 3, Provider: domain.ProviderWeChatPay, Channel: domain.ChannelH5Official, MerchantOrderNo: "M-7", PayerIdentityID: 4, PayerCustomerID: 11, BeneficiaryCustomerID: 22, AmountMinor: 1000, Currency: "CNY", Status: domain.StatusAwaitingPayment, Version: 2, CreatedAt: now.Add(-time.Hour), UpdatedAt: now}}
	service := NewService(uowStub{}, store, orderStub{nativeOrder()}, sessionStub{}, &effectStub{})
	if err := service.SetPaymentChannelAppIDs("wx-mini", "wx-oa"); err != nil {
		t.Fatal(err)
	}
	callback := paymentprovider.CallbackResult{Kind: "payment", AppID: "wx-mini", MerchantOrderNo: "M-7", ProviderTransactionReference: "tx-payment-callback", ProviderTransactionDigest: string(effectport.Hash("wechatpay.transaction", "tx-payment-callback")), AmountMinor: 1000, Currency: "CNY", OccurredAt: now.Add(time.Minute)}
	if err := service.ApplyVerifiedCallback(context.Background(), callback); !errors.Is(err, paymentport.ErrConflict) {
		t.Fatalf("mismatched callback err=%v", err)
	}
}

func TestVerifiedCallbackReplaysCanonicalPostgreSQLConfirmationTime(t *testing.T) {
	now := time.Date(2026, 9, 16, 1, 0, 0, 0, time.UTC)
	providerConfirmedAt := time.Date(2026, 9, 16, 1, 2, 3, 123456789, time.UTC)
	transactionReference := "tx-postgres-confirmation-time"
	storedConfirmedAt := providerConfirmedAt.Truncate(time.Microsecond)
	store := &storeStub{payment: domain.Payment{
		ID: 7, OrderID: 3, Provider: domain.ProviderWeChatPay, Channel: domain.ChannelMiniProgram,
		MerchantOrderNo: "M-postgres-confirmation-time", AmountMinor: 1000, Currency: "CNY", Status: domain.StatusPaid,
		ProviderTransactionReference: transactionReference, ProviderTransactionDigest: string(effectport.Hash("wechatpay.transaction", transactionReference)),
		PaidConfirmedAt: &storedConfirmedAt, CreatedAt: now.Add(-time.Hour), UpdatedAt: now,
	}}
	service := NewService(uowStub{}, store, orderStub{nativeOrder()}, sessionStub{}, &effectStub{})
	callback := paymentprovider.CallbackResult{
		Kind: "payment", MerchantOrderNo: store.payment.MerchantOrderNo, ProviderTransactionReference: transactionReference,
		ProviderTransactionDigest: string(effectport.Hash("wechatpay.transaction", transactionReference)), AmountMinor: 1000, Currency: "CNY",
		OccurredAt: providerConfirmedAt, EventDigest: [32]byte{1}, BodyDigest: [32]byte{2},
	}
	if err := service.ApplyVerifiedCallback(context.Background(), callback); err != nil {
		t.Fatalf("canonical PostgreSQL replay err=%v", err)
	}
	if store.callbackClaims != 1 || store.callbackOutcome != "replayed" {
		t.Fatalf("callback claims=%d outcome=%q", store.callbackClaims, store.callbackOutcome)
	}

	callback.OccurredAt = providerConfirmedAt.Add(time.Microsecond)
	if err := service.ApplyVerifiedCallback(context.Background(), callback); !errors.Is(err, paymentport.ErrConflict) {
		t.Fatalf("distinct canonical confirmation time err=%v", err)
	}
}

func TestVerifiedRefundCallbackCompletesUnknownOnceAndReplays(t *testing.T) {
	now := time.Date(2026, 9, 14, 8, 0, 0, 0, time.UTC)
	store := &storeStub{
		callbackReplay: true,
		payment:        domain.Payment{ID: 7, OrderID: 3, Provider: domain.ProviderWeChatPay, Channel: domain.ChannelMiniProgram, MerchantOrderNo: "M-refund-callback", AmountMinor: 1000, Currency: "CNY", Status: domain.StatusPaid, Version: 2, CreatedAt: now.Add(-time.Hour), UpdatedAt: now},
		refund:         domain.Refund{ID: 9, PaymentID: 7, Provider: domain.ProviderWeChatPay, RefundNo: "R-refund-callback", AmountMinor: 300, Status: domain.RefundOutcomeUnknown, Version: 4, CreatedAt: now.Add(-time.Hour), UpdatedAt: now},
	}
	service := NewService(uowStub{}, store, &checkoutOrderStub{}, sessionStub{}, &effectStub{})
	callback := paymentprovider.CallbackResult{
		Kind:                 "refund",
		RefundNo:             "R-refund-callback",
		AmountMinor:          300,
		Currency:             "CNY",
		OccurredAt:           now.Add(time.Minute),
		EventDigest:          [32]byte{1},
		BodyDigest:           [32]byte{2},
		ProviderRefundDigest: string(effectport.Hash("wechatpay.refund", "R-refund-callback")),
	}
	if err := service.ApplyVerifiedCallback(context.Background(), callback); err != nil {
		t.Fatal(err)
	}
	if store.refund.Status != domain.RefundCompleted || store.refundSettlementUpdates != 1 || store.callbackClaims != 1 {
		t.Fatalf("first callback refund=%+v updates=%d claims=%d", store.refund, store.refundSettlementUpdates, store.callbackClaims)
	}
	if err := service.ApplyVerifiedCallback(context.Background(), callback); err != nil {
		t.Fatal(err)
	}
	if store.refund.Status != domain.RefundCompleted || store.refundSettlementUpdates != 1 || store.callbackClaims != 2 {
		t.Fatalf("duplicate callback reapplied settlement: refund=%+v updates=%d claims=%d", store.refund, store.refundSettlementUpdates, store.callbackClaims)
	}
}

func TestRefundReservesOutstandingAmountsAndRejectsOverRefund(t *testing.T) {
	store := &storeStub{payment: domain.Payment{ID: 7, Provider: domain.ProviderWeChatPay, MerchantOrderNo: "M-7", AmountMinor: 1000, Currency: "CNY", Status: domain.StatusPaid, Version: 2, CreatedAt: time.Now().Add(-time.Hour), UpdatedAt: time.Now().Add(-time.Minute)}, reserved: 800}
	effects := &effectStub{}
	service := NewService(uowStub{}, store, orderStub{}, sessionStub{}, effects, effects)
	_, err := service.RequestRefund(context.Background(), paymentport.RefundCommand{PaymentID: 7, AmountMinor: 201, RefundNo: "R-7", Reason: "customer request", ActorScope: "admin:1", IdempotencyKey: "refund-key-000000001"})
	if !errors.Is(err, paymentport.ErrConflict) || store.refund.ID != 0 {
		t.Fatalf("refund=%+v err=%v", store.refund, err)
	}
}

func TestWeChatPayRefundRejectsNewKeyWhileEarlierRefundIsNonTerminal(t *testing.T) {
	now := time.Date(2026, 9, 12, 5, 0, 0, 0, time.UTC)
	store := &storeStub{payment: domain.Payment{ID: 7, Provider: domain.ProviderWeChatPay, MerchantOrderNo: "M-7", AmountMinor: 1000, Currency: "CNY", Status: domain.StatusPaid, Version: 2, CreatedAt: now.Add(-time.Hour), UpdatedAt: now}, nonTerminalRefund: true}
	service := NewService(uowStub{}, store, orderStub{}, sessionStub{}, &effectStub{})
	_, err := service.RequestRefund(context.Background(), paymentport.RefundCommand{PaymentID: 7, AmountMinor: 100, RefundNo: "R-new-key", Reason: "customer request", ActorScope: "admin:18", IdempotencyKey: "refund-key-new-0000001"})
	if !errors.Is(err, paymentport.ErrConflict) || store.refund.ID != 0 || store.nonTerminalRefundChecks != 1 {
		t.Fatalf("refund=%+v nonterminal_checks=%d err=%v", store.refund, store.nonTerminalRefundChecks, err)
	}
}

func TestWeChatPayRefundAllowsNewKeyAfterEarlierRefundIsTerminal(t *testing.T) {
	now := time.Date(2026, 9, 12, 5, 0, 0, 0, time.UTC)
	store := &storeStub{payment: domain.Payment{ID: 7, Provider: domain.ProviderWeChatPay, MerchantOrderNo: "M-7", AmountMinor: 1000, Currency: "CNY", Status: domain.StatusPaid, Version: 2, CreatedAt: now.Add(-time.Hour), UpdatedAt: now}}
	service := NewService(uowStub{}, store, orderStub{}, sessionStub{}, &effectStub{})
	created, err := service.RequestRefund(context.Background(), paymentport.RefundCommand{PaymentID: 7, AmountMinor: 100, RefundNo: "R-terminal-next", Reason: "customer request", ActorScope: "admin:18", IdempotencyKey: "refund-key-terminal-0001"})
	if err != nil || created.ID != 9 || store.nonTerminalRefundChecks != 1 {
		t.Fatalf("created=%+v nonterminal_checks=%d err=%v", created, store.nonTerminalRefundChecks, err)
	}
}

func TestRefundReplayReturnsExistingBeforeReservedAmountCheck(t *testing.T) {
	now := time.Date(2026, 9, 3, 1, 0, 0, 0, time.UTC)
	store := &storeStub{
		payment:           domain.Payment{ID: 7, Provider: domain.ProviderWeChatPay, MerchantOrderNo: "M-7", AmountMinor: 1000, Currency: "CNY", Status: domain.StatusPaid, Version: 2, CreatedAt: now.Add(-time.Hour), UpdatedAt: now},
		refund:            domain.Refund{ID: 9, PaymentID: 7, Provider: domain.ProviderWeChatPay, RefundNo: "R-7", Reason: "customer request", AmountMinor: 1000, Status: domain.RefundEffectAccepted, EffectID: "eer_9", Version: 2, CreatedAt: now, UpdatedAt: now},
		reserved:          1000,
		nonTerminalRefund: true,
	}
	service := NewService(uowStub{}, store, orderStub{}, sessionStub{}, &effectStub{})
	replay, err := service.RequestRefund(context.Background(), paymentport.RefundCommand{PaymentID: 7, AmountMinor: 1000, RefundNo: "R-7", Reason: "customer request", ActorScope: "admin:1", IdempotencyKey: "refund-key-000000001"})
	if err != nil || replay.ID != 9 || store.nonTerminalRefundChecks != 0 {
		t.Fatalf("refund=%+v nonterminal_checks=%d err=%v", replay, store.nonTerminalRefundChecks, err)
	}
}

func TestGetCheckoutAllowsRenewedSamePayerToReadTerminalWithoutHandoff(t *testing.T) {
	store := &storeStub{payment: domain.Payment{ID: 7, OrderID: 3, Provider: domain.ProviderWeChatPay, Channel: domain.ChannelH5Official, MerchantOrderNo: "M-terminal-7", PayerIdentityID: 4, PayerCustomerID: 11, BeneficiaryCustomerID: 11, AmountMinor: 990, Currency: "CNY", Status: domain.StatusPaid, Version: 3, CreatedAt: time.Now().Add(-time.Hour), UpdatedAt: time.Now()}}
	sessions := checkoutReadSessionStub{actors: map[string]paymentport.SessionActor{
		"renewed-payment-session-token": {PayerIdentityID: 4, PayerCustomerID: 11, BeneficiarySelection: paymentport.BeneficiarySelectionUnresolved, Channel: domain.ChannelH5Official},
		"other-payment-session-token":   {PayerIdentityID: 5, PayerCustomerID: 12, BeneficiarySelection: paymentport.BeneficiarySelectionUnresolved, Channel: domain.ChannelH5Official},
	}}
	service := NewService(uowStub{}, store, orderStub{}, sessions, &effectStub{})
	result, err := service.GetCheckout(context.Background(), domain.ProviderWeChatPay, "M-terminal-7", "renewed-payment-session-token")
	if err != nil || result.Status != domain.StatusPaid || len(result.Payload) != 0 || store.handoffCalls != 0 {
		t.Fatalf("renewed terminal checkout=%+v handoff_calls=%d err=%v", result, store.handoffCalls, err)
	}
	if _, err = service.GetCheckout(context.Background(), domain.ProviderWeChatPay, "M-terminal-7", "other-payment-session-token"); !errors.Is(err, paymentport.ErrConflict) || store.handoffCalls != 0 {
		t.Fatalf("other trusted payer err=%v handoff_calls=%d", err, store.handoffCalls)
	}
}

func TestCheckoutFromProductCreatesOrderAndPaymentInSameUOW(t *testing.T) {
	store := &storeStub{}
	orders := &checkoutOrderStub{}
	products := &checkoutProductStub{product: productport.CheckoutProduct{ID: 5, ProductType: productport.ProductOptionStandard, Code: "course-5", Name: "Course 5", PriceMinor: 8800, Currency: "CNY", Version: 3}}
	sessions := &oneShotSessionStub{actor: paymentport.SessionActor{PayerIdentityID: 4, PayerCustomerID: 11, BeneficiarySelection: paymentport.BeneficiarySelectionUnresolved}}
	service := NewService(uowStub{}, store, orders, sessions, &effectStub{})
	if err := service.SetCheckoutProductReader(products); err != nil {
		t.Fatal(err)
	}
	command := paymentport.CreateCommand{ProductID: 5, ProductType: "standard", BeneficiarySelection: paymentport.BeneficiarySelectionPayerSelf, SessionToken: "pays_session_token_0000000005", CheckoutSessionBinding: paymentport.CheckoutSessionBinding("pays_session_token_0000000005"), ActorScope: "public-checkout", IdempotencyKey: "checkout-product-key-0005"}
	first, err := service.Create(context.Background(), command)
	if err != nil || first.ID != 7 || first.AmountMinor != 8800 || products.calls != 1 || orders.command.ProductID != 5 || orders.command.ProductVersion != 3 || orders.command.BeneficiaryCustomerID != 11 || orders.command.MerchantOrderNo == "" || !sessions.consumed {
		t.Fatalf("payment=%+v product_calls=%d order=%+v consumed=%v err=%v", first, products.calls, orders.command, sessions.consumed, err)
	}
	if !regexp.MustCompile(`^[A-Za-z0-9_-]{6,32}$`).MatchString(orders.command.MerchantOrderNo) {
		t.Fatal("merchant order violates provider contract")
	}
	store.payment.MerchantOrderNo = orders.command.MerchantOrderNo
	replay, err := service.Create(context.Background(), command)
	if err != nil || replay.ID != first.ID || products.calls != 1 {
		t.Fatalf("replay=%+v product_calls=%d err=%v", replay, products.calls, err)
	}
	// A deployed 38-character legacy order must replay unchanged, never dispatch again.
	digest := sha256.Sum256([]byte("payment.checkout.v1\x00" + command.SessionToken + "\x00" + command.IdempotencyKey))
	store.payment.MerchantOrderNo = "v3pay_" + hex.EncodeToString(digest[:16])
	legacy, err := service.Create(context.Background(), command)
	if err != nil || legacy.MerchantOrderNo != store.payment.MerchantOrderNo || products.calls != 1 {
		t.Fatalf("legacy replay failed: %v", err)
	}
}

func TestH5CheckoutRequiresAndFreezesNormalizedMobile(t *testing.T) {
	store := &storeStub{}
	orders := &checkoutOrderStub{}
	products := &checkoutProductStub{product: productport.CheckoutProduct{ID: 5, ProductType: productport.ProductOptionStandard, Code: "course-5", Name: "Course 5", PriceMinor: 8800, Currency: "CNY", Version: 3, RequireMobile: true}}
	sessions := &oneShotSessionStub{actor: paymentport.SessionActor{PayerIdentityID: 4, PayerCustomerID: 11, BeneficiarySelection: paymentport.BeneficiarySelectionUnresolved, Channel: domain.ChannelH5Official}}
	service := NewService(uowStub{}, store, orders, sessions, &effectStub{})
	if err := service.SetCheckoutProductReader(products); err != nil {
		t.Fatal(err)
	}
	command := paymentport.CreateCommand{ProductID: 5, ProductType: "standard", BeneficiarySelection: paymentport.BeneficiarySelectionPayerSelf, SessionToken: "pays_h5_session_token_00000005", CheckoutSessionBinding: paymentport.CheckoutSessionBinding("pays_h5_session_token_00000005"), ActorScope: "public-checkout", IdempotencyKey: "checkout-h5-key-0000005"}
	if _, err := service.Create(context.Background(), command); !errors.Is(err, paymentport.ErrConflict) {
		t.Fatalf("missing mobile err=%v", err)
	}
	command.MobileE164 = "+8613812345678"
	payment, err := service.Create(context.Background(), command)
	if err != nil || payment.Channel != domain.ChannelH5Official || orders.command.MobileE164 != command.MobileE164 {
		t.Fatalf("payment=%+v order=%+v err=%v", payment, orders.command, err)
	}
}

func TestAlipayWapCheckoutPreservesCollectedMobileInOriginalOrder(t *testing.T) {
	store := &storeStub{}
	orders := &checkoutOrderStub{items: []orderdomain.ItemSnapshot{{LineNo: 1, ProductCode: "course-5", ProductName: "Course 5", UnitAmountMinor: 8800, Quantity: 1, LineAmountMinor: 8800}}}
	products := &checkoutProductStub{product: productport.CheckoutProduct{ID: 5, ProductType: productport.ProductOptionStandard, Code: "course-5", Name: "Course 5", PriceMinor: 8800, Currency: "CNY", Version: 3, RequireMobile: true}}
	sessions := &oneShotSessionStub{actor: paymentport.SessionActor{PayerIdentityID: 4, PayerCustomerID: 11, BeneficiarySelection: paymentport.BeneficiarySelectionUnresolved, Channel: domain.ChannelH5Official}}
	service := NewService(uowStub{}, store, orders, sessions, &effectStub{})
	if err := service.SetCheckoutProductReader(products); err != nil {
		t.Fatal(err)
	}
	command := paymentport.CreateCommand{ProductID: 5, ProductType: "standard", Provider: "alipay", Channel: domain.ChannelAlipayWap, MobileE164: "+8613812345678", BeneficiarySelection: paymentport.BeneficiarySelectionPayerSelf, SessionToken: "pays_h5_session_token_00000005", CheckoutSessionBinding: paymentport.CheckoutSessionBinding("pays_h5_session_token_00000005"), ActorScope: "public-checkout", IdempotencyKey: "checkout-alipay-key-0000005"}
	payment, err := service.Create(context.Background(), command)
	if err != nil || orders.command.Provider != orderdomain.ProviderAlipay || payment.Channel != domain.ChannelAlipayWap || orders.command.MobileE164 != command.MobileE164 || store.paymentIntentSnapshot["subject"] != "Course 5" {
		t.Fatalf("payment=%+v order=%+v err=%v", payment, orders.command, err)
	}
}

func TestListOrderEffectsJoinsOnlyPaymentOwnedProjection(t *testing.T) {
	store := &storeStub{}
	effects := &effectStub{}
	service := NewService(uowStub{}, store, orderStub{}, sessionStub{}, effects, effects)
	items, err := service.ListOrderEffects(context.Background(), domain.ProviderWeChatPay, "M-7")
	if err != nil || len(items) != 1 || items[0].State != effectport.StateQueued || items[0].AttemptCount != 1 {
		t.Fatalf("items=%+v err=%v", items, err)
	}
}

type payReconcilerStub struct {
	payment paymentport.WeChatPayPaymentQuery
	refund  paymentport.WeChatPayRefundQuery
}

func (s payReconcilerStub) QueryPayment(context.Context, string) (paymentport.WeChatPayPaymentQuery, error) {
	return s.payment, nil
}
func (s payReconcilerStub) QueryRefund(context.Context, string) (paymentport.WeChatPayRefundQuery, error) {
	return s.refund, nil
}

type shopReconcilerStub struct{ query paymentport.ShopRefundQuery }

func (shopReconcilerStub) ValidateRefundMaterial(context.Context, paymentport.ShopRefundMaterial) error {
	return nil
}

type reconciliationEnqueuerStub struct{ refundID int64 }

func (s *reconciliationEnqueuerStub) EnqueueWithin(ctx context.Context, target paymentport.ReconciliationTarget) error {
	if ctx.Value(txKey{}) != true {
		return errors.New("not in tx")
	}
	s.refundID = target.RefundID
	return nil
}

type checkoutProductStub struct {
	product productport.CheckoutProduct
	calls   int
}

func (stub *checkoutProductStub) ReadCheckoutProductWithin(ctx context.Context, _ productport.ProductOptionType, _ productport.ID) (productport.CheckoutProduct, error) {
	if ctx.Value(txKey{}) != true {
		return productport.CheckoutProduct{}, errors.New("not in tx")
	}
	stub.calls++
	return stub.product, nil
}
func (s shopReconcilerStub) QueryRefund(context.Context, string) (paymentport.ShopRefundQuery, error) {
	return s.query, nil
}

func TestWeChatPayPaymentReconciliationUsesPaymentForeignKey(t *testing.T) {
	updatedAt := time.Date(2026, 9, 3, 1, 0, 0, 0, time.UTC)
	store := &storeStub{payment: domain.Payment{ID: 7, OrderID: 3, Provider: domain.ProviderWeChatPay, MerchantOrderNo: "M-7", AmountMinor: 1000, Currency: "CNY", Status: domain.StatusAwaitingPayment, Version: 2, CreatedAt: updatedAt.Add(-time.Hour), UpdatedAt: updatedAt}}
	service := NewService(uowStub{}, store, orderStub{snapshot: nativeOrder()}, sessionStub{}, &effectStub{})
	if err := service.SetWeChatPayReconciler(payReconcilerStub{payment: paymentport.WeChatPayPaymentQuery{MerchantOrderNo: "M-7", Currency: "CNY", Status: "SUCCESS", TransactionReference: "tx-pay-query-M-7", AmountMinor: 1000, OccurredAt: updatedAt.Add(time.Minute), EvidenceDigest: effectport.Hash("pay-query", "M-7"), TransactionDigest: effectport.Hash("wechatpay.transaction", "tx-pay-query-M-7")}}); err != nil {
		t.Fatal(err)
	}
	result, err := service.ReconcileWeChatPayPayment(context.Background(), 7)
	if err != nil || result.Status != domain.StatusPaid || store.paymentReconciliationID != 7 || store.refundReconciliationID != 0 {
		t.Fatalf("payment=%+v payment_reconciliation=%d refund_reconciliation=%d err=%v", result, store.paymentReconciliationID, store.refundReconciliationID, err)
	}
}

func TestWeChatPayPaymentReconciliationRejectsUnexpectedChannelAppID(t *testing.T) {
	updatedAt := time.Date(2026, 9, 3, 1, 0, 0, 0, time.UTC)
	store := &storeStub{payment: domain.Payment{ID: 7, OrderID: 3, Provider: domain.ProviderWeChatPay, Channel: domain.ChannelMiniProgram, MerchantOrderNo: "M-7", AmountMinor: 1000, Currency: "CNY", Status: domain.StatusAwaitingPayment, Version: 2, CreatedAt: updatedAt.Add(-time.Hour), UpdatedAt: updatedAt}}
	service := NewService(uowStub{}, store, orderStub{snapshot: nativeOrder()}, sessionStub{}, &effectStub{})
	if err := service.SetPaymentChannelAppIDs("expected-mini-program-app", ""); err != nil {
		t.Fatal(err)
	}
	if err := service.SetWeChatPayReconciler(payReconcilerStub{payment: paymentport.WeChatPayPaymentQuery{AppID: "other-app", MerchantOrderNo: "M-7", Currency: "CNY", Status: "SUCCESS", TransactionReference: "tx-pay-query-M-7", AmountMinor: 1000, OccurredAt: updatedAt.Add(time.Minute), EvidenceDigest: effectport.Hash("pay-query", "M-7"), TransactionDigest: effectport.Hash("wechatpay.transaction", "tx-pay-query-M-7")}}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ReconcileWeChatPayPayment(context.Background(), 7); !errors.Is(err, paymentport.ErrConflict) {
		t.Fatalf("unexpected AppID reconcile error=%v", err)
	}
	if store.payment.Status != domain.StatusAwaitingPayment || store.paymentReconciliationID != 0 {
		t.Fatalf("unexpected AppID changed payment=%+v reconciliation=%d", store.payment, store.paymentReconciliationID)
	}
}

func TestWeChatPayTerminalFailureClosesPaymentAndOrderAtomically(t *testing.T) {
	updatedAt := time.Date(2026, 9, 3, 1, 0, 0, 0, time.UTC)
	store := &storeStub{payment: domain.Payment{ID: 7, OrderID: 3, Provider: domain.ProviderWeChatPay, MerchantOrderNo: "M-7", AmountMinor: 1000, Currency: "CNY", Status: domain.StatusAwaitingPayment, Version: 2, CreatedAt: updatedAt.Add(-time.Hour), UpdatedAt: updatedAt}}
	orders := &recordingOrderStub{orderStub: orderStub{snapshot: nativeOrder()}}
	service := NewService(uowStub{}, store, orders, sessionStub{}, &effectStub{})
	if err := service.SetWeChatPayReconciler(payReconcilerStub{payment: paymentport.WeChatPayPaymentQuery{MerchantOrderNo: "M-7", Currency: "CNY", Status: "CLOSED", AmountMinor: 1000, OccurredAt: updatedAt.Add(time.Minute), EvidenceDigest: effectport.Hash("pay-query", "closed-M-7")}}); err != nil {
		t.Fatal(err)
	}
	result, err := service.ReconcileWeChatPayPayment(context.Background(), 7)
	if err != nil || result.Status != domain.StatusFailed || orders.settlement == nil || !orders.settlement.Failed || orders.settlement.OrderID != 3 {
		t.Fatalf("payment=%+v settlement=%+v err=%v", result, orders.settlement, err)
	}
}

func TestWeChatPayClosedRefundReleasesReservationWithoutChangingOrder(t *testing.T) {
	updatedAt := time.Date(2026, 9, 3, 1, 0, 0, 0, time.UTC)
	store := &storeStub{
		payment: domain.Payment{ID: 7, OrderID: 3, Provider: domain.ProviderWeChatPay, MerchantOrderNo: "M-7", AmountMinor: 1000, Currency: "CNY", Status: domain.StatusPaid, Version: 2, CreatedAt: updatedAt.Add(-time.Hour), UpdatedAt: updatedAt},
		refund:  domain.Refund{ID: 9, PaymentID: 7, Provider: domain.ProviderWeChatPay, RefundNo: "R-9", AmountMinor: 500, Status: domain.RefundEffectAccepted, EffectID: "eer_9", Version: 2, CreatedAt: updatedAt.Add(-time.Minute), UpdatedAt: updatedAt},
	}
	orders := &recordingOrderStub{orderStub: orderStub{snapshot: nativeOrder()}}
	service := NewService(uowStub{}, store, orders, sessionStub{}, &effectStub{})
	if err := service.SetWeChatPayReconciler(payReconcilerStub{refund: paymentport.WeChatPayRefundQuery{RefundNo: "R-9", Currency: "CNY", Status: "CLOSED", AmountMinor: 500, TotalMinor: 1000, OccurredAt: updatedAt.Add(time.Minute), EvidenceDigest: effectport.Hash("refund-query", "closed-R-9")}}); err != nil {
		t.Fatal(err)
	}
	result, err := service.ReconcileWeChatPayRefund(context.Background(), 9)
	if err != nil || result.Status != domain.RefundFinalFailed || orders.settlement != nil {
		t.Fatalf("refund=%+v settlement=%+v err=%v", result, orders.settlement, err)
	}
}

func TestShopRefundReconciliationUsesRefundForeignKey(t *testing.T) {
	updatedAt := time.Date(2026, 9, 3, 1, 0, 0, 0, time.UTC)
	store := &storeStub{
		payment:      domain.Payment{ID: 7, OrderID: 3, Provider: domain.ProviderWeChatShop, MerchantOrderNo: "SHOP-7", AmountMinor: 1000, Currency: "CNY", Status: domain.StatusPaid, Version: 2, CreatedAt: updatedAt.Add(-time.Hour), UpdatedAt: updatedAt},
		refund:       domain.Refund{ID: 9, PaymentID: 7, Provider: domain.ProviderWeChatShop, RefundNo: "R-9", AmountMinor: 500, Status: domain.RefundEffectAccepted, EffectID: "eer_9", ProviderRefundReference: "AS-9", Version: 2, CreatedAt: updatedAt.Add(-time.Minute), UpdatedAt: updatedAt},
		shopMaterial: paymentport.ShopRefundMaterial{RefundID: 9, PaymentID: 7, AmountMinor: 500, RefundNo: "R-9", ProviderOrderID: "SHOP-7", ProductID: "P-1", SKUID: "SKU-1", RefundCount: 1, ReasonCode: "10000000", Currency: "CNY"},
	}
	service := NewService(uowStub{}, store, orderStub{snapshot: nativeOrder()}, sessionStub{}, &effectStub{})
	if err := service.SetShopReconciler(shopReconcilerStub{query: paymentport.ShopRefundQuery{AfterSaleID: "AS-9", ProviderOrderID: "SHOP-7", ProductID: "P-1", SKUID: "SKU-1", Count: 1, AmountMinor: 500, Currency: "CNY", Status: "MERCHANT_REFUND_SUCCESS", OccurredAt: updatedAt.Add(time.Minute), EvidenceDigest: effectport.Hash("shop-query", "AS-9"), ProviderRefundDigest: effectport.Hash("shop-refund", "AS-9")}}); err != nil {
		t.Fatal(err)
	}
	result, err := service.ReconcileShopRefund(context.Background(), 9)
	if err != nil || result.Status != domain.RefundCompleted || store.refundReconciliationID != 9 || store.paymentReconciliationID != 0 {
		t.Fatalf("refund=%+v payment_reconciliation=%d refund_reconciliation=%d err=%v", result, store.paymentReconciliationID, store.refundReconciliationID, err)
	}
}

func TestShopCallbackPersistsQueryRequiredReceiptAndDurableJob(t *testing.T) {
	now := time.Date(2026, 9, 3, 1, 0, 0, 0, time.UTC)
	store := &storeStub{
		refund:       domain.Refund{ID: 9, PaymentID: 7, Provider: domain.ProviderWeChatShop, RefundNo: "R-9", Status: domain.RefundEffectAccepted, ProviderRefundReference: "AS-9", Version: 2, CreatedAt: now.Add(-time.Minute), UpdatedAt: now},
		shopMaterial: paymentport.ShopRefundMaterial{RefundID: 9, ProviderOrderID: "SHOP-7"},
	}
	jobs := &reconciliationEnqueuerStub{}
	service := NewService(uowStub{}, store, orderStub{}, sessionStub{}, &effectStub{})
	if err := service.SetShopReconciler(shopReconcilerStub{}); err != nil {
		t.Fatal(err)
	}
	if err := service.SetReconciliationEnqueuer(jobs); err != nil {
		t.Fatal(err)
	}
	err := service.ApplyVerifiedShopCallback(context.Background(), paymentport.ShopRefundCallback{AfterSaleID: "AS-9", ProviderOrderID: "SHOP-7", Status: "MERCHANT_REFUND_SUCCESS", EventDigest: [32]byte{1}, PayloadDigest: [32]byte{2}, OccurredAt: now})
	if err != nil || jobs.refundID != 9 || store.callbackOutcome != "query_required" {
		t.Fatalf("job_refund=%d outcome=%q err=%v", jobs.refundID, store.callbackOutcome, err)
	}
}

type prepayReadStub struct {
	projection effectport.Projection
	calls      int
}

func (s *prepayReadStub) Get(context.Context, string) (effectport.Projection, error) {
	s.calls++
	return s.projection, nil
}

func TestGetCheckoutExposesUnknownPrepayOnlyToAuthorizedPayer(t *testing.T) {
	store := &storeStub{payment: domain.Payment{ID: 7, OrderID: 3, Provider: domain.ProviderWeChatPay, Channel: domain.ChannelH5Official, MerchantOrderNo: "M-pending-7", PayerIdentityID: 4, PayerCustomerID: 11, BeneficiaryCustomerID: 11, Status: domain.StatusAwaitingPrepay, EffectID: "eer_21"}}
	sessions := checkoutReadSessionStub{actors: map[string]paymentport.SessionActor{
		"authorized-payment-session":  {PayerIdentityID: 4, PayerCustomerID: 11, Channel: domain.ChannelH5Official, BeneficiarySelection: paymentport.BeneficiarySelectionUnresolved},
		"other-payment-session-token": {PayerIdentityID: 5, PayerCustomerID: 12, Channel: domain.ChannelH5Official, BeneficiarySelection: paymentport.BeneficiarySelectionUnresolved},
	}}
	reader := &prepayReadStub{projection: effectport.Projection{ID: "eer_21", Owner: effectport.OwnerPayment, Kind: effectport.KindWeChatPayPrepay, State: effectport.StateUnknown}}
	service := NewService(uowStub{}, store, orderStub{}, sessions, &effectStub{}, reader)
	result, err := service.GetCheckout(context.Background(), domain.ProviderWeChatPay, "M-pending-7", "authorized-payment-session")
	if err != nil || result.PrepayState != effectport.StateUnknown || len(result.Payload) != 0 || store.handoffCalls != 0 {
		t.Fatalf("unexpected checkout result=%+v err=%v", result, err)
	}
	if _, err = service.GetCheckout(context.Background(), domain.ProviderWeChatPay, "M-pending-7", "other-payment-session-token"); !errors.Is(err, paymentport.ErrConflict) || reader.calls != 1 {
		t.Fatalf("unauthorized effect read: calls=%d err=%v", reader.calls, err)
	}
	reader.projection.Owner = effectport.OwnerOutbound
	if _, err = service.GetCheckout(context.Background(), domain.ProviderWeChatPay, "M-pending-7", "authorized-payment-session"); !errors.Is(err, paymentport.ErrUnavailable) {
		t.Fatalf("wrong owner: %v", err)
	}
}

func (o orderStub) ReadStandardPurchaseWithin(context.Context, orderport.StandardPurchaseQuery) (orderport.StandardPurchaseState, error) {
	return orderport.StandardPurchaseState{}, nil
}
func (o *checkoutOrderStub) ReadStandardPurchaseWithin(context.Context, orderport.StandardPurchaseQuery) (orderport.StandardPurchaseState, error) {
	return o.purchase, nil
}

func TestStandardPurchaseGateBlocksOwnedButAllowsPending(t *testing.T) {
	products := &checkoutProductStub{product: productport.CheckoutProduct{ID: 5, ProductType: productport.ProductOptionStandard, Code: "course-5", Name: "Course 5", PriceMinor: 8800, Currency: "CNY", Version: 3}}
	newCommand := func() paymentport.CreateCommand {
		return paymentport.CreateCommand{ProductID: 5, ProductType: "standard", BeneficiarySelection: paymentport.BeneficiarySelectionPayerSelf, SessionToken: "pays_session_token_0000000005", CheckoutSessionBinding: paymentport.CheckoutSessionBinding("pays_session_token_0000000005"), ActorScope: "public-checkout", IdempotencyKey: "checkout-product-key-0005"}
	}
	t.Run("owned remains blocked", func(t *testing.T) {
		store := &storeStub{}
		orders := &checkoutOrderStub{purchase: orderport.StandardPurchaseState{Owned: true}}
		sessions := &oneShotSessionStub{actor: paymentport.SessionActor{PayerIdentityID: 4, PayerCustomerID: 11, BeneficiarySelection: paymentport.BeneficiarySelectionUnresolved}}
		svc := NewService(uowStub{}, store, orders, sessions, &effectStub{})
		_ = svc.SetCheckoutProductReader(products)
		if _, err := svc.Create(context.Background(), newCommand()); !errors.Is(err, paymentport.ErrAlreadyPurchased) {
			t.Fatalf("got %v want %v", err, paymentport.ErrAlreadyPurchased)
		}
		if orders.command.ProductID != 0 || store.payment.ID != 0 || sessions.consumed {
			t.Fatal("owned purchase wrote order/payment or consumed session")
		}
	})
	t.Run("pending allows a new order", func(t *testing.T) {
		store := &storeStub{}
		orders := &checkoutOrderStub{purchase: orderport.StandardPurchaseState{Pending: true}}
		sessions := &oneShotSessionStub{actor: paymentport.SessionActor{PayerIdentityID: 4, PayerCustomerID: 11, BeneficiarySelection: paymentport.BeneficiarySelectionUnresolved}}
		svc := NewService(uowStub{}, store, orders, sessions, &effectStub{})
		_ = svc.SetCheckoutProductReader(products)
		if _, err := svc.Create(context.Background(), newCommand()); err != nil {
			t.Fatalf("pending purchase blocked new checkout: %v", err)
		}
		if orders.command.ProductID != 5 || store.payment.ID == 0 || !sessions.consumed {
			t.Fatal("pending purchase did not create order/payment and consume session")
		}
	})
}

func TestPurchaseStatusTreatsPendingAsAvailable(t *testing.T) {
	orders := &checkoutOrderStub{purchase: orderport.StandardPurchaseState{Pending: true}}
	sessions := &oneShotSessionStub{actor: paymentport.SessionActor{PayerIdentityID: 4, PayerCustomerID: 11, BeneficiarySelection: paymentport.BeneficiarySelectionUnresolved}}
	svc := NewService(uowStub{}, &storeStub{}, orders, sessions, &effectStub{})
	_ = svc.SetCheckoutProductReader(&checkoutProductStub{product: productport.CheckoutProduct{
		ID: 5, ProductType: productport.ProductOptionStandard, Code: "course-5", Name: "Course 5", PriceMinor: 8800, Currency: "CNY", Version: 3,
	}})
	state, err := svc.PurchaseStatus(context.Background(), "pays_session_token_0000000005", "standard", 5)
	if err != nil {
		t.Fatal(err)
	}
	if state.State != "available" || !state.CanPurchase {
		t.Fatalf("pending purchase status=%+v", state)
	}
}

func TestServicePeriodPurchaseRemainsRenewableDespiteStandardOwned(t *testing.T) {
	orders := &checkoutOrderStub{purchase: orderport.StandardPurchaseState{Owned: true, Pending: true}}
	products := &checkoutProductStub{product: productport.CheckoutProduct{ID: 5, ProductType: productport.ProductOptionServicePeriod, Code: "period-5", Name: "Period 5", PriceMinor: 8800, Currency: "CNY", Version: 3, ServicePeriodDurationDays: 30}}
	sessions := &oneShotSessionStub{actor: paymentport.SessionActor{PayerIdentityID: 4, PayerCustomerID: 11, BeneficiarySelection: paymentport.BeneficiarySelectionUnresolved}}
	svc := NewService(uowStub{}, &storeStub{}, orders, sessions, &effectStub{})
	_ = svc.SetCheckoutProductReader(products)
	cmd := paymentport.CreateCommand{ProductID: 5, ProductType: "service_period", BeneficiarySelection: paymentport.BeneficiarySelectionPayerSelf, SessionToken: "pays_session_token_0000000005", CheckoutSessionBinding: paymentport.CheckoutSessionBinding("pays_session_token_0000000005"), ActorScope: "public-checkout", IdempotencyKey: "checkout-product-key-0005"}
	if _, err := svc.Create(context.Background(), cmd); err != nil {
		t.Fatal(err)
	}
	if orders.command.ProductType != "service_period" {
		t.Fatal("period renewal was not created")
	}
}
