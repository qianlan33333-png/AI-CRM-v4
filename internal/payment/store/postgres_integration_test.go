package store_test

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	paymentsession "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/session"
	paymentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/store"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func TestPostgreSQLHistoricalPaymentRefundReplayAndProviderScopedOrderNumber(t *testing.T) {
	pool, cleanup := paymentIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	wrapper, err := platformpostgres.Wrap(pool, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	repository := paymentstore.NewPostgreSQL()
	now := time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC)
	var payOrderID, shopOrderID int64
	for index, provider := range []string{"wechat_pay", "wechat_shop"} {
		var id *int64
		if index == 0 {
			id = &payOrderID
		} else {
			id = &shopOrderID
		}
		if err = pool.QueryRow(ctx, `INSERT INTO orders(provider,source_system,source_key,merchant_order_no,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,record_origin,effect_eligible,source_row_digest,created_at,updated_at) VALUES($1,'commerce-history',$2,'same-merchant',11,11,100,'CNY','paid','history',false,$3,$4,$4) RETURNING id`, provider, provider+"-1", make([]byte, 32), now).Scan(id); err != nil {
			t.Fatal(err)
		}
	}
	paidConfirmedAt := now
	payment := domain.Payment{OrderID: payOrderID, Provider: domain.ProviderWeChatPay, MerchantOrderNo: "same-merchant", PayerIdentityID: 4, PayerCustomerID: 11, BeneficiaryCustomerID: 11, AmountMinor: 100, Currency: "CNY", Status: domain.StatusPaid, PaidConfirmedAt: &paidConfirmedAt, Version: 1, CreatedAt: now, UpdatedAt: now}
	missingPaidConfirmation := payment
	missingPaidConfirmation.OrderID = shopOrderID
	missingPaidConfirmation.Provider = domain.ProviderWeChatShop
	missingPaidConfirmation.MerchantOrderNo = "legacy-missing-confirmation"
	missingPaidConfirmation.PaidConfirmedAt = nil
	var legacy domain.Payment
	if err = uow.Within(ctx, func(tx context.Context) error {
		var inner error
		legacy, inner = repository.ImportTerminalPayment(tx, missingPaidConfirmation, [32]byte{9}, "history-missing-paid-confirmation")
		if inner == nil && legacy.PaidConfirmedAt != nil {
			t.Fatalf("legacy payment invented paid confirmation: %+v", legacy)
		}
		return inner
	}); err != nil {
		t.Fatalf("historical paid row without a source confirmation was rejected: %v", err)
	}
	if err = uow.Within(ctx, func(tx context.Context) error {
		replayed, inner := repository.ImportTerminalPayment(tx, missingPaidConfirmation, [32]byte{9}, "history-missing-paid-confirmation")
		if inner == nil && (replayed.ID != legacy.ID || replayed.PaidConfirmedAt != nil) {
			t.Fatalf("legacy payment replay drift: %+v", replayed)
		}
		return inner
	}); err != nil {
		t.Fatalf("legacy payment replay failed: %v", err)
	}
	var persisted domain.Payment
	err = uow.Within(ctx, func(tx context.Context) error {
		var inner error
		persisted, inner = repository.ImportTerminalPayment(tx, payment, [32]byte{1}, "history-run")
		return inner
	})
	if err != nil || persisted.ID < 1 || persisted.EffectID != "" {
		t.Fatalf("payment=%+v err=%v", persisted, err)
	}
	err = uow.Within(ctx, func(tx context.Context) error {
		replay, inner := repository.ImportTerminalPayment(tx, payment, [32]byte{1}, "history-run")
		if inner == nil && replay.ID != persisted.ID {
			t.Fatalf("replay=%+v", replay)
		}
		return inner
	})
	if err != nil {
		t.Fatal(err)
	}
	// A distinct run is a real source refresh, not a same-key replay.
	payment.SourceStatus = "paid"
	payment.UpdatedAt = now.Add(time.Hour)
	err = uow.Within(ctx, func(tx context.Context) error {
		got, e := repository.ImportTerminalPayment(tx, payment, [32]byte{7}, "history-run-next")
		if e == nil && (got.ID != persisted.ID || got.Version != 2) {
			t.Fatalf("cross-run did not reuse payment")
		}
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	changed := payment
	changed.AmountMinor++
	err = uow.Within(ctx, func(tx context.Context) error {
		_, e := repository.ImportTerminalPayment(tx, changed, [32]byte{8}, "history-bad-amount")
		return e
	})
	if !errors.Is(err, paymentport.ErrConflict) {
		t.Fatalf("amount drift accepted: %v", err)
	}
	refund := domain.Refund{PaymentID: persisted.ID, Provider: domain.ProviderWeChatPay, RefundNo: "history-refund", Reason: "历史退款", AmountMinor: 40, Status: domain.RefundCompleted, Version: 1, CreatedAt: now, UpdatedAt: now}
	err = uow.Within(ctx, func(tx context.Context) error {
		_, inner := repository.ImportTerminalRefund(tx, refund, [32]byte{2}, "history-run")
		return inner
	})
	if err != nil {
		t.Fatal(err)
	}
	progress := refund
	progress.RefundNo = "history-progress"
	progress.Status = domain.RefundHistoryFailed
	for index, status := range []domain.RefundStatus{domain.RefundHistoryFailed, domain.RefundHistoryProcessing, domain.RefundCompleted} {
		progress.Status = status
		err = uow.Within(ctx, func(tx context.Context) error {
			_, e := repository.ImportTerminalRefund(tx, progress, [32]byte{byte(20 + index)}, fmt.Sprintf("refund-run-%d", index))
			return e
		})
		if err != nil {
			t.Fatalf("refund transition %s: %v", status, err)
		}
	}
	progress.Status = domain.RefundHistoryProcessing
	err = uow.Within(ctx, func(tx context.Context) error {
		_, e := repository.ImportTerminalRefund(tx, progress, [32]byte{30}, "refund-regression")
		return e
	})
	if !errors.Is(err, paymentport.ErrConflict) {
		t.Fatalf("completed refund regressed: %v", err)
	}
	var deltaCount int
	if e := pool.QueryRow(ctx, `SELECT count(*) FROM payment_history_source_deltas`).Scan(&deltaCount); e != nil || deltaCount != 3 {
		t.Fatalf("delta evidence count=%d err=%v", deltaCount, e)
	}

	for n, status := range []domain.RefundStatus{domain.RefundHistoryRequested, domain.RefundHistoryProcessing, domain.RefundHistoryFailed, domain.RefundHistoryClosed} {
		historical := refund
		historical.RefundNo = fmt.Sprintf("history-status-%d", n)
		historical.Status = status
		if err = uow.Within(ctx, func(tx context.Context) error {
			_, e := repository.ImportTerminalRefund(tx, historical, [32]byte{byte(n + 3)}, "history-run")
			return e
		}); err != nil {
			t.Fatal(err)
		}
		if err = uow.Within(ctx, func(tx context.Context) error {
			saved, e := repository.ImportTerminalRefund(tx, historical, [32]byte{byte(n + 3)}, "history-run")
			if e == nil && saved.Status != status {
				t.Fatal("status lost")
			}
			return e
		}); err != nil {
			t.Fatal(err)
		}
		if _, e := historical.BindEffect(1, "eer_forbidden", now); e == nil {
			t.Fatal("historical status accepted executable effect")
		}
		if _, e := historical.Complete(1, domain.RefundCompleted, now); e == nil {
			t.Fatal("historical status completed through live path")
		}
	}
	var payments, refunds, effects int
	if err = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM payments),(SELECT count(*) FROM payment_refunds),(SELECT count(*) FROM external_effects WHERE owner='payment')`).Scan(&payments, &refunds, &effects); err != nil || payments != 2 || refunds != 6 || effects != 0 {
		t.Fatalf("payments=%d refunds=%d effects=%d err=%v", payments, refunds, effects, err)
	}
}

func TestPostgreSQLRefundExposureExcludesOnlyFinalFailed(t *testing.T) {
	pool, cleanup := paymentIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	wrapper, _ := platformpostgres.Wrap(pool, time.Second)
	uow, _ := platformpostgres.NewUnitOfWork(wrapper)
	repository := paymentstore.NewPostgreSQL()
	now := time.Date(2026, 9, 4, 0, 0, 0, 0, time.UTC)
	orderIDs := make([]int64, 0, 5)
	for index, refundStatus := range []string{"requested", "effect_accepted", "outcome_unknown", "completed", "final_failed"} {
		var orderID, paymentID int64
		key := "refund-exposure-" + string(rune('a'+index))
		if err := pool.QueryRow(ctx, `INSERT INTO orders(provider,source_system,source_key,merchant_order_no,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,record_origin,effect_eligible,source_row_digest,created_at,updated_at) VALUES('wechat_pay','test',$1,$2,11,11,100,'CNY','paid','history',false,$3,$4,$4) RETURNING id`, key, "M-"+key, make([]byte, 32), now).Scan(&orderID); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, `INSERT INTO payments(order_id,provider,payment_channel,merchant_order_no,payer_identity_id,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,version,created_at,updated_at) VALUES($1,'wechat_pay','mini_program',$2,4,11,11,100,'CNY','paid',1,$3,$3) RETURNING id`, orderID, "M-"+key, now).Scan(&paymentID); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO payment_refunds(payment_id,provider,refund_no,amount_minor,reason,status,version,created_at,updated_at) VALUES($1,'wechat_pay',$2,100,'test',$3,1,$4,$4)`, paymentID, "R-"+key, refundStatus, now); err != nil {
			t.Fatal(err)
		}
		orderIDs = append(orderIDs, orderID)
	}
	var exposed map[int64]struct{}
	if err := uow.Within(ctx, func(tx context.Context) error {
		var inner error
		exposed, inner = repository.RefundRelatedOrderIDsWithin(tx, orderIDs)
		return inner
	}); err != nil {
		t.Fatal(err)
	}
	if len(exposed) != 4 {
		t.Fatalf("exposed=%+v", exposed)
	}
	if _, ok := exposed[orderIDs[4]]; ok {
		t.Fatal("final_failed refund reduced sold count")
	}
	var _ paymentport.RefundExposureReader = repository
}

