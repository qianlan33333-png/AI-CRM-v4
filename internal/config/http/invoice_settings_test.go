package http

import (
	"context"
	"errors"
	access "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	app "github.com/qianlan33333-png/AI-CRM-v3/internal/config/app"
	invoice "github.com/qianlan33333-png/AI-CRM-v3/internal/invoice/port"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestInvoiceKeyHTTPWriteOnlyAndAuthorization(t *testing.T) {
	key := strings.Repeat("A", 32)
	admin := access.Principal{InternalID: 7, Kind: access.KindAdmin, Roles: []access.Role{access.RoleAdmin}}
	cases := []struct {
		name, body string
		principal  access.Principal
		csrf       error
		code       int
	}{
		{"valid", `{"key":"` + key + `"}`, admin, nil, 201},
		{"csrf", `{"key":"` + key + `"}`, admin, errors.New("denied"), 403},
		{"anonymous", `{"key":"` + key + `"}`, access.Principal{}, nil, 403},
		{"viewer", `{"key":"` + key + `"}`, access.Principal{InternalID: 8, Kind: access.KindAdmin, Roles: []access.Role{access.RoleViewer}}, nil, 403},
		{"short", `{"key":"bad"}`, admin, nil, 400},
		{"extra", `{"key":"` + key + `","path":"/tmp/x"}`, admin, nil, 400},
		{"trailing", `{"key":"` + key + `"}{}`, admin, nil, 400},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, e := NewHandler(&testSettings{}, &testWizard{}, newTestConfig(), testProjections{}, testSecurity{principal: c.principal, csrf: c.csrf})
			if e != nil {
				t.Fatal(e)
			}
			dir := filepath.Join(t.TempDir(), "private")
			h.WithInvoiceKeys(app.InvoiceKeyFiles{Directory: dir})
			w := httptest.NewRecorder()
			h.ServeHTTP(w, adminSessionRequest(http.MethodPost, "/api/admin/config/invoice-key", strings.NewReader(c.body)))
			if w.Code != c.code {
				t.Fatalf("code=%d wanted=%d body=%s", w.Code, c.code, w.Body)
			}
			if strings.Contains(w.Body.String(), key) || strings.Contains(w.Body.String(), dir) || w.Header().Get("Cache-Control") != "private, no-store" {
				t.Fatal("private value exposed")
			}
			files, _ := filepath.Glob(filepath.Join(dir, "*.key"))
			if (c.code == 201 && len(files) != 1) || (c.code != 201 && len(files) != 0) {
				t.Fatal("unexpected file mutation")
			}
		})
	}
}

type invoiceCardAdmin struct {
	calls int
	err   error
}

func (s *invoiceCardAdmin) ReadCardSetup(context.Context) (invoice.CardSetup, error) {
	return invoice.CardSetup{State: "not_submitted", ConfigurationApplied: true}, nil
}
func (s *invoiceCardAdmin) AcceptCardSetup(context.Context, int64) (invoice.CardSetup, error) {
	s.calls++
	return invoice.CardSetup{}, s.err
}
func TestInvoiceCardHTTPAuthorizationAndPendingConfiguration(t *testing.T) {
	admin := access.Principal{InternalID: 7, Kind: access.KindAdmin, Roles: []access.Role{access.RoleAdmin}}
	h, e := NewHandler(&testSettings{}, &testWizard{}, newTestConfig(), testProjections{}, testSecurity{principal: admin})
	if e != nil {
		t.Fatal(e)
	}
	card := &invoiceCardAdmin{err: invoice.ErrConflict}
	h.WithInvoiceCard(card)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, adminSessionRequest("POST", "/api/admin/config/invoice-card", strings.NewReader(`{}`)))
	if w.Code != 409 || !strings.Contains(w.Body.String(), "invoice_card_config_not_applied") {
		t.Fatal("pending configuration not rejected")
	}
	h.security = testSecurity{principal: admin, csrf: errors.New("denied")}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, adminSessionRequest("POST", "/api/admin/config/invoice-card", strings.NewReader(`{}`)))
	if w.Code != 403 || card.calls != 1 {
		t.Fatal("card CSRF bypass")
	}
}
