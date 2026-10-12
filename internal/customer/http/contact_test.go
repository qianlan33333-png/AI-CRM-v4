package http

import (
	"context"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type contactReaderFake struct{ calls int }

func (f *contactReaderFake) ReadContactWithin(context.Context, customerdomain.CustomerID, bool) (customerport.ContactProfile, error) {
	f.calls++
	return customerport.ContactProfile{MobileE164: "+8613800000000", Shipping: &customerport.ShippingContact{DetailAddress: "私密地址"}}, nil
}
func TestContactRevealRetainsPermissionAndAudit(t *testing.T) {
	for _, role := range []accessdomain.Role{accessdomain.RoleViewer, accessdomain.RoleAdmin} {
		reader := &contactReaderFake{}
		audit := &testAudit{}
		cfg := testConfig(testSecurity{principal: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 7, Roles: []accessdomain.Role{role}}}, &testCustomerStore{}, &testIdentities{}, audit)
		cfg.Contacts = reader
		h, e := NewHandler(cfg)
		if e != nil {
			t.Fatal(e)
		}
		w := httptest.NewRecorder()
		h.Routes().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/admin/customers/1/contact-reveal", nil))
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("PII response cached")
		}
		if role == accessdomain.RoleViewer {
			if w.Code != 403 || reader.calls != 0 || strings.Contains(w.Body.String(), "私密") {
				t.Fatal("unauthorized reveal")
			}
		} else {
			if w.Code != 200 || reader.calls != 1 || len(audit.events) != 1 || audit.events[0].Action != "customer.contact_revealed" {
				t.Fatal("missing audited reveal")
			}
			if strings.Contains(string(audit.events[0].Payload), "138") || strings.Contains(string(audit.events[0].Payload), "私密") {
				t.Fatal("PII in audit")
			}
		}
	}
}
