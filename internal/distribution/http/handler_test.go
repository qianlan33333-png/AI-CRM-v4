package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	distributiondomain "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/domain"
	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
)

type distributionHTTPRegistrationStub struct{}

func (distributionHTTPRegistrationStub) CurrentAgreement(context.Context) (distributionport.Agreement, error) {
	return distributionport.Agreement{}, nil
}
func (distributionHTTPRegistrationStub) Profile(context.Context, distributionport.TrustedSessionActor) (distributionport.DistributorProfile, error) {
	return distributionport.DistributorProfile{}, nil
}
func (distributionHTTPRegistrationStub) Register(context.Context, distributionport.RegisterCommand) (distributionport.DistributorProfile, error) {
	return distributionport.DistributorProfile{}, nil
}
func (distributionHTTPRegistrationStub) PrepareReceiver(context.Context, distributionport.TrustedSessionActor) (distributionport.ReceiverPreparationResult, error) {
	return distributionport.ReceiverPreparationResult{}, nil
}

type distributionHTTPPromotionStub struct{}

func (distributionHTTPPromotionStub) ListPromotionProducts(context.Context, distributionport.TrustedSessionActor, string, int32) (distributionport.PromotionPage, error) {
	return distributionport.PromotionPage{}, nil
}
func (distributionHTTPPromotionStub) ApplicationTarget(_ context.Context, id int64, kind distributiondomain.ProductType) (distributionport.ApplicationTarget, error) {
	if id != 7 || kind != distributiondomain.ProductTypeStandard {
		return distributionport.ApplicationTarget{}, distributionport.ErrNotFound
	}
	return distributionport.ApplicationTarget{ProductID: id, ProductType: kind, PolicyEnabled: true, ProductName: "申请商品", PurchaseURL: "/p/application-product"}, nil
}
func (distributionHTTPPromotionStub) IssuePromotionLink(context.Context, distributionport.IssuePromotionCommand) (distributionport.PromotionLink, error) {
	return distributionport.PromotionLink{}, nil
}
func (distributionHTTPPromotionStub) ResolvePromotionTarget(context.Context, string) (string, error) {
	return "", distributionport.ErrNotFound
}

type distributionHTTPCredentialPromotionStub struct {
	issued distributionport.IssuePromotionCommand
}

func (stub *distributionHTTPCredentialPromotionStub) ListPromotionProducts(context.Context, distributionport.TrustedSessionActor, string, int32) (distributionport.PromotionPage, error) {
	return distributionport.PromotionPage{Items: []distributionport.PromotionProduct{{ProductID: 7, ProductType: distributiondomain.ProductTypeStandard}}}, nil
}
func (*distributionHTTPCredentialPromotionStub) ApplicationTarget(context.Context, int64, distributiondomain.ProductType) (distributionport.ApplicationTarget, error) {
	return distributionport.ApplicationTarget{}, distributionport.ErrNotFound
}
func (stub *distributionHTTPCredentialPromotionStub) IssuePromotionLink(_ context.Context, command distributionport.IssuePromotionCommand) (distributionport.PromotionLink, error) {
	stub.issued = command
	return distributionport.PromotionLink{URL: "https://crm.example.test/d/dpc_12345678901234567890", ExpiresAt: time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)}, nil
}
func (*distributionHTTPCredentialPromotionStub) ResolvePromotionTarget(context.Context, string) (string, error) {
	return "", distributionport.ErrNotFound
}

type distributionHTTPSessionStub struct{}

func (distributionHTTPSessionStub) Resolve(context.Context, string) (distributionport.TrustedSessionActor, error) {
	return distributionport.TrustedSessionActor{CustomerID: 11, IdentityID: 12, AppID: "wx-test", AppScope: "wechat-app:test", Channel: "mini_program", OccurredAt: time.Now().UTC()}, nil
}

type distributionHTTPBridgeStub struct{}

