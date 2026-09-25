package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	orderdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	paymentprovider "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/provider"
	paymentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/store"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// postgresRefundEffects is a transaction-local stand-in for the Effects Port.
// It persists only a queued fixture effect so Payment's real UoW/FK boundary is
// exercised without provider I/O or a worker.
type postgresRefundEffects struct{}

func (postgresRefundEffects) AcceptAndQueueWithin(ctx context.Context, command effectport.AcceptCommand) (effectport.Projection, effectport.Receipt, error) {
	if !command.Valid() {
		return effectport.Projection{}, effectport.Receipt{}, errors.New("invalid effect command")
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return effectport.Projection{}, effectport.Receipt{}, err
	}
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO external_effects(owner,kind,source_ref_digest,target_ref_digest,payload_digest,policy_version_hash,envelope_fingerprint,state) VALUES($1,$2,$3,$4,$5,$6,$7,'queued') RETURNING id`, command.Envelope.Owner, command.Envelope.Kind, command.Envelope.SourceRefDigest, command.Envelope.TargetRefDigest, command.Envelope.PayloadDigest, command.Envelope.PolicyVersionHash, command.Envelope.Fingerprint()).Scan(&id)
	if err != nil {
		return effectport.Projection{}, effectport.Receipt{}, err
	}
	return effectport.Projection{ID: fmt.Sprintf("eer_%d", id), Owner: command.Envelope.Owner, Kind: command.Envelope.Kind, State: effectport.StateQueued, Generation: 1, UpdatedAt: time.Now().UTC()}, effectport.Receipt{}, nil
}

type profitSharingReconcilerSequence struct {
	results []paymentport.ProfitSharingProviderResult
	next    int
}

// paymentReceiverStatusObserver is a Payment-port test double. Cross-domain
// projection is covered by cmd/aicrm's composed PostgreSQL journey; Payment
// only verifies that it emits the bounded stable-port snapshot.
type paymentReceiverStatusObserver struct {
	values []paymentport.ReceiverReadiness
}

type postgresOrderSettlementRecorder struct {
	orderStub
	settlementCount int
}

func (r *postgresOrderSettlementRecorder) SettlePaymentWithin(ctx context.Context, command orderport.PaymentSettlementCommand) (orderdomain.Snapshot, error) {
	if _, err := platformpostgres.RequireTransaction(ctx); err != nil {
		return orderdomain.Snapshot{}, err
	}
	r.settlementCount++
	return orderdomain.Snapshot{ID: command.OrderID}, nil
}

func (o *paymentReceiverStatusObserver) SyncProfitSharingReceiverStatusWithin(_ context.Context, value paymentport.ReceiverReadiness) error {
	o.values = append(o.values, value)
	return nil
}

func (sequence *profitSharingReconcilerSequence) QueryProfitSharing(context.Context, string) (paymentport.ProfitSharingProviderResult, error) {
	if sequence == nil || sequence.next >= len(sequence.results) {
		return paymentport.ProfitSharingProviderResult{}, errors.New("unexpected profit-sharing query")
	}
	result := sequence.results[sequence.next]
	sequence.next++
	return result, nil
}

func (sequence *profitSharingReconcilerSequence) QueryProfitSharingUnfreeze(context.Context, string) (paymentport.ProfitSharingProviderResult, error) {
	return paymentport.ProfitSharingProviderResult{}, errors.New("unexpected profit-sharing unfreeze query")
}

func TestPostgreSQLWeChatPayRefundSerializesNewKeysPerPayment(t *testing.T) {
	pool, cleanup := paymentAppIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	wrapped, err := platformpostgres.Wrap(pool, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	repository := paymentstore.NewPostgreSQL()
	now := time.Date(2026, 9, 12, 7, 0, 0, 0, time.UTC)
	var orderID, paymentID int64
	if err = pool.QueryRow(ctx, `INSERT INTO orders(provider,source_system,source_key,merchant_order_no,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,record_origin,effect_eligible,created_at,updated_at) VALUES('wechat_pay','refund-concurrency','native-refund-concurrency','M-refund-concurrency',11,11,1000,'CNY','paid','native',true,$1,$1) RETURNING id`, now).Scan(&orderID); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `INSERT INTO payments(order_id,provider,payment_channel,merchant_order_no,payer_identity_id,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,version,created_at,updated_at) VALUES($1,'wechat_pay','mini_program','M-refund-concurrency',4,11,11,1000,'CNY','paid',1,$2,$2) RETURNING id`, orderID, now).Scan(&paymentID); err != nil {
		t.Fatal(err)
	}
	service := NewService(uow, repository, orderStub{}, sessionStub{}, postgresRefundEffects{})
	commands := []paymentport.RefundCommand{
		{PaymentID: paymentID, AmountMinor: 100, RefundNo: "RF-concurrency-1", Reason: "fixture refund", ActorScope: "admin:17", IdempotencyKey: "refund-concurrency-key-0001"},
		{PaymentID: paymentID, AmountMinor: 200, RefundNo: "RF-concurrency-2", Reason: "fixture refund", ActorScope: "admin:18", IdempotencyKey: "refund-concurrency-key-0002"},
	}
	start := make(chan struct{})
	type result struct {
		command paymentport.RefundCommand
		refund  domain.Refund
		err     error
	}
	results := make(chan result, len(commands))
	var wait sync.WaitGroup
	for _, command := range commands {
		command := command
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			refund, requestErr := service.RequestRefund(ctx, command)
			results <- result{command: command, refund: refund, err: requestErr}
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	var accepted result
	acceptedCount, conflictCount := 0, 0
	for result := range results {
		if result.err == nil {
			acceptedCount++
			accepted = result
			continue
		}
		if errors.Is(result.err, paymentport.ErrConflict) {
			conflictCount++
			continue
		}
		t.Fatalf("unexpected concurrent refund error: %v", result.err)
	}
	if acceptedCount != 1 || conflictCount != 1 || accepted.refund.ID < 1 {
		t.Fatalf("accepted=%d conflicts=%d receipt=%+v", acceptedCount, conflictCount, accepted.refund)
	}
	var refundCount int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM payment_refunds WHERE payment_id=$1`, paymentID).Scan(&refundCount); err != nil || refundCount != 1 {
		t.Fatalf("refunds=%d err=%v", refundCount, err)
	}

	// An original-key replay stays valid even while its external outcome is not
	// terminal; it must not be mistaken for a second refund command.
	replayed, err := service.RequestRefund(ctx, accepted.command)
	if err != nil || replayed.ID != accepted.refund.ID {
		t.Fatalf("replay=%+v accepted=%+v err=%v", replayed, accepted.refund, err)
	}

	// A genuine terminal result permits a later explicit partial refund with a
	// different key. The amount gate still applies through ReservedRefundMinor.
	if err = uow.Within(ctx, func(tx context.Context) error {
		current, inner := repository.GetRefund(tx, accepted.refund.ID, true)
		if inner != nil {
			return inner
		}
		terminal, inner := current.Complete(current.Version, domain.RefundFinalFailed, current.UpdatedAt.Add(time.Minute))
		if inner != nil {
			return inner
		}
		_, inner = repository.UpdateRefundSettlement(tx, terminal, string(effectport.Hash("fixture-terminal-refund", accepted.refund.RefundNo)), "fixture-terminal-refund")
		return inner
	}); err != nil {
		t.Fatal(err)
	}
	later, err := service.RequestRefund(ctx, paymentport.RefundCommand{PaymentID: paymentID, AmountMinor: 300, RefundNo: "RF-concurrency-terminal-next", Reason: "fixture later partial", ActorScope: "admin:19", IdempotencyKey: "refund-concurrency-key-0003"})
	if err != nil || later.ID < 1 || later.ID == accepted.refund.ID {
		t.Fatalf("later=%+v original=%+v err=%v", later, accepted.refund, err)
	}
}

