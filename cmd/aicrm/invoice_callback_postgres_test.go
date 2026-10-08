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

func TestInvoicePostgreSQLCallbackBootstrapCannotQueueBusiness(t *testing.T) {
	f := newInvoiceFixture(t)
	id := f.order(f.customer, f.customer, "paid", "fixture invoice", "wechat_pay", time.Now())
	r := f.create(id, "callback-bootstrap-0001")
	a := f.read(r.ID)
	verifier := &invoiceCallbackProvider{notice: p.Notice{ID: "bootstrap-notice", ApplicationID: a.ID, SubMerchantID: a.Snapshot.MerchantID, BodyDigest: string(effects.Hash("fixture", "body"))}}
	f.s.Config.Enabled = false
	f.s.Provider = nil
	f.s.CallbackVerifier = verifier
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
	counts := func() [3]int {
		var counts [3]int
		if e := f.pool.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM invoice_callback_receipts), (SELECT count(*) FROM river_job WHERE kind='invoice.provider-query.v1'), (SELECT count(*) FROM external_effects WHERE owner='invoice')`).Scan(&counts[0], &counts[1], &counts[2]); e != nil {
			t.Fatal(e)
		}
		return counts
	}
	before := counts()
	verifier.notice.SubMerchantID = "foreign-sub"
	if post(true) != 409 {
		t.Fatal("known application belonging to another collecting merchant accepted")
	}
	verifier.notice.SubMerchantID = a.Snapshot.MerchantID
	if post(true) != 503 {
		t.Fatal("disabled known application accepted a business notification")
	}
	verifier.notice.ApplicationID = "inv_" + strings.Repeat("0", 28)
	if post(false) < 400 || post(true) != 200 || post(true) != 200 {
		t.Fatal("bootstrap must authenticate an unknown application before acknowledgement")
	}
	if after := counts(); after != before {
		t.Fatal("bootstrap created receipts, jobs or effects", before, after)
	}
	if f.s.ProviderAdapter() != nil || f.read(a.ID).State != a.State {
		t.Fatal("bootstrap enabled writes or changed an application")
	}
}