func (distributionHTTPBridgeStub) BridgePaymentSession(context.Context, string) (string, time.Time, error) {
	return "", time.Time{}, distributionport.ErrUnavailable
}

type distributionHTTPEarningsStub struct{}

func (distributionHTTPEarningsStub) Earnings(context.Context, distributionport.TrustedSessionActor) (distributionport.Earnings, error) {
	return distributionport.Earnings{}, nil
}
func (distributionHTTPEarningsStub) ListCommissions(_ context.Context, actor distributionport.TrustedSessionActor, status distributiondomain.CommissionStatus, cursor string, limit int32) (distributionport.CommissionPage, error) {
	if actor.CustomerID != 11 || status != distributiondomain.CommissionPending || cursor != "3" || limit != 20 {
		return distributionport.CommissionPage{}, distributionport.ErrConflict
	}
	now := time.Date(2026, 9, 14, 8, 0, 0, 0, time.UTC)
	return distributionport.CommissionPage{Items: []distributionport.CommissionListItem{
		{CommissionID: "4", OrderReference: "order-9001", ProductName: "无确认事实", InitialMinor: 990, CurrentPayableMinor: 880, PaidMinor: 0, Status: "pending", PaidConfirmedAt: now, DueAt: now.Add(24 * time.Hour), CreatedAt: now, Currency: "CNY"},
		{CommissionID: "5", OrderReference: "order-9002", ProductName: "已确认事实", InitialMinor: 990, CurrentPayableMinor: 880, PaidMinor: 880, Status: "paid", PaidConfirmedAt: now, DueAt: now.Add(24 * time.Hour), SettlementConfirmedAt: now.Add(time.Hour), PaidAt: now.Add(time.Hour), CreatedAt: now, Currency: "CNY"},
	}, NextCursor: "5"}, nil
}

func TestCommissionsResponseMapsFrozenReadModelToPublicContract(t *testing.T) {
	h, err := NewHandler(Config{Registration: distributionHTTPRegistrationStub{}, Promotion: distributionHTTPPromotionStub{}, Earnings: distributionHTTPEarningsStub{}, Sessions: distributionHTTPSessionStub{}, Bridge: distributionHTTPBridgeStub{}, AllowedOrigins: []string{"https://crm.example.test"}})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "/api/v1/distribution/commissions?status=pending&cursor=3&limit=20", nil)
	r.AddCookie(&http.Cookie{Name: DistributionSessionCookieName, Value: "trusted"})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	body := w.Body.String()
	for _, wanted := range []string{`"items":[{`, `"commission_id":"4"`, `"order_reference":"order-9001"`, `"product_name":"无确认事实"`, `"commission_id":"5"`, `"paid_at":"2026-09-14T09:00:00Z"`, `"next_cursor":"5"`} {
		if !strings.Contains(body, wanted) {
			t.Fatalf("response missing %s: %s", wanted, body)
		}
	}
	if strings.Count(body, `"paid_at"`) != 1 {
		t.Fatalf("missing confirmation fact must omit paid_at: %s", body)
	}
	if w.Code != http.StatusOK || strings.Contains(body, `"Items"`) || strings.Contains(body, `"NextCursor"`) {
		t.Fatalf("response=%d body=%s", w.Code, body)
	}
}

func TestProfileResponseKeepsOnlySafeReceiverPermissionClass(t *testing.T) {
	response := profileResponse(distributionport.DistributorProfile{Receiver: distributionport.ReceiverReadiness{Reason: "receiver_provider_permission_denied"}})
	receiver := response["receiver"].(map[string]any)
	if receiver["reason"] != "receiver_provider_permission_denied" {
		t.Fatalf("receiver response=%+v", receiver)
	}
}

