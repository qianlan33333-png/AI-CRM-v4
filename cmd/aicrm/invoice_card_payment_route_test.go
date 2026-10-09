package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/invoice"
)

func TestInvoiceCardAndPaymentRoutesRequireTrustedSession(t *testing.T) {
	h := mountInvoice(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTeapot) }), &invoice.Handler{})
	for _, path := range []string{"/api/h5/me/orders/7/payment", "/api/h5/me/invoices/4500000490202610044108114483/card"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "session_required") || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(path, w.Code, w.Body.String(), w.Header())
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/unrelated", nil))
	if w.Code != http.StatusTeapot {
		t.Fatal("unrelated route intercepted", w.Code)
	}
}
