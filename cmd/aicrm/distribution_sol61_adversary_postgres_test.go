package main

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	distributionapp "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/app"
	distributiondomain "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/domain"
	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
	distributionstore "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/store"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	identityquery "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/query"
	orderapp "github.com/qianlan33333-png/AI-CRM-v3/internal/order/app"
	orderstore "github.com/qianlan33333-png/AI-CRM-v3/internal/order/store"
	paymentapp "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/app"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	paymentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/store"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// These focused review tests use the existing isolated schema fixture and real
// Identity, Order, Payment, and Distribution applications. Only EER acceptance
// is a transactional fixture sink; no effect worker or provider is started.
type sol61DistributionAdversaryFixture struct {
	pool          *pgxpool.Pool
	uow           *platformpostgres.UnitOfWork
	distributions *distributionstore.Repository
	orders        *orderapp.Service
	payments      *paymentapp.Service
	qualification *distributionapp.QualificationService
	qualifying    refundWorkerOrder
	buyer         refundWorkerOrder
	commissionID  int64
	now           time.Time
}

func sol61DistributionAdversary(t *testing.T) sol61DistributionAdversaryFixture {
	t.Helper()
	pool, cleanup := refundWorkerPool(t)
	t.Cleanup(cleanup)
	wrapped, err := platformpostgres.Wrap(pool, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(wrapped.Close)
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	distributions, err := distributionstore.NewPostgreSQL(pool, uow)
	if err != nil {
		t.Fatal(err)
	}
	orderRepository, err := orderstore.NewPostgreSQL(pool, uow)
	if err != nil {
		t.Fatal(err)
	}
	orders := orderapp.NewService(uow, orderRepository)
	payments := paymentapp.NewService(uow, paymentstore.NewPostgreSQL(), nil, nil, settlementWorkerEffects{})
	if err = payments.SetPaymentChannelAppIDs("wx-sol61-adversary", ""); err != nil {
		t.Fatal(err)
	}
	qualification, err := distributionapp.NewQualificationService(identityquery.NewPostgreSQL(), orders, payments)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	paidAt := now.Add(-8 * 24 * time.Hour)
	promoter := refundWorkerCustomer(t, ctx, pool)
	buyer := refundWorkerCustomer(t, ctx, pool)
	qualifying := refundWorkerPaidOrder(t, ctx, pool, promoter, 761, "sol61-qualification", paidAt.Add(-time.Hour))
	order := sol61AdversaryPaidOrder(t, ctx, pool, buyer, 761, "sol61-buyer", 10000, paidAt)
	// The buyer's one-item order was paid 10,000 minor units. Its frozen policy
	// is 10%, with a completed seven-day waiting period.
	if _, err = pool.Exec(ctx, `UPDATE payments SET profit_sharing_marked=true,provider_transaction_reference='4200000000000761',provider_transaction_digest=$2 WHERE id=$1`, order.paymentID, effectport.Hash("sol61-buyer-transaction")); err != nil {
		t.Fatal(err)
	}
	commissionID := refundWorkerCommission(t, ctx, pool, promoter, order.orderID, 761, paidAt)
	for _, statement := range []string{
		`UPDATE distribution_product_policies SET commission_rate_basis_points=1000`,
		`UPDATE distribution_order_attributions SET commission_rate_basis_points=1000`,
		`UPDATE distribution_commissions SET original_item_paid_minor=10000,initial_minor=1000,current_payable_minor=1000,commission_rate_basis_points=1000`,
	} {
		if _, err = pool.Exec(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	var receiverID int64
	if err = pool.QueryRow(ctx, `INSERT INTO payment_profit_sharing_receivers(customer_id,identity_id,app_id,app_scope,channel,account_digest,state,version,created_at,updated_at) VALUES($1,1,'wx-sol61-adversary','wechat-app:wx-sol61-adversary','mini_program',$2,'ready',1,$3,$3) RETURNING id`, promoter, effectport.Hash("sol61-receiver"), paidAt).Scan(&receiverID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE distribution_distributors SET receiver_reference=$2,receiver_app_id='wx-sol61-adversary',receiver_ready=true,receiver_reason='',receiver_checked_at=$3 WHERE customer_id=$1`, promoter, "psrecv_"+strconv.FormatInt(receiverID, 10), paidAt); err != nil {
		t.Fatal(err)
	}
	return sol61DistributionAdversaryFixture{pool: pool, uow: uow, distributions: distributions, orders: orders, payments: payments, qualification: qualification, qualifying: qualifying, buyer: order, commissionID: commissionID, now: now}
}

func sol61AdversaryPaidOrder(t *testing.T, ctx context.Context, pool *pgxpool.Pool, customerID, productID int64, key string, amount int64, paidAt time.Time) refundWorkerOrder {
	t.Helper()
	var orderID, paymentID int64
	merchant := "M-refund-worker-" + key
	if err := pool.QueryRow(ctx, `INSERT INTO orders(provider,source_system,source_key,merchant_order_no,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,record_origin,effect_eligible,version,created_at,updated_at) VALUES('wechat_pay','refund-worker',$1,$2,$3,$3,$5,'CNY','paid','native',true,2,$4,$4) RETURNING id`, key, merchant, customerID, paidAt, amount).Scan(&orderID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO order_items(order_id,line_no,product_id,product_version,product_code,product_name,unit_amount_minor,quantity,line_amount_minor) VALUES($1,1,$2,1,'refund-worker-product','Refund worker product',$3,1,$3)`, orderID, productID, amount); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO order_checkout_snapshots(order_id,product_type,product_id,product_code,product_name,product_version,service_period_duration_days,gross_amount_minor,discount_amount_minor,payable_amount_minor,currency,coupon_applied,coupon_reservation_ref,reserved_at,created_at) VALUES($1,'standard_product',$2,'refund-worker-product','Refund worker product',1,0,$4,0,$4,'CNY',false,'',$3,$3)`, orderID, productID, paidAt, amount); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO order_paid_events(order_id,order_version,source_digest,occurred_at) VALUES($1,2,$2,$3)`, orderID, refundWorkerDigest(key+":paid"), paidAt); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO payments(order_id,provider,payment_channel,merchant_order_no,payer_identity_id,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,profit_sharing_marked,version,paid_confirmed_at,created_at,updated_at) VALUES($1,'wechat_pay','mini_program',$2,1,$3,$3,$5,'CNY','paid',false,1,$4,$4,$4) RETURNING id`, orderID, merchant, customerID, paidAt, amount).Scan(&paymentID); err != nil {
		t.Fatal(err)
	}
	return refundWorkerOrder{orderID: orderID, paymentID: paymentID}
}

type sol61PaymentSnapshotBarrier struct {
	paymentport.DistributionSettlementPort
	observed chan paymentport.DistributionPaymentState
	release  chan struct{}
}

type sol61SettlementContextBarrier struct {
	*distributionstore.Repository
	reads  int
	locked chan struct{}
	resume chan struct{}
}

type sol61SettlementDistributorBarrier struct {
	*distributionstore.Repository
	read   chan struct{}
	resume chan struct{}
}

func (s *sol61SettlementDistributorBarrier) ReadDistributorWithin(ctx context.Context, id int64, lock bool) (distributiondomain.Distributor, distributionport.ReceiverReadiness, error) {
	distributor, receiver, err := s.Repository.ReadDistributorWithin(ctx, id, lock)
	if err == nil {
		close(s.read)
		select {
		case <-s.resume:
		case <-ctx.Done():
			return distributiondomain.Distributor{}, distributionport.ReceiverReadiness{}, ctx.Err()
		}
	}
	return distributor, receiver, err
}

func TestPostgreSQLSol61SettlementDoesNotInvertReceiverProjectionLocks(t *testing.T) {
	f := sol61DistributionAdversary(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	barrier := &sol61SettlementDistributorBarrier{Repository: f.distributions, read: make(chan struct{}), resume: make(chan struct{})}
	due, err := distributionapp.NewSettlementService(f.uow, barrier, f.qualification, f.payments)
	if err != nil {
		t.Fatal(err)
	}
	registration, err := distributionapp.NewRegistrationService(f.uow, f.distributions, f.payments)
	if err != nil {
		t.Fatal(err)
	}
	var receiverID int64
	if err = f.pool.QueryRow(ctx, `SELECT r.id FROM payment_profit_sharing_receivers r JOIN distribution_distributors d ON d.customer_id=r.customer_id JOIN distribution_commissions c ON c.distributor_id=d.id WHERE c.id=$1`, f.commissionID).Scan(&receiverID); err != nil {
		t.Fatal(err)
	}
	receiverLocked, projectReceiver := make(chan struct{}), make(chan struct{})
	completionDone := make(chan error, 1)
	go func() {
		completionDone <- f.uow.Within(ctx, func(tx context.Context) error {
			// Payment's completion takes this exact receiver lock before its
			// stable observer projects readiness to the Distribution owner.
			receiver, err := paymentstore.NewPostgreSQL().GetProfitSharingReceiver(tx, receiverID, true)
			if err != nil {
				return err
			}
			close(receiverLocked)
			select {
			case <-projectReceiver:
			case <-ctx.Done():
				return ctx.Err()
			}
			return registration.SyncProfitSharingReceiverStatusWithin(tx, paymentport.ReceiverReadiness{CustomerID: receiver.CustomerID, Reference: "psrecv_" + strconv.FormatInt(receiver.ID, 10), AppID: receiver.AppID, Ready: true, OutcomeKnown: true, State: "ready", UpdatedAt: f.now})
		})
	}()
	select {
	case <-receiverLocked:
	case <-ctx.Done():
		t.Fatal("Payment receiver lock")
	}
	dueDone := make(chan error, 1)
	go func() { dueDone <- due.RunCommissionDueCheck(ctx, f.commissionID) }()
	select {
	case <-barrier.read:
	case <-ctx.Done():
		t.Fatal("due distributor read")
	}
	close(projectReceiver)
	close(barrier.resume)
	completionErr, dueErr := <-completionDone, <-dueDone
	var snooze *river.JobSnoozeError
	if completionErr != nil || (dueErr != nil && !errors.As(dueErr, &snooze)) {
		t.Fatalf("Payment receiver->Distribution projection and due distributor->receiver cannot both complete: completion=%v due=%v", completionErr, dueErr)
	}
}

func (s *sol61SettlementContextBarrier) ReadSettlementContextWithin(ctx context.Context, id int64) (distributionstore.SettlementContext, error) {
	value, err := s.Repository.ReadSettlementContextWithin(ctx, id)
	s.reads++
	if err == nil && s.reads == 2 {
		close(s.locked)
		select {
		case <-s.resume:
		case <-ctx.Done():
			return distributionstore.SettlementContext{}, ctx.Err()
		}
	}
	return value, err
}

// Real context/credential locks must permit the same product's promotion
// issuance and commission due check to finish. Immutable joined policy facts
// do not require the due check to own the policy write lock.
func TestPostgreSQLSol61SettlementDoesNotInvertPromotionPolicyLocks(t *testing.T) {
	f := sol61DistributionAdversary(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	barrier := &sol61SettlementContextBarrier{Repository: f.distributions, locked: make(chan struct{}), resume: make(chan struct{})}
	due, err := distributionapp.NewSettlementService(f.uow, barrier, f.qualification, f.payments)
	if err != nil {
		t.Fatal(err)
	}
	var distributorID int64
	if err = f.pool.QueryRow(ctx, `SELECT distributor_id FROM distribution_commissions WHERE id=$1`, f.commissionID).Scan(&distributorID); err != nil {
		t.Fatal(err)
	}
	distributorLocked, requestPolicy := make(chan struct{}), make(chan struct{})
	promotionDone := make(chan error, 1)
	go func() {
		promotionDone <- f.uow.Within(ctx, func(tx context.Context) error {
			// These are the actual Owner Store methods and order used by
			// PromotionService.IssuePromotionLink's write decision.
			if _, _, err := f.distributions.ReadDistributorWithin(tx, distributorID, true); err != nil {
				return err
			}
			close(distributorLocked)
			select {
			case <-requestPolicy:
			case <-ctx.Done():
				return ctx.Err()
			}
			_, err := f.distributions.ReadProductPolicyForUpdateWithin(tx, 761, "standard_product")
			return err
		})
	}()
	select {
	case <-distributorLocked:
	case <-ctx.Done():
		t.Fatal("promotion distributor lock")
	}
	dueDone := make(chan error, 1)
	go func() { dueDone <- due.RunCommissionDueCheck(ctx, f.commissionID) }()
	select {
	case <-barrier.locked:
	case <-ctx.Done():
		t.Fatal("due context lock")
	}
	close(requestPolicy)
	close(barrier.resume)
	promotionErr, dueErr := <-promotionDone, <-dueDone
	var snooze *river.JobSnoozeError
	if promotionErr != nil || (dueErr != nil && !errors.As(dueErr, &snooze)) {
		t.Fatalf("promotion distributor->policy and due context->distributor lock order cannot both complete: promotion=%v due=%v", promotionErr, dueErr)
	}
}

func (b *sol61PaymentSnapshotBarrier) DistributionPaymentState(ctx context.Context, orderID int64) (paymentport.DistributionPaymentState, error) {
	state, err := b.DistributionSettlementPort.DistributionPaymentState(ctx, orderID)
	if err != nil {
		return state, err
	}
	select {
	case b.observed <- state:
	case <-ctx.Done():
		return paymentport.DistributionPaymentState{}, ctx.Err()
	}
	select {
	case <-b.release:
		return state, nil
	case <-ctx.Done():
		return paymentport.DistributionPaymentState{}, ctx.Err()
	}
}

func TestPostgreSQLSol61SettlementPricesRefundCommittedAfterSnapshot(t *testing.T) {
	f := sol61DistributionAdversary(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	barrier := &sol61PaymentSnapshotBarrier{DistributionSettlementPort: f.payments, observed: make(chan paymentport.DistributionPaymentState, 1), release: make(chan struct{})}
	service, err := distributionapp.NewSettlementService(f.uow, f.distributions, f.qualification, barrier)
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { result <- service.RunCommissionDueCheck(ctx, f.commissionID) }()
	select {
	case snapshot := <-barrier.observed:
		if snapshot.OriginalMinor != 10000 || snapshot.SuccessfulRefundMinor != 0 {
			t.Fatalf("unexpected pre-refund snapshot: %+v", snapshot)
		}
	case <-ctx.Done():
		t.Fatal("settlement did not reach the snapshot barrier")
	}
	// A successful partial refund commits under the same Payment row lock used
	// by normal refund commands, before settlement acquires its acceptance lock.
	err = f.uow.Within(ctx, func(txctx context.Context) error {
		tx, err := platformpostgres.RequireTransaction(txctx)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(txctx, `SELECT id FROM payments WHERE id=$1 FOR UPDATE`, f.buyer.paymentID); err != nil {
			return err
		}
		_, err = tx.Exec(txctx, `INSERT INTO payment_refunds(payment_id,provider,refund_no,amount_minor,reason,status,version,created_at,updated_at) VALUES($1,'wechat_pay','sol61-snapshot-refund',1000,'synthetic authoritative successful refund','completed',1,$2,$2)`, f.buyer.paymentID, f.now)
		return err
	})
	close(barrier.release)
	if err != nil {
		<-result
		t.Fatal(err)
	}
	runErr := <-result
	var snooze *river.JobSnoozeError
	if runErr != nil && !errors.As(runErr, &snooze) {
		t.Fatalf("settlement after committed refund: %v", runErr)
	}
	var status string
	var refunded, payable int64
	if err = f.pool.QueryRow(ctx, `SELECT status,successful_refund_minor,current_payable_minor FROM distribution_commissions WHERE id=$1`, f.commissionID).Scan(&status, &refunded, &payable); err != nil {
		t.Fatal(err)
	}
	var accepted, effects int
	var acceptedMinor int64
	if err = f.pool.QueryRow(ctx, `SELECT count(*),COALESCE(MAX(amount_minor),0) FROM payment_profit_sharing_instructions`).Scan(&accepted, &acceptedMinor); err != nil {
		t.Fatal(err)
	}
	if err = f.pool.QueryRow(ctx, `SELECT count(*) FROM external_effects WHERE kind='wechat_pay_profit_sharing_order_v1'`).Scan(&effects); err != nil {
		t.Fatal(err)
	}
	t.Logf("refund committed after snapshot: commission=%s recorded_refund=%d payable=%d instructions=%d accepted_minor=%d queued_effects=%d", status, refunded, payable, accepted, acceptedMinor, effects)
	// The shared decision may retry after detecting the stale snapshot or accept
	// the corrected obligation. It must never accept the stale 1,000 payout.
	if acceptedMinor > 900 || accepted > 1 || effects > 1 {
		t.Fatalf("stale refund pricing accepted %d minor units; correct payable=(10000-1000)*1000/10000=900", acceptedMinor)
	}
}

func TestPostgreSQLSol61QualificationRefundRecheckDeduplicatesSameSource(t *testing.T) {
	f := sol61DistributionAdversary(t)
	ctx := context.Background()
	if _, err := f.pool.Exec(ctx, `UPDATE distribution_commissions SET status='paid',paid_minor=current_payable_minor,version=version+1,updated_at=$2 WHERE id=$1`, f.commissionID, f.now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	refundWorkerCompletedRefund(t, ctx, f.pool, f.qualifying.paymentID, "sol61-qualifying-refund", 1000, f.now)
	worker, err := distributionapp.NewRefundService(f.uow, f.distributions, &refundWorkerDue{}, &refundWorkerRechecks{}, f.qualification, f.orders)
	if err != nil {
		t.Fatal(err)
	}
	args := distributionapp.RefundRecheckJobArgs{OrderID: f.qualifying.orderID, State: "successful", OccurredAt: f.now, ReceiptKey: "sol61-qualification-final-refund"}
	for i := 0; i < 2; i++ {
		if err = worker.RunRefundRecheck(ctx, args); err != nil {
			t.Fatalf("immutable qualification refund delivery %d: %v", i+1, err)
		}
	}
	var count int
	var sourceAmount, paidMinor int64
	if err = f.pool.QueryRow(ctx, `SELECT count(*),COALESCE(sum(amount_minor),0) FROM distribution_exceptions WHERE commission_id=$1 AND kind='qualification_revoked_after_paid'`, f.commissionID).Scan(&count, &sourceAmount); err != nil {
		t.Fatal(err)
	}
	if err = f.pool.QueryRow(ctx, `SELECT paid_minor FROM distribution_commissions WHERE id=$1`, f.commissionID).Scan(&paidMinor); err != nil {
		t.Fatal(err)
	}
	t.Logf("same successful qualification-refund source replay: exception_rows=%d source_amount_sum=%d actual_paid=%d", count, sourceAmount, paidMinor)
	// Separate audit observations may be appended, but one immutable refund
	// source must not become two independently open after-sales exception rows.
	if count != 1 {
		t.Fatalf("one immutable qualification refund source opened %d exceptions", count)
	}
	var exceptionID int64
	if err = f.pool.QueryRow(ctx, `SELECT id FROM distribution_exceptions WHERE commission_id=$1`, f.commissionID).Scan(&exceptionID); err != nil {
		t.Fatal(err)
	}
	admin, err := distributionapp.NewAdminService(f.uow, f.distributions, f.payments)
	if err != nil {
		t.Fatal(err)
	}
	if err = admin.RecordMerchantLiability(ctx, distributionport.AdminExceptionCommand{ExceptionID: exceptionID, ExpectedVersion: 1, AmountMinor: 1000, ActorScope: "access:sol61", Reason: "synthetic local accounting observation", IdempotencyKey: "sol61-liability-handled-source"}); err != nil {
		t.Fatal(err)
	}
	if err = worker.RunRefundRecheck(ctx, args); err != nil {
		t.Fatal(err)
	}
	var status string
	if err = f.pool.QueryRow(ctx, `SELECT count(*),max(status) FROM distribution_exceptions WHERE commission_id=$1`, f.commissionID).Scan(&count, &status); err != nil || count != 1 || status != "merchant_liability_recorded" {
		t.Fatalf("handled source replay reopened exception: count=%d status=%s err=%v", count, status, err)
	}
}

func TestPostgreSQLSol61DelayedRefundAfterPaidKeepsPaidHistory(t *testing.T) {
	f := sol61DistributionAdversary(t)
	ctx := context.Background()
	if _, err := f.pool.Exec(ctx, `UPDATE distribution_commissions SET status='paid',paid_minor=1000,updated_at=$2,version=version+1 WHERE id=$1`, f.commissionID, f.now); err != nil {
		t.Fatal(err)
	}
	refundAt := f.now.Add(-2 * time.Minute)
	refundWorkerCompletedRefund(t, ctx, f.pool, f.buyer.paymentID, "sol61-late-paid-refund", 1000, refundAt)
	worker, err := distributionapp.NewRefundService(f.uow, f.distributions, &refundWorkerDue{}, &refundWorkerRechecks{}, f.qualification, f.orders)
	if err != nil {
		t.Fatal(err)
	}
	args := distributionapp.RefundRecheckJobArgs{OrderID: f.buyer.orderID, State: "successful", OccurredAt: refundAt, ReceiptKey: "sol61-late-paid-refund"}
	for replay := 0; replay < 2; replay++ {
		if err = worker.RunRefundRecheck(ctx, args); err != nil {
			t.Fatal(err)
		}
	}
	var paid, payable, refund, exceptionAmount int64
	var exceptions int
	if err = f.pool.QueryRow(ctx, `SELECT c.paid_minor,c.current_payable_minor,c.successful_refund_minor,(SELECT count(*) FROM distribution_exceptions e WHERE e.commission_id=c.id),(SELECT coalesce(sum(amount_minor),0) FROM distribution_exceptions e WHERE e.commission_id=c.id) FROM distribution_commissions c WHERE id=$1`, f.commissionID).Scan(&paid, &payable, &refund, &exceptions, &exceptionAmount); err != nil || paid != 1000 || payable != 900 || refund != 1000 || exceptions != 1 || exceptionAmount != 100 {
		t.Fatalf("delayed after-sales history: paid=%d payable=%d refund=%d exceptions=%d exception_amount=%d err=%v", paid, payable, refund, exceptions, exceptionAmount, err)
	}
}

func TestPostgreSQLSol61DelayedSuccessfulRefundRecheckUsesCurrentTransitionTime(t *testing.T) {
	f := sol61DistributionAdversary(t)
	ctx := context.Background()
	refundAt := f.now.Add(-2 * time.Minute)
	refundWorkerCompletedRefund(t, ctx, f.pool, f.buyer.paymentID, "sol61-delayed-refund", 1000, refundAt)
	// A sibling refund remains requested. The real due worker consequently
	// records a newer payment/refund hold before it reaches successful-refund
	// repricing. Both the older success and newer exposure are valid facts.
	if _, err := f.pool.Exec(ctx, `INSERT INTO payment_refunds(payment_id,provider,refund_no,amount_minor,reason,status,version,created_at,updated_at) VALUES($1,'wechat_pay','sol61-sibling-refund',100,'synthetic requested sibling refund','requested',1,$2,$2)`, f.buyer.paymentID, f.now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	due, err := distributionapp.NewSettlementService(f.uow, f.distributions, f.qualification, f.payments)
	if err != nil {
		t.Fatal(err)
	}
	var snooze *river.JobSnoozeError
	if dueErr := due.RunCommissionDueCheck(ctx, f.commissionID); !errors.As(dueErr, &snooze) {
		t.Fatalf("sibling-refund due check should persist a hold and snooze: %v", dueErr)
	}
	refundWorkerAssertCommission(t, ctx, f.pool, f.commissionID, "held", 0, 1000, "")
	worker, err := distributionapp.NewRefundService(f.uow, f.distributions, &refundWorkerDue{}, &refundWorkerRechecks{}, f.qualification, f.orders)
	if err != nil {
		t.Fatal(err)
	}
	args := distributionapp.RefundRecheckJobArgs{OrderID: f.buyer.orderID, State: "successful", OccurredAt: refundAt, ReceiptKey: "sol61-delayed-successful-refund"}
	if err = worker.RunRefundRecheck(ctx, args); err != nil {
		t.Fatalf("valid successful-refund job cannot reprice a newer held commission: %v", err)
	}
	refundWorkerAssertCommission(t, ctx, f.pool, f.commissionID, "held", 1000, 900, "")
}

var _ paymentport.DistributionSettlementPort = (*sol61PaymentSnapshotBarrier)(nil)