func TestApplicationContextIsStrictPublicProductReadWithoutSession(t *testing.T) {
	h, err := NewHandler(Config{Registration: distributionHTTPRegistrationStub{}, Promotion: distributionHTTPPromotionStub{}, Earnings: distributionHTTPEarningsStub{}, Sessions: distributionHTTPSessionStub{}, Bridge: distributionHTTPBridgeStub{}, AllowedOrigins: []string{"https://crm.example.test"}})
	if err != nil {
		t.Fatal(err)
	}
	valid := httptest.NewRequest(http.MethodGet, "/api/v1/distribution/application-context?product_id=7&product_type=standard_product", nil)
	response := httptest.NewRecorder()
	h.ServeHTTP(response, valid)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"purchase_url":"/p/application-product"`) || strings.Contains(response.Body.String(), "customer") {
		t.Fatalf("response=%d body=%s", response.Code, response.Body.String())
	}
	for _, path := range []string{
		"/api/v1/distribution/application-context?product_id=07&product_type=standard_product",
		"/api/v1/distribution/application-context?product_id=7&product_type=standard_product&next=/pay/x",
		"/api/v1/distribution/application-context?product_id=7&product_id=8&product_type=standard_product",
		"/api/v1/distribution/application-context?product_id=7&product_type=unknown",
	} {
		bad := httptest.NewRequest(http.MethodGet, path, nil)
		out := httptest.NewRecorder()
		h.ServeHTTP(out, bad)
		if out.Code != http.StatusBadRequest {
			t.Fatalf("path=%s code=%d body=%s", path, out.Code, out.Body.String())
		}
	}
}

func TestIssueCredentialUsesServerCandidateAndExactPublicContract(t *testing.T) {
	promotion := &distributionHTTPCredentialPromotionStub{}
	h, err := NewHandler(Config{Registration: distributionHTTPRegistrationStub{}, Promotion: promotion, Earnings: distributionHTTPEarningsStub{}, Sessions: distributionHTTPSessionStub{}, Bridge: distributionHTTPBridgeStub{}, AllowedOrigins: []string{"https://crm.example.test"}})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/distribution/products/7/promotion-credentials", nil)
	request.Header.Set("Origin", "https://crm.example.test")
	request.Header.Set("Idempotency-Key", "distribution-credential-7")
	request.Header.Set(DistributionCSRFHeader, "csrf-proof")
	request.AddCookie(&http.Cookie{Name: DistributionSessionCookieName, Value: "trusted"})
	request.AddCookie(&http.Cookie{Name: DistributionCSRFCookieName, Value: "csrf-proof"})
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || promotion.issued.ProductID != 7 || promotion.issued.ProductType != distributiondomain.ProductTypeStandard || promotion.issued.IdempotencyKey != "distribution-credential-7" || !strings.Contains(response.Body.String(), `"url":"https://crm.example.test/d/dpc_12345678901234567890"`) || !strings.Contains(response.Body.String(), `"expires_at":"2026-09-15T00:00:00Z"`) || strings.Contains(response.Body.String(), "product_type") {
		t.Fatalf("response=%d issued=%+v body=%s", response.Code, promotion.issued, response.Body.String())
	}
	bad := httptest.NewRequest(http.MethodPost, "/api/v1/distribution/products/7/promotion-credentials", strings.NewReader(`{"product_type":"service_period"}`))
	bad.Header.Set("Origin", "https://crm.example.test")
	bad.Header.Set("Idempotency-Key", "distribution-credential-invalid")
	bad.Header.Set(DistributionCSRFHeader, "csrf-proof")
	bad.AddCookie(&http.Cookie{Name: DistributionSessionCookieName, Value: "trusted"})
	bad.AddCookie(&http.Cookie{Name: DistributionCSRFCookieName, Value: "csrf-proof"})
	out := httptest.NewRecorder()
	h.ServeHTTP(out, bad)
	if out.Code != http.StatusMethodNotAllowed || promotion.issued.ProductType != distributiondomain.ProductTypeStandard {
		t.Fatalf("body must be rejected code=%d issued=%+v", out.Code, promotion.issued)
	}
}
