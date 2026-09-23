package paymenthttp

import (
	"context"
	"errors"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type abandonApp struct {
	appStub
	command paymentport.AbandonCheckoutCommand
	calls   int
}

func (a *abandonApp) AbandonCheckout(_ context.Context, c paymentport.AbandonCheckoutCommand) error {
	a.calls++
	a.command = c
	return nil
}

type abandonSecurity struct {
	securityStub
	principal accessdomain.Principal
	err       error
}

func (s abandonSecurity) AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error) {
	return s.principal, s.err
}
func TestAbandonCheckoutRequiresAdminCSRFAndUsesAuthenticatedActor(t *testing.T) {
	for _, name := range []string{"admin", "super_admin", "csrf_failed", "missing_role", "nonadmin"} {
		t.Run(name, func(t *testing.T) {
			app := &abandonApp{}
			sec := abandonSecurity{principal: accessdomain.Principal{InternalID: 4, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}}
			switch name {
			case "super_admin":
				sec.principal.Roles = []accessdomain.Role{accessdomain.RoleSuperAdmin}
			case "csrf_failed":
				sec.err = errors.New("csrf")
			case "missing_role":
				sec.principal.Roles = nil
			case "nonadmin":
				sec.principal.Kind = "customer"
			}
			h, _ := NewHandler(app, nil, sec, true)
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/api/admin/wechat-pay/payments/7/abandon-checkout", strings.NewReader(`{"confirmed_no_debit":true,"evidence_digest":"`+strings.Repeat("a", 64)+`"}`))
			r.Header.Set("Content-Type", "application/json")
			h.ServeHTTP(w, r)
			if name == "admin" || name == "super_admin" {
				if w.Code != 200 || app.calls != 1 || app.command.ActorScope != "admin:4" {
					t.Fatalf("status=%d calls=%d", w.Code, app.calls)
				}
			} else if w.Code != 403 || app.calls != 0 {
				t.Fatalf("unauthorized status=%d calls=%d", w.Code, app.calls)
			}
		})
	}
}
