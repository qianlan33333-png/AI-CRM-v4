package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
)

type review20261009PagedPromotion struct {
	distributionHTTPCredentialPromotionStub
	cursors []string
	pages   map[string]distributionport.PromotionPage
}

func (p *review20261009PagedPromotion) ListPromotionProducts(_ context.Context, _ distributionport.TrustedSessionActor, cursor string, _ int32) (distributionport.PromotionPage, error) {
	p.cursors = append(p.cursors, cursor)
	return p.pages[cursor], nil
}

func TestDistributionReview20261009CredentialUsesEligibleLaterPage(t *testing.T) {
	promotion := &review20261009PagedPromotion{pages: map[string]distributionport.PromotionPage{
		"":    {NextCursor: "500"},
		"500": {Items: []distributionport.PromotionProduct{{ProductID: 7, ProductType: "standard_product"}}},
	}}
	w := review20261009IssueCredential(t, promotion, nil)
	if w.Code != http.StatusCreated || promotion.issued.ProductID != 7 || len(promotion.cursors) != 2 || promotion.cursors[1] != "500" {
		t.Fatalf("eligible product in filtered later page: status=%d issued=%+v cursors=%v body=%s", w.Code, promotion.issued, promotion.cursors, w.Body.String())
	}
}

func review20261009IssueCredential(t *testing.T, promotion *review20261009PagedPromotion, change func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	h, err := NewHandler(Config{Registration: distributionHTTPRegistrationStub{}, Promotion: promotion, Sessions: distributionHTTPSessionStub{}, Bridge: distributionHTTPBridgeStub{}, AllowedOrigins: []string{"https://crm.example.test"}})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/v1/distribution/products/7/promotion-credentials", nil)
	r.Header.Set("Origin", "https://crm.example.test")
	r.Header.Set("Idempotency-Key", "review-later-page-credential")
	r.Header.Set(DistributionCSRFHeader, "review-csrf")
	r.AddCookie(&http.Cookie{Name: DistributionSessionCookieName, Value: "trusted"})
	r.AddCookie(&http.Cookie{Name: DistributionCSRFCookieName, Value: "review-csrf"})
	if change != nil {
		change(r)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestDistributionReview20261009CredentialKeepsFilterAndRejectsCursorCycles(t *testing.T) {
	for _, test := range []struct {
		name   string
		next   string
		status int
	}{
		{"filtered_target_absent", "", http.StatusNotFound},
		{"repeated_cursor", "500", http.StatusServiceUnavailable},
		{"backward_cursor", "100", http.StatusServiceUnavailable},
		{"noncanonical_cursor", "0501", http.StatusServiceUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			promotion := &review20261009PagedPromotion{pages: map[string]distributionport.PromotionPage{
				"":    {NextCursor: "500"},
				"500": {Items: []distributionport.PromotionProduct{{ProductID: 8, ProductType: "standard_product"}}, NextCursor: test.next},
			}}
			w := review20261009IssueCredential(t, promotion, nil)
			if w.Code != test.status || promotion.issued.ProductID != 0 || len(promotion.cursors) != 2 {
				t.Fatalf("status=%d issued=%+v cursors=%v", w.Code, promotion.issued, promotion.cursors)
			}
		})
	}
}

func TestDistributionReview20261009CredentialRejectsUnauthenticatedAndCrossSiteRequests(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		change func(*http.Request)
	}{
		{"cross_site", http.StatusForbidden, func(r *http.Request) { r.Header.Set("Origin", "https://untrusted.test") }},
		{"wrong_csrf", http.StatusForbidden, func(r *http.Request) { r.Header.Set(DistributionCSRFHeader, "other-proof") }},
		{"missing_session", http.StatusUnauthorized, func(r *http.Request) {
			r.Header.Del("Cookie")
			r.AddCookie(&http.Cookie{Name: DistributionCSRFCookieName, Value: "review-csrf"})
		}},
		{"invalid_idempotency", http.StatusBadRequest, func(r *http.Request) { r.Header.Set("Idempotency-Key", "short") }},
	} {
		t.Run(test.name, func(t *testing.T) {
			promotion := &review20261009PagedPromotion{pages: map[string]distributionport.PromotionPage{"": {Items: []distributionport.PromotionProduct{{ProductID: 7, ProductType: "standard_product"}}}}}
			w := review20261009IssueCredential(t, promotion, test.change)
			if w.Code != test.status || promotion.issued.ProductID != 0 || len(promotion.cursors) != 0 {
				t.Fatalf("status=%d issued=%+v cursors=%v", w.Code, promotion.issued, promotion.cursors)
			}
		})
	}
}
