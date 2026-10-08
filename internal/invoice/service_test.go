package invoice

import (
	"errors"
	p "github.com/qianlan33333-png/AI-CRM-v3/internal/invoice/port"
	o "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
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
	s := Service{Config: p.Config{MerchantID: "merchant", SellerTaxID: "taxpayer", BillingPersonID: "person", Taxes: map[string]p.Tax{"course": {Code: "3010101020203000000", Rate: 600, GoodsName: "课程服务"}}}}
	order := o.Snapshot{Amount: o.Money{AmountMinor: 9900000000000, Currency: "CNY"}, Items: []o.ItemSnapshot{{ProductCode: "course", Quantity: 1, LineAmountMinor: 10000000000000}, {ProductCode: "course", Quantity: 1, LineAmountMinor: 10000000000000}}}
	snapshot, e := s.freeze(order)
	if e != nil || snapshot.Lines[0].TotalAmount != 4950000000000 || snapshot.Lines[0].TotalAmount+snapshot.Lines[1].TotalAmount != order.Amount.AmountMinor {
		t.Fatalf("discount allocation/overflow=%+v %v", snapshot, e)
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
	if validApplicationID("../../admin") || validApplicationID("inv_"+strings.Repeat("g", 28)) {
		t.Fatal("invalid application route accepted")
	}
}
