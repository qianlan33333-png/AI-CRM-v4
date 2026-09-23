package http

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	ordermigration "github.com/qianlan33333-png/AI-CRM-v3/internal/order/migration"
)

const importSnapshot = `{"schema_version":"aicrm-commerce-history-v3","run_key":"order-snapshot-http-0001","coverage":{"identities":false,"wechat_pay_orders":true,"wechat_pay_refunds":false,"wechat_shop_orders":false,"wechat_shop_refunds":false,"alipay_orders":false},"subjects":[],"identities":[],"identity_quarantines":[],"orders":[{"provider":"wechat_pay","source_key":"legacy-1","merchant_order_no":"merchant-1","provider_transaction_no":"transaction-1","payer_identity_key":"","payer_subject_key":"","beneficiary_subject_key":"","amount_minor":100,"currency":"CNY","status":"paid","items":[{"line_no":1,"product_code":"legacy","product_name":"历史交易","unit_amount_minor":100,"quantity":1,"line_amount_minor":100}],"created_at":"2026-09-01T00:00:00Z","updated_at":"2026-09-01T00:00:00Z"}],"refunds":[]}`

type importAppStub struct{ applies int }

func (stub *importAppStub) Apply(context.Context, ordermigration.Manifest) (ordermigration.Result, error) {
	stub.applies++
	return ordermigration.Result{Orders: 1}, nil
}

type reconcileStub struct{}

func (reconcileStub) ReconcileOrders(context.Context, ordermigration.Manifest) (ordermigration.OrderOnlyReconciliation, error) {
	return ordermigration.OrderOnlyReconciliation{Matched: true, Orders: 1, AmountMinor: 100, Floating: 1}, nil
}

func importRequest(path string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(importSnapshot))
	digest := sha256.Sum256([]byte(importSnapshot))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Manifest-SHA256", hex.EncodeToString(digest[:]))
	request.Header.Set("Idempotency-Key", "order-snapshot-http-0001")
	request.Header.Set("X-Confirm-Apply", "order-snapshot-http-0001")
	return request
}

func TestOrderImportAllowsDailyBusinessAdminAndKeepsViewerOut(t *testing.T) {
	app := &importAppStub{}
	security := securityStub{principal: accessdomain.Principal{InternalID: 9, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}}
	handler, _ := NewImportHandler(app, reconcileStub{}, security)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, importRequest("/api/admin/order-imports/apply"))
	if response.Code != http.StatusOK || app.applies != 1 {
		t.Fatalf("admin import status=%d applies=%d", response.Code, app.applies)
	}

	security.principal.Roles = []accessdomain.Role{accessdomain.RoleViewer}
	handler, _ = NewImportHandler(app, reconcileStub{}, security)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, importRequest("/api/admin/order-imports/apply"))
	if response.Code != http.StatusForbidden || app.applies != 1 {
		t.Fatalf("viewer import status=%d applies=%d", response.Code, app.applies)
	}

	security.principal.Roles = []accessdomain.Role{accessdomain.RoleSuperAdmin}
	handler, _ = NewImportHandler(app, reconcileStub{}, security)
	request := importRequest("/api/admin/order-imports/apply")
	request.Header.Set("X-Manifest-SHA256", strings.Repeat("0", 64))
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusConflict || app.applies != 1 {
		t.Fatalf("digest mismatch status=%d applies=%d", response.Code, app.applies)
	}
}

func TestOrderImportAppliesConfirmedOrderOnlySnapshot(t *testing.T) {
	app := &importAppStub{}
	security := securityStub{principal: accessdomain.Principal{InternalID: 9, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleSuperAdmin}}}
	handler, _ := NewImportHandler(app, reconcileStub{}, security)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, importRequest("/api/admin/order-imports/apply"))
	if response.Code != http.StatusOK || app.applies != 1 || !strings.Contains(response.Body.String(), `"Orders":1`) {
		t.Fatalf("status=%d applies=%d body=%s", response.Code, app.applies, response.Body.String())
	}
}
