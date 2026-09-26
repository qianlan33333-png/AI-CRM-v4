package main

import (
	"context"
	"sync"
	"testing"
	"time"

	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	paymentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	paymentprovider "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/provider"
)

// Real Payment/Order/UoW/paid-event consumers, isolated synthetic PostgreSQL
// data. Signature normalization is tested separately with ephemeral SDK keys.
func TestPostgreSQLAlipayConcurrentCallbacksSettleOnceAndRollback(t *testing.T) {
	fixture := newProductExternalPushChromiumFixtureWithOptions(t, 90*time.Second, productExternalPushChromiumFixtureOptions{enablePublicH5: true, enableAlipay: true, deferEffectsWorker: true})
	ctx := context.Background()
	session := issuePublicCommerceTrustedH5SessionWithKey(t, fixture, "alipay-callback-session-0001")
	created := createPublicAlipayCheckout(t, fixture, session.token, fixture.productID, paymentdomain.ChannelAlipayWap, "alipay-callback-create-0001")
	var amount int64
	if err := fixture.application.pool.Native().QueryRow(ctx, `SELECT amount_minor FROM payments WHERE id=$1`, created.PaymentID).Scan(&amount); err != nil {
		t.Fatal(err)
	}
	callback := paymentprovider.CallbackResult{Provider: paymentdomain.ProviderAlipay, Kind: "payment", AppID: "alipay-fixture-app",
		MerchantOrderNo: created.MerchantOrder, ProviderTransactionReference: "synthetic-callback-trade",
		ProviderTransactionDigest: string(effectport.Hash("alipay.transaction", "synthetic-callback-trade")), AmountMinor: amount, Currency: "CNY",
		OccurredAt: time.Now().UTC(), EventDigest: [32]byte{51}, BodyDigest: [32]byte{52}}
	// Read the fixture's accepted app identifier rather than any real config.
	callback.AppID = "virtual-alipay-test-app"
	var wait sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			errs <- fixture.application.paymentReconciliation.ApplyVerifiedCallback(ctx, callback)
		}()
	}
	wait.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var status string
	var version, receipts, events int64
	if err := fixture.application.pool.Native().QueryRow(ctx, `SELECT status,version FROM orders WHERE id=$1`, created.OrderID).Scan(&status, &version); err != nil {
		t.Fatal(err)
	}
	if err := fixture.application.pool.Native().QueryRow(ctx, `SELECT count(*) FROM payment_callback_receipts WHERE payment_id=$1`, created.PaymentID).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if err := fixture.application.pool.Native().QueryRow(ctx, `SELECT count(*) FROM order_paid_events WHERE order_id=$1`, created.OrderID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if status != "paid" || version != 2 || receipts != 1 || events != 1 {
		t.Fatalf("status=%s version=%d receipts=%d events=%d", status, version, receipts, events)
	}
	// A new notification after a legacy query observation timestamp records a
	// replay receipt while keeping the original settlement and paid event.
	if _, err := fixture.application.pool.Native().Exec(ctx, `UPDATE payments SET paid_confirmed_at=$2 WHERE id=$1`, created.PaymentID, callback.OccurredAt.Add(80*time.Second)); err != nil {
		t.Fatal(err)
	}
	callback.EventDigest = [32]byte{53}
	if err := fixture.application.paymentReconciliation.ApplyVerifiedCallback(ctx, callback); err != nil {
		t.Fatal(err)
	}
	var outcome string
	if err := fixture.application.pool.Native().QueryRow(ctx, `SELECT outcome FROM payment_callback_receipts WHERE payment_id=$1 AND event_digest=$2`, created.PaymentID, callback.EventDigest[:]).Scan(&outcome); err != nil {
		t.Fatal(err)
	}
	if outcome != "replayed" {
		t.Fatalf("outcome=%s", outcome)
	}
	// Force the Order consumer to fail. The Payment state and claimed callback
	// must roll back together, then the same verified callback stays retryable.
	nextSession := issuePublicCommerceTrustedH5SessionWithKey(t, fixture, "alipay-callback-session-0002")
	next := createPublicAlipayCheckout(t, fixture, nextSession.token, seedAlipayPageCheckoutProduct(t, fixture), paymentdomain.ChannelAlipayPage, "alipay-callback-create-0002")
	callback.OccurredAt = time.Now().UTC()
	callback.ProviderTransactionReference = "synthetic-callback-trade-2"
	callback.ProviderTransactionDigest = string(effectport.Hash("alipay.transaction", callback.ProviderTransactionReference))
	callback.MerchantOrderNo = next.MerchantOrder
	callback.EventDigest = [32]byte{54}
	if err := fixture.application.pool.Native().QueryRow(ctx, `SELECT amount_minor FROM payments WHERE id=$1`, next.PaymentID).Scan(&callback.AmountMinor); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.application.pool.Native().Exec(ctx, `CREATE FUNCTION reject_paid_fixture() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic paid-event failure'; END $$; CREATE TRIGGER reject_paid_fixture BEFORE INSERT ON order_paid_events FOR EACH ROW EXECUTE FUNCTION reject_paid_fixture()`); err != nil {
		t.Fatal(err)
	}
	if err := fixture.application.paymentReconciliation.ApplyVerifiedCallback(ctx, callback); err == nil {
		t.Fatal("consumer failure accepted")
	}
	if err := fixture.application.pool.Native().QueryRow(ctx, `SELECT status FROM payments WHERE id=$1`, next.PaymentID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if err := fixture.application.pool.Native().QueryRow(ctx, `SELECT count(*) FROM payment_callback_receipts WHERE payment_id=$1`, next.PaymentID).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if status == "paid" || receipts != 0 {
		t.Fatalf("partial settlement status=%s receipts=%d", status, receipts)
	}
	if _, err := fixture.application.pool.Native().Exec(ctx, `DROP TRIGGER reject_paid_fixture ON order_paid_events`); err != nil {
		t.Fatal(err)
	}
	if err := fixture.application.paymentReconciliation.ApplyVerifiedCallback(ctx, callback); err != nil {
		t.Fatal(err)
	}
}