func TestPostgreSQLRefundQueryBeforeCallbackKeepsOneSettlement(t *testing.T) {
	pool, cleanup := paymentAppIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	wrapped, err := platformpostgres.Wrap(pool, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)
	var orderID, paymentID, refundID int64
	if err = pool.QueryRow(ctx, `INSERT INTO orders(provider,source_system,source_key,merchant_order_no,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,record_origin,effect_eligible,created_at,updated_at) VALUES('wechat_pay','refund-callback-order','refund-callback-order-1','M-refund-query-first',11,11,1000,'CNY','paid','native',true,$1,$1) RETURNING id`, now).Scan(&orderID); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `INSERT INTO payments(order_id,provider,payment_channel,merchant_order_no,payer_identity_id,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,version,created_at,updated_at) VALUES($1,'wechat_pay','mini_program','M-refund-query-first',4,11,11,1000,'CNY','paid',1,$2,$2) RETURNING id`, orderID, now).Scan(&paymentID); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `INSERT INTO payment_refunds(payment_id,provider,refund_no,amount_minor,reason,status,version,created_at,updated_at) VALUES($1,'wechat_pay','R-refund-query-first',300,'fixture refund','effect_accepted',2,$2,$2) RETURNING id`, paymentID, now).Scan(&refundID); err != nil {
		t.Fatal(err)
	}
	providerDigest := effectport.Hash("wechatpay.refund", "provider-refund-query-first")
	orders := &postgresOrderSettlementRecorder{}
	service := NewService(uow, paymentstore.NewPostgreSQL(), orders, sessionStub{}, postgresRefundEffects{})
	if err = service.SetWeChatPayReconciler(payReconcilerStub{refund: paymentport.WeChatPayRefundQuery{
		RefundNo: "R-refund-query-first", Currency: "CNY", Status: "SUCCESS", AmountMinor: 300, TotalMinor: 1000,
		OccurredAt: now.Add(time.Minute), EvidenceDigest: effectport.Hash("wechatpay.refund.query", "R-refund-query-first"), RefundDigest: providerDigest,
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err = service.ReconcileWeChatPayRefund(ctx, refundID); err != nil {
		t.Fatalf("provider query settlement: %v", err)
	}
	callback := paymentprovider.CallbackResult{
		Kind: "refund", RefundNo: "R-refund-query-first", AmountMinor: 300, Currency: "CNY", OccurredAt: now.Add(time.Minute),
		ProviderRefundDigest: string(providerDigest), EventDigest: [32]byte{7}, BodyDigest: [32]byte{8},
	}
	if err = service.ApplyVerifiedCallback(ctx, callback); err != nil {
		t.Fatalf("late verified callback: %v", err)
	}
	if err = service.ApplyVerifiedCallback(ctx, callback); err != nil {
		t.Fatalf("repeated verified callback: %v", err)
	}
	var status, digest, callbackOutcome string
	var version, callbackCount int
	if err = pool.QueryRow(ctx, `SELECT status,provider_refund_digest,version FROM payment_refunds WHERE id=$1`, refundID).Scan(&status, &digest, &version); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*),max(outcome) FROM payment_callback_receipts WHERE refund_id=$1`, refundID).Scan(&callbackCount, &callbackOutcome); err != nil {
		t.Fatal(err)
	}
	if status != "completed" || digest != string(providerDigest) || version != 3 || callbackCount != 1 || callbackOutcome != "replayed" || orders.settlementCount != 1 {
		t.Fatalf("query/callback replay status=%s digest=%s version=%d receipts=%d outcome=%s order_settlements=%d", status, digest, version, callbackCount, callbackOutcome, orders.settlementCount)
	}
}

func TestPostgreSQLProfitSharingCompletionUpdatesOnlyMatchingEffect(t *testing.T) {
	pool, cleanup := paymentAppIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	wrapped, err := platformpostgres.Wrap(pool, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 14, 8, 0, 0, 0, time.UTC)
	var effectID, receiverID int64
	if err = pool.QueryRow(ctx, `INSERT INTO external_effects(owner,kind,source_ref_digest,target_ref_digest,payload_digest,policy_version_hash,envelope_fingerprint,state) VALUES('payment',$1,$2,$3,$4,$5,$6,'queued') RETURNING id`, effectport.KindWeChatPayReceiverAdd, effectport.Hash("receiver-source"), effectport.Hash("receiver-target"), effectport.Hash("receiver-payload"), effectport.Hash("receiver-policy"), effectport.Hash("receiver-envelope")).Scan(&effectID); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `INSERT INTO payment_profit_sharing_receivers(customer_id,identity_id,app_id,app_scope,channel,account_digest,state,external_effect_id,version,created_at,updated_at) VALUES(19,29,'wx-app','wechat-app:wx-app','h5_official_account',$1,'accepted',$2,1,$3,$3) RETURNING id`, effectport.Hash("receiver-account"), effectID, now).Scan(&receiverID); err != nil {
		t.Fatal(err)
	}
	service := NewService(uow, paymentstore.NewPostgreSQL(), orderStub{}, sessionStub{}, postgresRefundEffects{})
	service.now = func() time.Time { return now.Add(time.Minute) }
	if err = uow.Within(ctx, func(tx context.Context) error {
		return service.CompleteEffect(tx, fmt.Sprintf("eer_%d", effectID), effectport.Envelope{Owner: effectport.OwnerPayment, Kind: effectport.KindWeChatPayReceiverAdd}, effectport.Attempt{}, effectport.AdapterResult{Completion: effectport.StateExecuted})
	}); err != nil {
		t.Fatal(err)
	}
	var state string
	var version int64
	if err = pool.QueryRow(ctx, `SELECT state,version FROM payment_profit_sharing_receivers WHERE id=$1`, receiverID).Scan(&state, &version); err != nil || state != string(domain.ProfitSharingReceiverReady) || version != 2 {
		t.Fatalf("state=%s version=%d err=%v", state, version, err)
	}
	if err = uow.Within(ctx, func(tx context.Context) error {
		return service.CompleteEffect(tx, fmt.Sprintf("eer_%d", effectID), effectport.Envelope{Owner: effectport.OwnerPayment, Kind: effectport.KindWeChatPayProfitSharing}, effectport.Attempt{}, effectport.AdapterResult{Completion: effectport.StateExecuted})
	}); err == nil {
		t.Fatal("a receiver effect cannot be reused as a split completion")
	}
}

func TestPostgreSQLReceiverPermissionDenialForwardsOnlySafeClassToObserver(t *testing.T) {
	pool, cleanup := paymentAppIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	wrapped, err := platformpostgres.Wrap(pool, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 14, 19, 30, 0, 0, time.UTC)
	observer := &paymentReceiverStatusObserver{}
	service := NewService(uow, paymentstore.NewPostgreSQL(), orderStub{}, sessionStub{}, postgresRefundEffects{})
	service.now = func() time.Time { return now.Add(time.Minute) }
	if err = service.SetProfitSharingReceiverStatusObserver(observer); err != nil {
		t.Fatal(err)
	}

	var effectID, receiverID int64
	if err = pool.QueryRow(ctx, `INSERT INTO external_effects(owner,kind,source_ref_digest,target_ref_digest,payload_digest,policy_version_hash,envelope_fingerprint,state) VALUES('payment',$1,$2,$3,$4,$5,$6,'queued') RETURNING id`, effectport.KindWeChatPayReceiverAdd, effectport.Hash("receiver-denied-source"), effectport.Hash("receiver-denied-target"), effectport.Hash("receiver-denied-payload"), effectport.Hash("receiver-denied-policy"), effectport.Hash("receiver-denied-envelope")).Scan(&effectID); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `INSERT INTO payment_profit_sharing_receivers(customer_id,identity_id,app_id,app_scope,channel,account_digest,state,external_effect_id,version,created_at,updated_at) VALUES(701,702,'wx-receiver-denied','wechat-app:wx-receiver-denied','h5_official_account',$1,'accepted',$2,1,$3,$3) RETURNING id`, effectport.Hash("receiver-denied-account"), effectID, now).Scan(&receiverID); err != nil {
		t.Fatal(err)
	}
	if err = uow.Within(ctx, func(tx context.Context) error {
		return service.CompleteEffect(tx, fmt.Sprintf("eer_%d", effectID), effectport.Envelope{Owner: effectport.OwnerPayment, Kind: effectport.KindWeChatPayReceiverAdd}, effectport.Attempt{Number: 1}, effectport.AdapterResult{Completion: effectport.StateFinalFailed, FailureCode: domain.ProfitSharingReceiverFailureProviderPermissionDenied, CallAttempted: true, RealExternalCallExecuted: true})
	}); err != nil {
		t.Fatal(err)
	}
	var failureClass, state, auditClass string
	if err = pool.QueryRow(ctx, `SELECT r.failure_class,r.state,(SELECT payload->>'failure_class' FROM payment_profit_sharing_audit_events WHERE aggregate_kind='receiver' AND aggregate_id=r.id ORDER BY id DESC LIMIT 1) FROM payment_profit_sharing_receivers r WHERE r.id=$1`, receiverID).Scan(&failureClass, &state, &auditClass); err != nil {
		t.Fatal(err)
	}
	if failureClass != domain.ProfitSharingReceiverFailureProviderPermissionDenied || state != string(domain.ProfitSharingReceiverFinalFailed) || auditClass != domain.ProfitSharingReceiverFailureProviderPermissionDenied {
		t.Fatalf("failure_class=%q state=%q audit_class=%q", failureClass, state, auditClass)
	}
	if len(observer.values) != 1 || observer.values[0].FailureClass != domain.ProfitSharingReceiverFailureProviderPermissionDenied || observer.values[0].Ready || !observer.values[0].OutcomeKnown {
		t.Fatalf("safe receiver observer payload=%+v", observer.values)
	}
}

func TestPostgreSQLProfitSharingUnknownQueryKeepsReceiverMismatchReserveForReconciliation(t *testing.T) {
	pool, cleanup := paymentAppIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	wrapped, err := platformpostgres.Wrap(pool, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 14, 9, 0, 0, 0, time.UTC)
	var orderID, paymentID, receiverID, instructionID int64
	if err = pool.QueryRow(ctx, `INSERT INTO orders(provider,source_system,source_key,merchant_order_no,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,record_origin,effect_eligible,created_at,updated_at) VALUES('wechat_pay','profit-sharing-query','profit-sharing-query-1','M-profit-sharing-query',11,11,1000,'CNY','paid','native',true,$1,$1) RETURNING id`, now).Scan(&orderID); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `INSERT INTO payments(order_id,provider,payment_channel,merchant_order_no,payer_identity_id,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,profit_sharing_marked,provider_transaction_reference,provider_transaction_digest,version,created_at,updated_at) VALUES($1,'wechat_pay','mini_program','M-profit-sharing-query',4,11,11,1000,'CNY','paid',true,'4200000003',$2,1,$3,$3) RETURNING id`, orderID, effectport.Hash("payment-transaction"), now).Scan(&paymentID); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `INSERT INTO payment_profit_sharing_receivers(customer_id,identity_id,app_id,app_scope,channel,account_digest,state,version,created_at,updated_at) VALUES(19,29,'wx-app','wechat-app:wx-app','h5_official_account',$1,'ready',1,$2,$2) RETURNING id`, effectport.Hash("receiver-account"), now).Scan(&receiverID); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `INSERT INTO payment_profit_sharing_instructions(settlement_ref,payment_id,receiver_id,provider_order_no,idempotency_key_digest,source_ref_digest,payload_digest,policy_version_hash,amount_minor,currency,state,deadline_at,version,created_at,updated_at) VALUES('settlement-unknown-1',$1,$2,'v3ps_ABCDEFGHIJKLMNOPQRST',$3,$4,$5,$6,100,'CNY','settling',$7,1,$8,$8) RETURNING id`, paymentID, receiverID, effectport.Hash("instruction-key"), effectport.Hash("instruction-source"), effectport.Hash("instruction-payload"), effectport.Hash("instruction-policy"), now.Add(24*time.Hour), now).Scan(&instructionID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO payment_profit_sharing_reserves(instruction_id,payment_id,amount_minor,state,created_at) VALUES($1,$2,100,'reserved',$3)`, instructionID, paymentID, now); err != nil {
		t.Fatal(err)
	}
	sequence := &profitSharingReconcilerSequence{results: []paymentport.ProfitSharingProviderResult{
		{State: "OUTCOME_UNKNOWN", OccurredAt: now.Add(time.Minute), EvidenceDigest: effectport.Hash("query", "unknown")},
		// FINISHED without exact receiver result is an aggregate terminal
		// response, not proof that this receiver/amount was unpaid.
		{State: "FINISHED", ReceiverConfirmedSuccess: false, OutcomeKnown: true, OccurredAt: now.Add(2 * time.Minute), EvidenceDigest: effectport.Hash("query", "receiver-mismatch")},
		// A later exact CLOSED result is finally proof this receiver/amount
		// was unpaid, so only this observation can release the reserve.
		{State: "FINISHED", ReceiverConfirmedFailure: true, FailureClass: domain.ProfitSharingInstructionFailureReceiverReceiptLimit, OutcomeKnown: true, OccurredAt: now.Add(3 * time.Minute), EvidenceDigest: effectport.Hash("query", "receiver-closed")},
	}}
	service := NewService(uow, paymentstore.NewPostgreSQL(), orderStub{}, sessionStub{}, postgresRefundEffects{})
	if err = service.SetProfitSharingReconciler(sequence); err != nil {
		t.Fatal(err)
	}
	first, err := service.ReconcileProfitSharing(ctx, "psinst_"+fmt.Sprintf("%d", instructionID))
	if err != nil || first.State != string(domain.ProfitSharingOutcomeUnknown) || first.OutcomeKnown || first.ReceiverConfirmedSuccess {
		t.Fatalf("unknown query result=%+v err=%v", first, err)
	}
	second, err := service.ReconcileProfitSharing(ctx, first.Reference)
	if err != nil || second.State != string(domain.ProfitSharingException) || second.OutcomeKnown || second.ReceiverConfirmedSuccess {
		t.Fatalf("receiver mismatch result=%+v err=%v", second, err)
	}
	var reserveState string
	if err = pool.QueryRow(ctx, `SELECT state FROM payment_profit_sharing_reserves WHERE instruction_id=$1`, instructionID).Scan(&reserveState); err != nil || reserveState != "reserved" {
		t.Fatalf("receiver mismatch must retain reserve for reconciliation=%q err=%v", reserveState, err)
	}
	third, err := service.ReconcileProfitSharing(ctx, second.Reference)
	if err != nil || third.State != string(domain.ProfitSharingException) || !third.OutcomeKnown || third.ReceiverConfirmedSuccess {
		t.Fatalf("exact receiver closed result=%+v err=%v", third, err)
	}
	if third.FailureClass != domain.ProfitSharingInstructionFailureReceiverReceiptLimit {
		t.Fatalf("exact receiver closed class=%q", third.FailureClass)
	}
	var persistedFailureClass string
	if err = pool.QueryRow(ctx, `SELECT failure_class FROM payment_profit_sharing_instructions WHERE id=$1`, instructionID).Scan(&persistedFailureClass); err != nil || persistedFailureClass != domain.ProfitSharingInstructionFailureReceiverReceiptLimit {
		t.Fatalf("persisted exact receiver failure class=%q err=%v", persistedFailureClass, err)
	}
	if err = pool.QueryRow(ctx, `SELECT state FROM payment_profit_sharing_reserves WHERE instruction_id=$1`, instructionID).Scan(&reserveState); err != nil || reserveState != "released" {
		t.Fatalf("exact receiver closed must release reserve=%q err=%v", reserveState, err)
	}
}

