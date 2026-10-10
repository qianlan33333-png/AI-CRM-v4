package main

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

func TestPostgreSQLPaymentRefundRoleKeepsHTTPAuthAndNoDaemon(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	url, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()
	key := base64.RawStdEncoding.EncodeToString(make([]byte, 32))
	application, err := compose(ctx, platformconfig.Runtime{Role: platformconfig.RolePaymentRefund, PaymentRefundID: 1244, PaymentRefundOrderID: 5626, PaymentRefundAmountMinor: 680, PaymentRefundKey: "fixture-refund-key-0001", PaymentRefundAuthorization: "user-task-fixture", DatabaseURL: url, PublicOrigin: "https://refund-role.example.test", ReleaseSHA: "refund-role-fixture", WorkerOwner: "refund-role-fixture", WorkerLimit: 1, GroupOps: platformconfig.GroupOps{WebhookSecret: "refund-role-fixture-webhook"}, Survey: platformconfig.Survey{DataKey: key, IdentityPhoneDataKey: key}})
	if err != nil {
		t.Fatal(err)
	}
	defer application.Close()
	if application.paymentReconciliation == nil {
		t.Fatal("normal Payment service missing")
	}
	var daemons, effects int
	if err = application.pool.Native().QueryRow(ctx, `SELECT (SELECT count(*) FROM config_runtime_applications),(SELECT count(*) FROM external_effects)`).Scan(&daemons, &effects); err != nil {
		t.Fatal(err)
	}
	if daemons != 0 || effects != 0 {
		t.Fatal("composition started a daemon or money operation")
	}
	response := httptest.NewRecorder()
	application.handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/admin/payments/1244/refunds", nil))
	if response.Code != http.StatusForbidden {
		t.Fatalf("HTTP refund auth changed: %d", response.Code)
	}
	if err = application.pool.Native().QueryRow(ctx, `SELECT count(*) FROM external_effects`).Scan(&effects); err != nil || effects != 0 {
		t.Fatal("unauthenticated HTTP request created an effect")
	}
}
