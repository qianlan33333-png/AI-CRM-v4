package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	effects "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	invoice "github.com/qianlan33333-png/AI-CRM-v3/internal/invoice"
	p "github.com/qianlan33333-png/AI-CRM-v3/internal/invoice/port"
)

// Cryptographic verification is covered with real SDK RSA/AES fixtures in the
// provider package; here the real PostgreSQL receipt and callback HTTP are used.
type invoiceCallbackProvider struct {
	*invoiceFixtureProvider
	notice p.Notice
}

func (v *invoiceCallbackProvider) Verify(_ context.Context, r *http.Request) (p.Notice, error) {
	if r.Header.Get("X-Fixture-Verified") != "true" {
		return p.Notice{}, p.ErrInvalid
	}
	return v.notice, nil
}
func TestInvoicePostgreSQLCallbackAcknowledgementAndReplay(t *testing.T) {
	f := newInvoiceFixture(t)
	id := f.order(f.customer, f.customer, "paid", "fixture invoice", "wechat_pay", time.Now())
	r := f.create(id, "callback-key-00000001")
	a := f.read(r.ID)
	provider := &invoiceCallbackProvider{invoiceFixtureProvider: f.provider, notice: p.Notice{ID: "notice-fixture", ApplicationID: a.ID, SubMerchantID: a.Snapshot.MerchantID, BodyDigest: string(effects.Hash("fixture", "body"))}}
	f.s.Provider = provider
	h := &invoice.Handler{Service: f.s}
	post := func(valid bool) int {
		req := httptest.NewRequest(http.MethodPost, "https://crm.fixture/api/callbacks/wechat-invoice", strings.NewReader(`{}`))
		if valid {
			req.Header.Set("X-Fixture-Verified", "true")
		}
		out := httptest.NewRecorder()
		h.ServeHTTP(out, req)
		return out.Code
	}
	if post(true) != 200 || post(true) != 200 {
		t.Fatal("verified or replayed notification not acknowledged")
	}
	if post(false) < 400 {
		t.Fatal("unverified callback acknowledged")
	}
	provider.notice.BodyDigest = string(effects.Hash("fixture", "changed"))
	if post(true) != 409 {
		t.Fatal("notification ID rebound to a different body")
	}
	provider.notice.ID = "different-notice"
	provider.notice.SubMerchantID = "foreign"
	if post(true) != 409 {
		t.Fatal("wrong collecting merchant accepted")
	}
	var count int
	if e := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM invoice_callback_receipts`).Scan(&count); e != nil || count != 1 {
		t.Fatal("invalid notification persisted", e)
	}
}
