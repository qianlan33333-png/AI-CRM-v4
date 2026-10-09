package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	orderapp "github.com/qianlan33333-png/AI-CRM-v3/internal/order/app"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	orderstore "github.com/qianlan33333-png/AI-CRM-v3/internal/order/store"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
)

// Reuse the enabled callback fixture and its exact installed API process.
// All requests are reads. Card positive Provider matching is covered in the
// signed Provider/source contracts; these rejects must never call a Provider.
func exerciseInvoiceCardPaymentHTTP(t *testing.T, f *invoiceFixture, applicationID, endpoint string, client *http.Client) {
	t.Helper()
	checkout := orderapp.NewService(f.s.UoW, f.s.Orders.(*orderstore.Repository))
	var orderID int64
	if err := f.s.UoW.Within(f.ctx, func(tx context.Context) error {
		order, err := checkout.CreatePaymentOrderWithin(tx, orderport.PaymentOrderCommand{
			Provider: "wechat_pay", MerchantOrderNo: "installed-original-resume-7", PayerCustomerID: f.customer,
			BeneficiaryCustomerID: f.customer, ProductID: 9, ProductType: "standard_product", ProductCode: "course",
			ProductName: "原商品冻结名称", ProductVersion: 1, UnitAmountMinor: 9900, Currency: "CNY",
			ActorScope: "installed-resume-fixture", IdempotencyKey: "installed-resume-fixture-0001",
		})
		orderID = order.ID
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(f.ctx, `INSERT INTO payments(order_id,provider,payment_channel,merchant_order_no,payer_identity_id,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,version,created_at,updated_at)
		SELECT id,'wechat_pay','h5_official_account',merchant_order_no,$2,payer_customer_id,beneficiary_customer_id,amount_minor,'CNY','awaiting_payment',1,created_at,updated_at FROM orders WHERE id=$1`, orderID, f.identity); err != nil {
		t.Fatal(err)
	}
	foreign := f.order(f.other, f.other, "pending_payment", "他人原单", "wechat_pay", time.Now())
	counts := func() [5]int {
		var out [5]int
		if err := f.pool.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM orders),(SELECT count(*) FROM payments),(SELECT count(*) FROM external_effects),(SELECT count(*) FROM invoice_applications),(SELECT COALESCE(sum(version),0) FROM payments)`).Scan(&out[0], &out[1], &out[2], &out[3], &out[4]); err != nil {
			t.Fatal(err)
		}
		return out
	}
	before := counts()
	read := func(path, token string, expected int) map[string]any {
		request, err := http.NewRequestWithContext(f.ctx, http.MethodGet, endpoint+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if token != "" {
			request.AddCookie(&http.Cookie{Name: paymentport.TrustedSessionCookieName, Value: token})
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal("mounted invoice/order read failed")
		}
		body, err := io.ReadAll(io.LimitReader(response.Body, 16<<10))
		response.Body.Close()
		var out map[string]any
		if response.StatusCode != expected || err != nil || json.Unmarshal(body, &out) != nil || response.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("mounted read %s status=%d want=%d no-store=%t", path, response.StatusCode, expected, response.Header.Get("Cache-Control") == "no-store")
		}
		for _, forbidden := range []string{"openid-isolated-fixture", "invoice-fixture-token", "handoff_token", "prepay_id"} {
			if strings.Contains(string(body), forbidden) {
				t.Fatalf("mounted read leaked private identity/payment field %s", forbidden)
			}
		}
		return out
	}
	paymentPath := fmt.Sprintf("/api/h5/me/orders/%d/payment", orderID)
	cardPath := "/api/h5/me/invoices/" + applicationID + "/card"
	for _, path := range []string{paymentPath, cardPath} {
		if read(path, "", 401)["code"] != "session_required" || read(path, "invalid-session-installed-0001", 401)["code"] != "session_required" {
			t.Fatal("mounted reads bypassed trusted session")
		}
	}
	for attempt := 0; attempt < 2; attempt++ {
		out := read(paymentPath, f.token, 200)
		if out["order_id"] != float64(orderID) || out["amount_minor"] != float64(9900) || out["merchant_order_no"] != "installed-original-resume-7" || out["product_name"] != "原商品冻结名称" || out["product_kind"] != "standard" || out["payment_path"] != fmt.Sprintf("/pay/course?resume_order=%d", orderID) {
			t.Fatal("mounted original payment lost frozen order facts")
		}
	}
	if read(fmt.Sprintf("/api/h5/me/orders/%d/payment", foreign), f.token, 404)["code"] != "not_found" {
		t.Fatal("mounted original payment broadened payer ownership")
	}
	if read(cardPath, f.token, 409)["code"] != "ineligible" {
		t.Fatal("mounted awaiting-title card exposed a native entry")
	}
	if _, err := f.pool.Exec(f.ctx, `UPDATE invoice_applications SET customer_id=$2 WHERE id=$1`, applicationID, f.other); err != nil {
		t.Fatal(err)
	}
	if read(cardPath, f.token, 404)["code"] != "not_found" {
		t.Fatal("mounted native card broadened application ownership")
	}
	if _, err := f.pool.Exec(f.ctx, `UPDATE invoice_applications SET customer_id=$2,state='issued',blue_number='BLUE-fixture',refund_confirmed=true WHERE id=$1`, applicationID, f.customer); err != nil {
		t.Fatal(err)
	}
	if read(cardPath, f.token, 409)["code"] != "ineligible" {
		t.Fatal("mounted native card accepted confirmed refund")
	}
	if counts() != before {
		t.Fatal("mounted GET created or changed order/payment/effect/application facts")
	}
	t.Log("invoice-card-payment-mounted PASS frozen_original_order=true session_and_owner_rejected=true card_unissued_and_refunded_rejected=true no_store=true zero_new_orders_payments_effects=true")
}