func TestPostgreSQLRefundListScopesExactPayment(t *testing.T) {
	pool, cleanup := paymentIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	wrapper, err := platformpostgres.Wrap(pool, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	repository := paymentstore.NewPostgreSQL()
	now := time.Date(2026, 9, 10, 4, 10, 38, 0, time.UTC)
	type fixture struct {
		merchant string
		orderID  int64
		payment  domain.Payment
	}
	fixtures := []fixture{{merchant: "WXP2609100410381093CE4C5B0C"}, {merchant: "WXP-other"}}
	for index := range fixtures {
		digest := sha256.Sum256([]byte("order-refund-list-" + fixtures[index].merchant))
		if err = pool.QueryRow(ctx, `INSERT INTO orders(provider,source_system,source_key,merchant_order_no,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,record_origin,effect_eligible,source_row_digest,created_at,updated_at) VALUES('wechat_pay','commerce-history',$1,$2,11,11,200000,'CNY','paid','history',false,$3,$4,$4) RETURNING id`, "refund-list:"+fixtures[index].merchant, fixtures[index].merchant, digest[:], now).Scan(&fixtures[index].orderID); err != nil {
			t.Fatal(err)
		}
		paidConfirmedAt := now
		fixtures[index].payment = domain.Payment{OrderID: fixtures[index].orderID, Provider: domain.ProviderWeChatPay, MerchantOrderNo: fixtures[index].merchant, PayerIdentityID: 4, PayerCustomerID: 11, BeneficiaryCustomerID: 11, AmountMinor: 200000, Currency: "CNY", Status: domain.StatusPaid, PaidConfirmedAt: &paidConfirmedAt, Version: 1, CreatedAt: now, UpdatedAt: now}
		if err = uow.Within(ctx, func(tx context.Context) error {
			persisted, inner := repository.ImportTerminalPayment(tx, fixtures[index].payment, [32]byte{byte(index + 1)}, "refund-list-run")
			fixtures[index].payment = persisted
			return inner
		}); err != nil {
			t.Fatal(err)
		}
		if err = uow.Within(ctx, func(tx context.Context) error {
			_, inner := repository.ImportTerminalRefund(tx, domain.Refund{PaymentID: fixtures[index].payment.ID, Provider: domain.ProviderWeChatPay, RefundNo: fmt.Sprintf("refund-list-%d", index), Reason: "历史退款", AmountMinor: 100, Status: domain.RefundCompleted, Version: 1, CreatedAt: now, UpdatedAt: now}, [32]byte{byte(index + 11)}, "refund-list-run")
			return inner
		}); err != nil {
			t.Fatal(err)
		}
	}
	var rows []paymentport.RefundProjection
	var total int64
	if err = uow.Within(ctx, func(tx context.Context) error {
		var inner error
		rows, total, inner = repository.ListRefundsForPayment(tx, domain.ProviderWeChatPay, fixtures[0].merchant, 50, 0)
		return inner
	}); err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(rows) != 1 || rows[0].OrderID != fixtures[0].orderID || rows[0].MerchantOrder != fixtures[0].merchant || rows[0].Refund.PaymentID != fixtures[0].payment.ID {
		t.Fatalf("total=%d rows=%+v", total, rows)
	}
}

func TestPostgreSQLRefundRecoveryReceiptScopesOriginalKeyAndActor(t *testing.T) {
	pool, cleanup := paymentIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	wrapper, err := platformpostgres.Wrap(pool, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	repository := paymentstore.NewPostgreSQL()
	now := time.Date(2026, 9, 12, 3, 0, 0, 0, time.UTC)
	var orderID, paymentID int64
	digest := sha256.Sum256([]byte("refund-recovery-order"))
	if err = pool.QueryRow(ctx, `INSERT INTO orders(provider,source_system,source_key,merchant_order_no,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,record_origin,effect_eligible,source_row_digest,created_at,updated_at) VALUES('wechat_pay','test','refund-recovery-order','M-recovery',11,11,2000,'CNY','paid','history',false,$1,$2,$2) RETURNING id`, digest[:], now).Scan(&orderID); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `INSERT INTO payments(order_id,provider,payment_channel,merchant_order_no,payer_identity_id,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,version,created_at,updated_at) VALUES($1,'wechat_pay','mini_program','M-recovery',4,11,11,2000,'CNY','paid',1,$2,$2) RETURNING id`, orderID, now).Scan(&paymentID); err != nil {
		t.Fatal(err)
	}
	key := sha256.Sum256([]byte("refund-recovery-key-0001"))
	payload := sha256.Sum256([]byte("refund-recovery-payload"))
	var created domain.Refund
	if err = uow.Within(ctx, func(tx context.Context) error {
		var inner error
		created, _, inner = repository.CreateRefund(tx, domain.Refund{PaymentID: paymentID, Provider: domain.ProviderWeChatPay, RefundNo: "RF-recovery", AmountMinor: 1000, Reason: "test", Status: domain.RefundRequested, Version: 1, CreatedAt: now, UpdatedAt: now}, key, payload, "admin:17")
		return inner
	}); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name  string
		key   [32]byte
		actor string
		found bool
	}{{"exact", key, "admin:17", true}, {"different actor", key, "admin:18", false}, {"different key", sha256.Sum256([]byte("refund-recovery-key-0002")), "admin:17", false}} {
		t.Run(test.name, func(t *testing.T) {
			var refund domain.Refund
			var found bool
			err := uow.Within(ctx, func(tx context.Context) error {
				var inner error
				refund, found, inner = repository.FindRefundByIdempotencyKey(tx, test.key, test.actor)
				return inner
			})
			if err != nil || found != test.found || test.found && refund.ID != created.ID {
				t.Fatalf("refund=%+v found=%t want=%t err=%v", refund, found, test.found, err)
			}
		})
	}
}

