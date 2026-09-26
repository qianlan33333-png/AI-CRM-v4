package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

func servicePeriodIdentityTestHandler(t *testing.T) *ServicePeriodPublicHandler {
	t.Helper()
	h, err := NewServicePeriodPublicHandler(&servicePeriodPublicStub{product: productport.CheckoutProduct{ID: 71, ProductType: productport.ProductOptionServicePeriod, Code: "term-31", Name: "周期商品", PriceMinor: 99900, Currency: "CNY", Version: 1, ServicePeriodDurationDays: 90}})
	if err != nil {
		t.Fatal(err)
	}
	h.now = func() time.Time { return time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC) }
	return h
}

func TestServicePeriodStateDistinguishesIdentityFromNoEntitlement(t *testing.T) {
	for _, kind := range []string{"anonymous", "expired_session", "authenticated_no_entitlement", "active", "expired", "refunded"} {
		t.Run(kind, func(t *testing.T) {
			h := servicePeriodIdentityTestHandler(t)
			entitlements := servicePeriodEntitlementStub{}
			if kind == "active" || kind == "expired" || kind == "refunded" {
				end := h.now().Add(286 * 24 * time.Hour)
				status := kind
				if kind == "expired" {
					end = h.now().Add(-time.Hour)
					status = "active"
				}
				entitlements.page.Items = []orderport.Entitlement{{CustomerID: 11, ServiceProductID: 71, Status: status, EndAt: end}}
			}
			session := paymentport.SessionReader(servicePeriodSessionStub{})
			if kind == "expired_session" {
				session = servicePeriodExpiredSessionStub{}
			}
			if err := h.SetTrustedPublicState(servicePeriodTestUOW{}, session, entitlements); err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest("GET", "/api/h5/service-period-products/term-31?promotion_context=dpc_"+strings.Repeat("a", 43), nil)
			if kind != "anonymous" {
				req.AddCookie(&http.Cookie{Name: paymentport.TrustedSessionCookieName, Value: "service-period-trusted"})
			}
			response := httptest.NewRecorder()
			h.ServeHTTP(response, req)
			var state struct {
				Authenticated bool `json:"authenticated"`
				Entitlement   struct {
					Status        string `json:"status"`
					RemainingDays int    `json:"remaining_days"`
				} `json:"entitlement"`
				CTA         string `json:"cta_text"`
				CheckoutURL string `json:"checkout_url"`
			}
			if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &state) != nil {
				t.Fatalf("state status=%d", response.Code)
			}
			if state.Authenticated != (kind != "anonymous" && kind != "expired_session") {
				t.Fatalf("authentication=%t", state.Authenticated)
			}
			expectedStatus, expectedCTA := "none", "立即报名"
			if kind == "active" {
				expectedStatus, expectedCTA = "active", "立即续费"
				if state.Entitlement.RemainingDays != 286 {
					t.Fatalf("days=%d", state.Entitlement.RemainingDays)
				}
			}
			if kind == "expired" || kind == "refunded" {
				expectedStatus, expectedCTA = "expired", "重新开通"
			}
			if state.Entitlement.Status != expectedStatus || state.CTA != expectedCTA {
				t.Fatalf("state=%+v", state)
			}
			if state.CheckoutURL != "/s/term-31/pay?promotion_context=dpc_"+strings.Repeat("a", 43) {
				t.Fatal("promotion lost")
			}
		})
	}
}

type servicePeriodFailedRead struct{ servicePeriodEntitlementStub }

func (servicePeriodFailedRead) GetCustomerServicePeriodEntitlement(context.Context, int64, int64) (orderport.Entitlement, bool, error) {
	return orderport.Entitlement{}, false, errors.New("read failure")
}

func TestServicePeriodReadFailureRetainsRetryableIntroduction(t *testing.T) {
	h := servicePeriodIdentityTestHandler(t)
	if err := h.SetTrustedPublicState(servicePeriodTestUOW{}, servicePeriodSessionStub{}, servicePeriodFailedRead{}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/s/term-31", "/api/h5/service-period-products/term-31"} {
		request := httptest.NewRequest("GET", path, nil)
		request.AddCookie(&http.Cookie{Name: paymentport.TrustedSessionCookieName, Value: "service-period-trusted"})
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		if strings.HasPrefix(path, "/api/") {
			if response.Code != 503 {
				t.Fatalf("API=%d", response.Code)
			}
			continue
		}
		if response.Code != 200 || !strings.Contains(response.Body.String(), `"read_failed":true`) || !strings.Contains(response.Body.String(), "重新查询") {
			t.Fatalf("retryable detail=%d", response.Code)
		}
	}
}

func TestServicePeriodIdentityLifecycle(t *testing.T) {
	h := servicePeriodIdentityTestHandler(t)
	end := h.now().Add(286 * 24 * time.Hour)
	if err := h.SetTrustedPublicState(servicePeriodTestUOW{}, servicePeriodSessionStub{}, servicePeriodEntitlementStub{page: orderport.EntitlementPage{Items: []orderport.Entitlement{{CustomerID: 11, ServiceProductID: 71, Status: "active", EndAt: end}}}}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(h)
	defer server.Close()
	_, source, _, _ := runtime.Caller(0)
	command := exec.Command("node", filepath.Join(filepath.Dir(source), "service_period_identity_journey.mjs"), server.URL)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("identity lifecycle: %v\n%s", err, output)
	}
}