func TestPostgreSQLProfitSharingFundingTreatsHistoricalInFlightRefundsAsExposure(t *testing.T) {
	pool, cleanup := paymentAppIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	wrapped, err := platformpostgres.Wrap(pool, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
	var orderID, paymentID int64
	if err = pool.QueryRow(ctx, `INSERT INTO orders(provider,source_system,source_key,merchant_order_no,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,record_origin,effect_eligible,created_at,updated_at) VALUES('wechat_pay','history-refund','history-refund-1','M-history-refund',11,11,1000,'CNY','paid','history',false,$1,$1) RETURNING id`, now).Scan(&orderID); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `INSERT INTO payments(order_id,provider,payment_channel,merchant_order_no,payer_identity_id,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,version,created_at,updated_at,historical) VALUES($1,'wechat_pay','mini_program','M-history-refund',4,11,11,1000,'CNY','paid',1,$2,$2,true) RETURNING id`, orderID, now).Scan(&paymentID); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		number string
		amount int64
		status domain.RefundStatus
	}{
		{"HR-requested", 100, domain.RefundHistoryRequested}, {"HR-processing", 200, domain.RefundHistoryProcessing},
	} {
		if _, err = pool.Exec(ctx, `INSERT INTO payment_refunds(payment_id,provider,refund_no,amount_minor,reason,status,version,created_at,updated_at) VALUES($1,'wechat_pay',$2,$3,'history evidence',$4,1,$5,$5)`, paymentID, row.number, row.amount, row.status, now); err != nil {
			t.Fatal(err)
		}
	}
	repository := paymentstore.NewPostgreSQL()
	var funding domain.ProfitSharingFunding
	if err = uow.Within(ctx, func(tx context.Context) error {
		var inner error
		funding, inner = repository.ProfitSharingFunding(tx, paymentID)
		return inner
	}); err != nil {
		t.Fatal(err)
	}
	if funding.RequestedRefundMinor != 100 || funding.ProcessingRefundMinor != 200 || !funding.RefundExposure() {
		t.Fatalf("historical in-flight refunds must block funding: %+v", funding)
	}
}

