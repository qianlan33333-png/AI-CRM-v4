package invoice

import (
	"errors"
	p "github.com/qianlan33333-png/AI-CRM-v3/internal/invoice/port"
	o "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestInvoiceBeijingDatesAndRejectedFilters(t *testing.T) {
	q, e := parseOrdersQuery(url.Values{"from": {"2026-10-07"}, "until": {"2026-10-08"}, "keyword": {"课程"}})
	if e != nil || q.From.Format(time.RFC3339) != "2026-10-06T16:00:00Z" || q.Until.Format(time.RFC3339) != "2026-10-08T16:00:00Z" {
		t.Fatalf("date boundaries=%+v %v", q, e)
	}
	for _, v := range []url.Values{{"state": {"paid", "all"}}, {"customer_id": {"11"}}, {"until": {"2026-13-08"}}, {"from": {"2026-10-09"}, "until": {"2026-10-08"}}, {"eligible": {"1"}}, {"limit": {"999"}}} {
		if _, e := parseOrdersQuery(v); !errors.Is(e, p.ErrInvalid) {
			t.Fatalf("accepted filter %v %v", v, e)
		}
	}
}
func TestInvoiceFrozenTaxUsesPaidAmountAndNoGuesses(t *testing.T) {
	policy := int64(8)
	s := Service{Config: p.Config{MerchantID: "merchant", SellerTaxID: "taxpayer", BillingPersonID: "person", Taxes: map[string]p.Tax{"course": {Code: "3010101020203000000", Rate: 600, GoodsName: "课程服务"}}}}
	order := o.Snapshot{ProviderTransactionNo: "4200000444201910177461284488", Amount: o.Money{AmountMinor: 9900000000000, Currency: "CNY"}, Items: []o.ItemSnapshot{{ProductCode: "course", Quantity: 1, LineAmountMinor: 10000000000000}, {ProductCode: "course", Quantity: 1, LineAmountMinor: 10000000000000}}}
	tax := s.Config.Taxes["course"]
	tax.PreferentialPolicyCode = &policy
	s.Config.Taxes["course"] = tax
	snapshot, e := s.freeze(order)
	if e != nil || snapshot.Lines[0].TotalAmount != 4950000000000 || snapshot.Lines[0].TotalAmount+snapshot.Lines[1].TotalAmount != order.Amount.AmountMinor {
		t.Fatalf("discount allocation/overflow=%+v %v", snapshot, e)
	}
	policy = 9
	if snapshot.Lines[0].PreferentialPolicyCode == nil || *snapshot.Lines[0].PreferentialPolicyCode != 8 {
		t.Fatal("finance policy was not frozen")
	}
	tax.Rate = 800
	if tax.Valid() {
		t.Fatal("unsupported official rate accepted")
	}
	order.Items[0].ProductCode = "unknown"
	if _, e = s.freeze(order); e == nil {
		t.Fatal("guessed tax configuration")
	}
	if (p.Buyer{Type: "ORGANIZATION", Name: "Company"}).Valid(snapshot) {
		t.Fatal("company title missing taxpayer id accepted")
	}
	if (p.Buyer{Type: "INDIVIDUAL", Name: "User", Amount: 1}).Valid(snapshot) {
		t.Fatal("wrong invoice amount accepted")
	}
}
func TestInvoiceHTTPRejectsUnauthenticatedAndCrossSiteWrite(t *testing.T) {
	h := Handler{Origins: []string{"https://crm.test"}}
	cases := []struct {
		cookie, origin, host string
		want                 int
	}{{"", "https://crm.test", "crm.test", 401}, {"session", "https://evil.test", "crm.test", 403}, {"session", "", "crm.test", 403}, {"session", "https://crm.test", "evil.test", 403}}
	for _, c := range cases {
		r := httptest.NewRequest(http.MethodPost, "https://"+c.host+"/api/h5/me/invoices", strings.NewReader(`{"order_id":1}`))
		r.Header.Set("Origin", c.origin)
		if c.cookie != "" {
			r.AddCookie(&http.Cookie{Name: "aicrm_payment_session", Value: c.cookie})
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != c.want {
			t.Fatalf("boundary status=%d want %d", w.Code, c.want)
		}
	}
	if validApplicationID("../../admin") || validApplicationID(strings.Repeat("1", 33)) {
		t.Fatal("invalid application route accepted")
	}
}

func TestInvoiceConfiguredDefaultTaxAndExplicitOverride(t *testing.T) {
	policy := int64(8)
	defaultTax := p.Tax{Code: "3040203000000000000", Rate: 100, GoodsName: "服务费", PreferentialPolicyCode: &policy}
	s := Service{Config: p.Config{MerchantID: "merchant", SellerTaxID: "taxpayer", BillingPersonID: "person", Taxes: map[string]p.Tax{"*": defaultTax, "specific": {Code: "3010101020203000000", Rate: 600, GoodsName: "课程服务"}}}}
	order := o.Snapshot{ProviderTransactionNo: "4200000444201910177461284488", Amount: o.Money{AmountMinor: 9900, Currency: "CNY"}, Items: []o.ItemSnapshot{{ProductCode: "new-product", Quantity: 1, LineAmountMinor: 9900}, {ProductCode: "specific", Quantity: 1, LineAmountMinor: 9900}}}
	snapshot, e := s.freeze(order)
	if e != nil || len(snapshot.Lines) != 2 || snapshot.Lines[0].TaxCode != defaultTax.Code || snapshot.Lines[0].TaxRate != 100 || snapshot.Lines[0].GoodsName != "服务费" || snapshot.Lines[1].TaxRate != 600 || snapshot.Lines[0].TotalAmount+snapshot.Lines[1].TotalAmount != 9900 {
		t.Fatal("default or product override not frozen", e, snapshot)
	}
	policy = 9
	if *snapshot.Lines[0].PreferentialPolicyCode != 8 {
		t.Fatal("default policy mutated after freezing")
	}
	s.Config.Taxes["specific"] = p.Tax{}
	if _, e = s.freeze(order); e == nil {
		t.Fatal("invalid explicit tax silently fell back to default")
	}
	delete(s.Config.Taxes, "specific")
	s.Config.Taxes["*"] = p.Tax{}
	if _, e = s.freeze(order); e == nil {
		t.Fatal("invalid default accepted")
	}
}

func TestInvoiceCallbackProbeCannotAcceptBusinessNotice(t *testing.T) {
	// A disabled provider may be reached by the official config checker. It must
	// never acknowledge unsigned POSTs as successfully accepted business notices.
	h := Handler{Service: &Service{}}
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(method, "https://crm.test/api/callbacks/wechat-invoice", nil))
		if w.Code != 200 || w.Body.Len() != 0 {
			t.Fatal("probe failed")
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "https://crm.test/api/callbacks/wechat-invoice", strings.NewReader(`{}`)))
	if w.Code < 400 {
		t.Fatal("unsigned business notice acknowledged")
	}
}

func TestInvoiceFreeOrderCannotBecomePaidInvoice(t *testing.T) {
	// Eligibility is pure: this sentinel must never receive a Provider call.
	service := Service{Config: p.Config{Enabled: true}, Provider: struct{ p.Provider }{}}
	customer := int64(11)
	at := time.Now()
	order := orderport.SelfOrder{Order: o.Snapshot{Provider: o.ProviderNoPayment, RecordOrigin: o.RecordOriginNative, EffectEligible: true, Status: o.StatusPaid, PayerCustomerID: &customer, Amount: o.Money{Currency: "CNY"}}, ConfirmedPaidAt: &at}
	if reason := service.reason(order, []int64{customer}, false, nil); reason != "unsupported_channel" {
		t.Fatalf("free invoice eligibility=%s", reason)
	}
	order.Order.Provider = o.ProviderWeChatPay
	order.Order.ProviderTransactionNo = "forged"
	if reason := service.reason(order, []int64{customer}, false, nil); reason != "not_paid" {
		t.Fatalf("zero amount invoice eligibility=%s", reason)
	}
}
