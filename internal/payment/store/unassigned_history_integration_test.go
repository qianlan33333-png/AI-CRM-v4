package store_test

import (
	"context"
	"encoding/json"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	paymenthttp "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/http"
	paymentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/store"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type historyReadApp struct {
	paymenthttp.Application
	read func(context.Context, domain.Provider, string) (domain.Payment, error)
}

func (a historyReadApp) FindPayment(c context.Context, p domain.Provider, s string) (domain.Payment, error) {
	return a.read(c, p, s)
}

type historyAdmin struct{}

func (historyAdmin) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	return accessdomain.Principal{Kind: accessdomain.KindAdmin}, nil
}
func (historyAdmin) AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error) {
	return accessdomain.Principal{Kind: accessdomain.KindAdmin}, nil
}
func TestPostgreSQLUnassignedHistoryNativeAPIAndNoEffects(t *testing.T) {
	pool, cleanup := paymentIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	wrapper, _ := platformpostgres.Wrap(pool, time.Second)
	uow, _ := platformpostgres.NewUnitOfWork(wrapper)
	repo := paymentstore.NewPostgreSQL()
	now := time.Now().UTC().Truncate(time.Microsecond)
	var orderID int64
	if err := pool.QueryRow(ctx, `INSERT INTO orders(provider,source_system,source_key,merchant_order_no,amount_minor,currency,status,record_origin,effect_eligible,source_row_digest,created_at,updated_at) VALUES('wechat_shop','commerce-history','unassigned','unassigned',100,'CNY','paid','history',false,$1,$2,$2) RETURNING id`, make([]byte, 32), now).Scan(&orderID); err != nil {
		t.Fatal(err)
	}
	paidConfirmedAt := now
	p := domain.Payment{OrderID: orderID, Provider: domain.ProviderWeChatShop, MerchantOrderNo: "unassigned", AmountMinor: 100, Currency: "CNY", Status: domain.StatusPaid, PaidConfirmedAt: &paidConfirmedAt, SourceStatus: "returned", HistoryReason: "refund_evidence_missing", Version: 1, CreatedAt: now, UpdatedAt: now}
	if err := uow.Within(ctx, func(c context.Context) error {
		var e error
		p, e = repo.ImportTerminalPayment(c, p, [32]byte{4}, "unassigned-test")
		return e
	}); err != nil {
		t.Fatal(err)
	}
	var nulls bool
	if err := pool.QueryRow(ctx, `SELECT payer_identity_id IS NULL AND payer_customer_id IS NULL AND beneficiary_customer_id IS NULL AND historical AND external_effect_id IS NULL FROM payments WHERE id=$1`, p.ID).Scan(&nulls); err != nil || !nulls {
		t.Fatalf("nullable historical storage: %v %v", nulls, err)
	}
	if _, err := domain.NewRefund(p, "unsafe", 1, "no new effect", now); err == nil {
		t.Fatal("historical money allowed new refund")
	}
	// Source has no refund evidence: do not invent a refund from returned status.
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM payment_refunds WHERE payment_id=$1`, p.ID).Scan(&n); err != nil || n != 0 {
		t.Fatal("fabricated refund", err)
	}
	app := historyReadApp{read: func(c context.Context, provider domain.Provider, merchant string) (domain.Payment, error) {
		var value domain.Payment
		e := uow.Within(c, func(tx context.Context) error {
			var e error
			value, e = repo.GetPaymentByMerchantProvider(tx, provider, merchant, false)
			return e
		})
		return value, e
	}}
	handler, err := paymenthttp.NewHandler(app, nil, historyAdmin{}, false)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/admin/payments/history?provider=wechat_shop&merchant_order_no=unassigned", nil))
	var body map[string]any
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &body) != nil || body["payer_customer_id"] != nil || body["beneficiary_customer_id"] != nil || body["source_status"] != "returned" || body["history_reason"] != "refund_evidence_missing" || body["status"] != "paid" || body["effect_eligible"] != false {
		t.Fatalf("native history API %d %s", rec.Code, rec.Body.String())
	}
	// An independently evidenced failed refund remains visible even without payer.
	if err := uow.Within(ctx, func(c context.Context) error {
		_, e := repo.ImportTerminalRefund(c, domain.Refund{PaymentID: p.ID, Provider: p.Provider, RefundNo: "failed-history", Reason: "source failure", AmountMinor: 40, Status: domain.RefundHistoryFailed, Version: 1, CreatedAt: now, UpdatedAt: now}, [32]byte{5}, "unassigned-test")
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if err := uow.Within(ctx, func(c context.Context) error {
		rows, total, e := repo.ListRefunds(c, 10, 0)
		if e == nil && (total != 1 || rows[0].Refund.Status != domain.RefundHistoryFailed || rows[0].Refund.EffectID != "") {
			t.Fatal("refund history lost")
		}
		return e
	}); err != nil {
		t.Fatal(err)
	}
	// Payer-only historical fact preserves known payer without inventing a recipient.
	var payerOnlyOrder int64
	if err := pool.QueryRow(ctx, `INSERT INTO orders(provider,source_system,source_key,merchant_order_no,payer_customer_id,amount_minor,currency,status,record_origin,effect_eligible,source_row_digest,created_at,updated_at) VALUES('wechat_pay','commerce-history','payer-only','payer-only',11,100,'CNY','paid','history',false,$1,$2,$2) RETURNING id`, make([]byte, 32), now).Scan(&payerOnlyOrder); err != nil {
		t.Fatal(err)
	}
	var payerOnly domain.Payment
	if err := uow.Within(ctx, func(c context.Context) error {
		var e error
		payerOnly, e = repo.ImportTerminalPayment(c, domain.Payment{OrderID: payerOnlyOrder, Provider: domain.ProviderWeChatPay, MerchantOrderNo: "payer-only", PayerIdentityID: 4, PayerCustomerID: 11, AmountMinor: 100, Currency: "CNY", Status: domain.StatusPaid, PaidConfirmedAt: &paidConfirmedAt, Version: 1, CreatedAt: now, UpdatedAt: now}, [32]byte{6}, "unassigned-test")
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT payer_identity_id=4 AND payer_customer_id=11 AND beneficiary_customer_id IS NULL FROM payments WHERE id=$1`, payerOnly.ID).Scan(&nulls); err != nil || !nulls {
		t.Fatal("invented beneficiary", err)
	}
	// Native records still require real identity columns; SQL NULL is not accepted.
	if _, err := pool.Exec(ctx, `UPDATE payments SET historical=false WHERE id=$1`, p.ID); err == nil {
		t.Fatal("native identity guard bypassed")
	}
}