func paymentAppIntegrationPool(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	url, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL (or legacy DATABASE_URL) is not configured; skipping Payment PostgreSQL integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var random [8]byte
	if _, err = rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	schema := "aicrm_payment_app_test_" + hex.EncodeToString(random[:])
	admin, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		admin.Close(ctx)
		t.Fatal(err)
	}
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		admin.Close(ctx)
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		admin.Close(ctx)
		t.Fatal(err)
	}
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(file), "..", "..", "..")
	for _, name := range []string{"0001_platform.sql", "0002_identity.sql", "0005_external_effects.sql", "0020_order.sql", "0021_payment.sql", "0024_order_product_version.sql", "0025_payment_reconciliation.sql", "0061_product_public_purchase.sql", "0068_payment_session_beneficiary_selection.sql", "0127_payment_historical_refund_states.sql", "0131_payment_historical_unassigned.sql", "0134_payment_history_source_delta.sql", "0140_payment_h5_unionid_verified.sql", "0143_payment_checkout_abandonments.sql", "0144_payment_checkout_restart_permissions.sql", "0156_distribution_profit_sharing_payment.sql", "0157_distribution_core.sql", "0161_payment_paid_confirmation_time.sql", "0163_payment_profit_sharing_receiver_recovery.sql", "0165_payment_profit_sharing_receiver_failure_class.sql", "0166_payment_profit_sharing_instruction_failure_class.sql", "0167_distribution_settlement_not_paid_exception.sql"} {
		raw, readErr := os.ReadFile(filepath.Join(root, "migrations", name))
		if readErr != nil {
			pool.Close()
			admin.Close(ctx)
			t.Fatal(readErr)
		}
		if _, err = pool.Exec(ctx, string(raw)); err != nil {
			pool.Close()
			admin.Close(ctx)
			t.Fatalf("apply %s: %v", name, err)
		}
	}
	return pool, func() {
		pool.Close()
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_, _ = admin.Exec(cleanup, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close(cleanup)
	}
}