func TestPostgreSQLPaymentSessionBeneficiaryFactsCASAndCheckoutRollback(t *testing.T) {
	pool, cleanup := paymentIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	wrapper, err := platformpostgres.Wrap(pool, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	repository := paymentsession.NewPostgreSQL()
	now := time.Date(2026, 9, 5, 2, 0, 0, 0, time.UTC)
	expires := now.Add(10 * time.Minute)
	legacyDigest := sha256.Sum256([]byte("legacy-payment-session"))
	newDigest := sha256.Sum256([]byte("new-payment-session"))
	insert := func(digest [32]byte, beneficiary customerdomain.CustomerID, selection paymentport.BeneficiarySelection, selectedAt *time.Time) {
		t.Helper()
		err := uow.Within(ctx, func(tx context.Context) error {
			_, err := repository.Insert(tx, paymentsession.Record{UnionIDVerified: true, TokenDigest: digest, PayerIdentityID: 9, PayerCustomerID: 11, BeneficiaryCustomerID: beneficiary, BeneficiarySelection: selection, BeneficiarySelectedAt: selectedAt, AppScopeDigest: sha256.Sum256([]byte("scope")), Channel: domain.ChannelH5Official, ExpiresAt: expires, CreatedAt: now})
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	insert(legacyDigest, 44, paymentport.BeneficiarySelectionLegacyPrebound, nil)
	insert(newDigest, 0, paymentport.BeneficiarySelectionUnresolved, nil)

	var legacy paymentsession.Record
	err = uow.Within(ctx, func(tx context.Context) error {
		var inner error
		legacy, inner = repository.Lookup(tx, legacyDigest, now)
		return inner
	})
	if err != nil || legacy.BeneficiaryCustomerID != 44 || legacy.BeneficiarySelection != paymentport.BeneficiarySelectionLegacyPrebound || legacy.BeneficiarySelectedAt != nil {
		t.Fatalf("legacy=%+v err=%v", legacy, err)
	}
	err = uow.Within(ctx, func(tx context.Context) error {
		_, inner := repository.SelectPayerSelf(tx, legacyDigest, now)
		return inner
	})
	if !errors.Is(err, paymentsession.ErrInvalid) {
		t.Fatalf("legacy selection err=%v", err)
	}

	checkoutFailure := errors.New("order creation failed")
	err = uow.Within(ctx, func(tx context.Context) error {
		selected, inner := repository.SelectPayerSelf(tx, newDigest, now)
		if inner != nil || selected.BeneficiaryCustomerID != 11 || selected.BeneficiarySelection != paymentport.BeneficiarySelectionPayerSelf {
			t.Fatalf("selected=%+v err=%v", selected, inner)
		}
		return checkoutFailure // A later order/payment write fails: the selection must roll back too.
	})
	if !errors.Is(err, checkoutFailure) {
		t.Fatalf("rollback err=%v", err)
	}
	var unresolved paymentsession.Record
	err = uow.Within(ctx, func(tx context.Context) error {
		var inner error
		unresolved, inner = repository.Lookup(tx, newDigest, now)
		return inner
	})
	if err != nil || unresolved.BeneficiaryCustomerID != 0 || unresolved.BeneficiarySelection != paymentport.BeneficiarySelectionUnresolved || unresolved.BeneficiarySelectedAt != nil {
		t.Fatalf("rollback left session=%+v err=%v", unresolved, err)
	}

	start := make(chan struct{})
	errorsByAttempt := make(chan error, 2)
	var wait sync.WaitGroup
	for _, at := range []time.Time{now.Add(time.Second), now.Add(2 * time.Second)} {
		wait.Add(1)
		go func(at time.Time) {
			defer wait.Done()
			<-start
			errorsByAttempt <- uow.Within(ctx, func(tx context.Context) error {
				selected, inner := repository.SelectPayerSelf(tx, newDigest, at)
				if inner != nil {
					return inner
				}
				if selected.PayerCustomerID != 11 || selected.BeneficiaryCustomerID != 11 || selected.BeneficiarySelection != paymentport.BeneficiarySelectionPayerSelf {
					return errors.New("payer self selection drift")
				}
				return nil
			})
		}(at)
	}
	close(start)
	wait.Wait()
	close(errorsByAttempt)
	for attemptErr := range errorsByAttempt {
		if attemptErr != nil {
			t.Fatalf("concurrent selection err=%v", attemptErr)
		}
	}
	err = uow.Within(ctx, func(tx context.Context) error {
		var inner error
		unresolved, inner = repository.Lookup(tx, newDigest, now.Add(3*time.Second))
		return inner
	})
	if err != nil || unresolved.BeneficiaryCustomerID != 11 || unresolved.BeneficiarySelection != paymentport.BeneficiarySelectionPayerSelf || unresolved.BeneficiarySelectedAt == nil {
		t.Fatalf("concurrent selected=%+v err=%v", unresolved, err)
	}
}

func TestPostgreSQLPaymentSessionBeneficiaryFactConstraintRejectsMutualExclusion(t *testing.T) {
	pool, cleanup := paymentIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 9, 5, 3, 0, 0, 0, time.UTC)
	expires := now.Add(10 * time.Minute)
	selected := now.Add(time.Second)
	scopeDigest := sha256.Sum256([]byte("scope"))

	type invalidFact struct {
		name        string
		beneficiary any
		selection   string
		selectedAt  any
	}
	for _, row := range []invalidFact{
		{name: "legacy recipient cannot claim a fresh selection", beneficiary: int64(44), selection: "legacy_prebound", selectedAt: selected},
		{name: "legacy requires preserved recipient", beneficiary: nil, selection: "legacy_prebound", selectedAt: nil},
		{name: "unresolved cannot have recipient", beneficiary: int64(44), selection: "unresolved", selectedAt: nil},
		{name: "unresolved cannot have selected time", beneficiary: nil, selection: "unresolved", selectedAt: selected},
		{name: "payer self requires recipient", beneficiary: nil, selection: "payer_self", selectedAt: selected},
		{name: "payer self must equal payer", beneficiary: int64(44), selection: "payer_self", selectedAt: selected},
		{name: "payer self requires selected time", beneficiary: int64(11), selection: "payer_self", selectedAt: nil},
		{name: "admin assisted requires recipient", beneficiary: nil, selection: "admin_assisted", selectedAt: selected},
		{name: "admin assisted requires selected time", beneficiary: int64(44), selection: "admin_assisted", selectedAt: nil},
	} {
		t.Run(row.name, func(t *testing.T) {
			digest := sha256.Sum256([]byte("invalid-payment-session-fact:" + row.name))
			_, err := pool.Exec(ctx, `
				INSERT INTO payment_sessions(
					token_digest,payer_identity_id,payer_customer_id,beneficiary_customer_id,
					beneficiary_selection,beneficiary_selected_at,app_scope_digest,payment_channel,expires_at,created_at
				) VALUES($1,9,11,$2,$3,$4,$5,'h5_official',$6,$7)`,
				digest[:], row.beneficiary, row.selection, row.selectedAt, scopeDigest[:], expires, now,
			)
			if err == nil {
				t.Fatal("invalid beneficiary fact was accepted")
			}
		})
	}
}

func TestPostgreSQLH5OAuthReturnPathAcceptsOnlyPublicCommerceSegments(t *testing.T) {
	pool, cleanup := paymentIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 9, 5, 3, 0, 0, 0, time.UTC)
	for index, path := range []string{"/pay/course-7", "/s/term-31", "/s/term-31/pay", "/c/cp-a1b2c3"} {
		digest := sha256.Sum256([]byte("valid-return-path:" + path))
		if _, err := pool.Exec(ctx, `INSERT INTO payment_h5_oauth_states(state_digest,return_path,expires_at,created_at) VALUES($1,$2,$3,$4)`, digest[:], path, now.Add(time.Hour), now.Add(time.Duration(index)*time.Second)); err != nil {
			t.Fatalf("valid return path %q: %v", path, err)
		}
	}
	for _, path := range []string{"https://evil.example/pay/course-7", "//evil.example/pay/course-7", "/p/course-7", "/pay/course/7", "/s/term%2F31", "/s/term%5C31", "/s/term%23fragment", "/c/CAPITAL-2026", "/s/term\\31"} {
		digest := sha256.Sum256([]byte("invalid-return-path:" + path))
		if _, err := pool.Exec(ctx, `INSERT INTO payment_h5_oauth_states(state_digest,return_path,expires_at,created_at) VALUES($1,$2,$3,$4)`, digest[:], path, now.Add(time.Hour), now); err == nil {
			t.Fatalf("invalid return path accepted: %q", path)
		}
	}
}

func TestPostgreSQLAlipayPaymentChannelsAndRefundProvider(t *testing.T) {
	pool, cleanup := paymentIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	wrapper, err := platformpostgres.Wrap(pool, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	repository := paymentstore.NewPostgreSQL()
	now := time.Now().UTC()
	createOrder := func(provider, suffix string) int64 {
		t.Helper()
		var orderID int64
		err := pool.QueryRow(ctx, `INSERT INTO orders(provider,source_system,source_key,merchant_order_no,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,record_origin,effect_eligible,created_at,updated_at) VALUES($1,'alipay-checkout-test',$2,$3,11,11,8800,'CNY','pending_payment','native',true,$4,$4) RETURNING id`, provider, suffix, "M-"+suffix, now).Scan(&orderID)
		if err != nil {
			t.Fatal(err)
		}
		return orderID
	}
	for _, test := range []struct {
		provider domain.Provider
		channel  domain.Channel
		suffix   string
	}{
		{domain.ProviderAlipay, domain.ChannelAlipayWap, "alipay-wap"},
		{domain.ProviderAlipay, domain.ChannelAlipayPage, "alipay-page"},
		{domain.ProviderWeChatPay, domain.ChannelH5Official, "wechat-h5"},
	} {
		orderID := createOrder(string(test.provider), test.suffix)
		payment := domain.Payment{OrderID: orderID, Provider: test.provider, Channel: test.channel, MerchantOrderNo: "M-" + test.suffix, PayerIdentityID: 4, PayerCustomerID: 11, BeneficiaryCustomerID: 11, AmountMinor: 8800, Currency: "CNY", Status: domain.StatusAwaitingPrepay, Version: 1, CreatedAt: now, UpdatedAt: now}
		key := sha256.Sum256([]byte(test.suffix))
		payload := sha256.Sum256([]byte("payment:" + test.suffix))
		var created domain.Payment
		var createdNow bool
		if err = uow.Within(ctx, func(tx context.Context) error {
			created, createdNow, err = repository.CreatePayment(tx, payment, key, payload, "public-checkout")
			return err
		}); err != nil || created.ID < 1 || !createdNow {
			t.Fatalf("%s payment=%+v created=%t err=%v", test.suffix, created, createdNow, err)
		}
		if test.suffix == "alipay-wap" {
			source := effectport.Hash("alipay-provider-intent", test.suffix)
			digest := effectport.Hash("alipay-provider-intent-payload", test.suffix)
			if _, err = pool.Exec(ctx, `INSERT INTO payment_provider_intents(payment_id,effect_kind,source_ref_digest,target_ref_digest,payload_digest,policy_version_hash,request_snapshot,created_at) VALUES($1,$2,$3,$4,$5,$6,'{}',$7)`, created.ID, effectport.KindAlipayWapPay, source, digest, digest, digest, now); err != nil {
				t.Fatalf("insert legacy Alipay provider intent: %v", err)
			}
			var intent paymentport.ProviderIntent
			if err = uow.Within(ctx, func(tx context.Context) error {
				intent, err = repository.ProviderIntent(tx, effectport.KindAlipayWapPay, source)
				return err
			}); err != nil || intent.OrderID != orderID || intent.PaymentID != created.ID || intent.ProductID != "" {
				t.Fatalf("Alipay provider intent order linkage=%+v err=%v", intent, err)
			}
			if _, err = pool.Exec(ctx, `INSERT INTO payment_refunds(payment_id,provider,refund_no,amount_minor,reason,status,version,created_at,updated_at) VALUES($1,'alipay','R-alipay-wap',100,'test','requested',1,$2,$2)`, created.ID, now); err != nil {
				t.Fatalf("Alipay refund provider was rejected: %v", err)
			}
		}
	}
	wrongOrderID := createOrder("alipay", "alipay-wrong-channel")
	if _, err = pool.Exec(ctx, `INSERT INTO payments(order_id,provider,payment_channel,merchant_order_no,payer_identity_id,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,version,created_at,updated_at) VALUES($1,'alipay','h5_official_account','M-alipay-wrong-channel',4,11,11,8800,'CNY','awaiting_prepay',1,$2,$2)`, wrongOrderID, now); err == nil {
		t.Fatal("database accepted an Alipay payment with a WeChat channel")
	}
}

func paymentIntegrationPool(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	url, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("DATABASE_URL is not configured; skipping Payment PostgreSQL integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var random [8]byte
	if _, err = rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	schema := "aicrm_payment_test_" + hex.EncodeToString(random[:])
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
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(file), "..", "..", "..")
	for _, name := range []string{"0001_platform.sql", "0002_identity.sql", "0005_external_effects.sql", "0020_order.sql", "0021_payment.sql", "0024_order_product_version.sql", "0025_payment_reconciliation.sql", "0061_product_public_purchase.sql", "0068_payment_session_beneficiary_selection.sql", "0127_payment_historical_refund_states.sql", "0131_payment_historical_unassigned.sql", "0134_payment_history_source_delta.sql", "0140_payment_h5_unionid_verified.sql", "0143_payment_checkout_abandonments.sql", "0144_payment_checkout_restart_permissions.sql", "0156_distribution_profit_sharing_payment.sql", "0161_payment_paid_confirmation_time.sql", "0165_payment_profit_sharing_receiver_failure_class.sql", "0166_payment_profit_sharing_instruction_failure_class.sql", "0202_alipay_web_payment.sql", "0206_order_native_alipay_checkout.sql", "0207_payment_alipay_provider_channels.sql"} {
		raw, readErr := os.ReadFile(filepath.Join(root, "migrations", name))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, err = pool.Exec(ctx, string(raw)); err != nil {
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

func TestPostgreSQLProductOAuthReturnMigrationPreservesRedirectBoundary(t *testing.T) {
	pool, cleanup := paymentIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	_, file, _, _ := runtime.Caller(0)
	body, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations", "0142_payment_h5_product_return_path.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, string(body)); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/p/subscription_trial_month", "/p/7", "/pay/7", "/s/course/pay", "/c/coupon-2026"} {
		digest := sha256.Sum256([]byte(path))
		if _, err = pool.Exec(ctx, `INSERT INTO payment_h5_oauth_states(state_digest,return_path,expires_at,created_at) VALUES($1,$2,now()+interval '10 minutes',now())`, digest[:], path); err != nil {
			t.Fatalf("valid path=%q: %v", path, err)
		}
	}
	for _, path := range []string{"//evil.example/p/7", "https://evil.example/p/7", "/p/a/b", "/p/a?b", "/p/a#b", "/p/a%2fb", "/p/a%5Cb", "/p/a%3fb", "/p/a%23b", "/p/a\\b"} {
		digest := sha256.Sum256([]byte(path))
		if _, err = pool.Exec(ctx, `INSERT INTO payment_h5_oauth_states(state_digest,return_path,expires_at,created_at) VALUES($1,$2,now()+interval '10 minutes',now())`, digest[:], path); err == nil {
			t.Fatalf("unsafe redirect accepted: %q", path)
		}
	}
}

func TestPostgreSQLDistributionOAuthReturnMigrationKeepsApplicationContextClosed(t *testing.T) {
	pool, cleanup := paymentIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	_, file, _, _ := runtime.Caller(0)
	for _, name := range []string{"0142_payment_h5_product_return_path.sql", "0162_payment_h5_distribution_return_path.sql"} {
		body, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations", name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, string(body)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
	}
	promotion := "dpc_" + strings.Repeat("A", 43)
	for _, path := range []string{
		"/p/subscription_trial_month",
		"/pay/7",
		"/s/course/pay",
		"/c/coupon-2026",
		"/distribution",
		"/distribution?product_id=7&product_type=standard_product",
		"/distribution?product_id=8&product_type=service_period",
		"/distribution?product_id=9223372036854775807&product_type=standard_product",
		"/p/course-7?promotion_context=" + promotion,
		"/pay/course-7?promotion_context=" + promotion,
		"/s/term-31?promotion_context=" + promotion,
		"/s/term-31/pay?promotion_context=" + promotion,
	} {
		digest := sha256.Sum256([]byte("distribution-valid-return-path:" + path))
		if _, err := pool.Exec(ctx, `INSERT INTO payment_h5_oauth_states(state_digest,return_path,expires_at,created_at) VALUES($1,$2,now()+interval '10 minutes',now())`, digest[:], path); err != nil {
			t.Fatalf("valid Distribution return path %q: %v", path, err)
		}
	}
	for _, path := range []string{
		"/distribution?product_id=0&product_type=standard_product",
		"/distribution?product_id=7&product_type=unknown",
		"/distribution?product_type=standard_product&product_id=7",
		"/distribution?product_id=7&product_type=standard_product&commission_rate=100",
		"/distribution?product_id=7&product_type=standard_product%26receiver=x",
		"/distribution?product_id=7&product_type=standard_product#fragment",
		"/distribution?product_id=7&product_type=standard_product&product_type=service_period",
		"/distribution?product_id=9223372036854775808&product_type=standard_product",
		"/p/course-7?promotion_context=dpc_short",
		"/p/course-7?promotion_context=" + promotion + "&next=/pay/course-7",
		"/p/course-7?next=/pay/course-7&promotion_context=" + promotion,
		"/p/course-7?promotion_context=" + promotion + "&promotion_context=" + promotion,
		"/p/course%2d7?promotion_context=" + promotion,
		"/p/course-7?promotion_context=dpc_" + strings.Repeat("A", 42),
		"/p/course-7?promotion_context=" + promotion + "#fragment",
		"/c/coupon-2026?promotion_context=" + promotion,
	} {
		digest := sha256.Sum256([]byte("distribution-invalid-return-path:" + path))
		if _, err := pool.Exec(ctx, `INSERT INTO payment_h5_oauth_states(state_digest,return_path,expires_at,created_at) VALUES($1,$2,now()+interval '10 minutes',now())`, digest[:], path); err == nil {
			t.Fatalf("unsafe Distribution return path accepted: %q", path)
		}
	}
}

func TestPostgreSQLReferralOAuthReturnMigrationClosesStatePersistenceBoundary(t *testing.T) {
	pool, cleanup := paymentIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	_, file, _, _ := runtime.Caller(0)
	migrationRoot := filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations")
	for _, name := range []string{"0142_payment_h5_product_return_path.sql", "0162_payment_h5_distribution_return_path.sql"} {
		body, err := os.ReadFile(filepath.Join(migrationRoot, name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, string(body)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
	}
	preMigration := "/referral?campaign=7"
	preDigest := sha256.Sum256([]byte("referral-before-migration"))
	if _, err := pool.Exec(ctx, `INSERT INTO payment_h5_oauth_states(state_digest,return_path,expires_at,created_at) VALUES($1,$2,now()+interval '10 minutes',now())`, preDigest[:], preMigration); err == nil {
		t.Fatalf("Referral return path unexpectedly passed pre-0191 constraint: %q", preMigration)
	}
	body, err := os.ReadFile(filepath.Join(migrationRoot, "0191_payment_h5_referral_return_path.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, string(body)); err != nil {
		t.Fatal(err)
	}
	token := "rfi_" + strings.Repeat("A", 43)
	for index, path := range []string{
		"/pay/course-7",
		"/distribution?product_id=7&product_type=standard_product",
		"/referral",
		"/referral?campaign=7",
		"/referral?campaign=7&invite=" + token,
		"/referral?campaign=9223372036854775807&invite=" + token,
	} {
		digest := sha256.Sum256([]byte(fmt.Sprintf("referral-valid-%d", index)))
		if _, err = pool.Exec(ctx, `INSERT INTO payment_h5_oauth_states(state_digest,return_path,expires_at,created_at) VALUES($1,$2,now()+interval '10 minutes',now())`, digest[:], path); err != nil {
			t.Fatalf("valid return path %q: %v", path, err)
		}
	}
	for index, path := range []string{
		"https://evil.example/referral?campaign=7",
		"/referral/",
		"/referral?campaign=0",
		"/referral?campaign=9223372036854775808",
		"/referral?invite=" + token,
		"/referral?campaign=7&invite=rfi_short",
		"/referral?invite=" + token + "&campaign=7",
		"/referral?campaign=7&invite=" + token + "&next=/admin",
		"/referral?campaign=7&invite=" + token + "#fragment",
		"/referral?campaign=7%26invite=" + token,
	} {
		digest := sha256.Sum256([]byte(fmt.Sprintf("referral-invalid-%d", index)))
		if _, err = pool.Exec(ctx, `INSERT INTO payment_h5_oauth_states(state_digest,return_path,expires_at,created_at) VALUES($1,$2,now()+interval '10 minutes',now())`, digest[:], path); err == nil {
			t.Fatalf("unsafe Referral return path accepted: %q", path)
		}
	}
}
