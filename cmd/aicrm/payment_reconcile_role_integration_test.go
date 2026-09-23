package main

import (
	"context"
	"encoding/base64"
	"testing"
	"time"

	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

// TestPostgreSQLPaymentReconcileRoleComposesWithoutRuntimeApplicationRecord
// proves the controlled one-shot role uses the full production Composition
// without trying to insert a daemon-runtime fact into the deliberately closed
// config_runtime_applications role catalog. The Provider remains disabled in
// this composition check: no test network call may stand in for a signed
// production read, which is covered by Payment's PostgreSQL reconciliation
// journey.
func TestPostgreSQLPaymentReconcileRoleComposesWithoutRuntimeApplicationRecord(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()

	dataKey := base64.RawStdEncoding.EncodeToString(make([]byte, 32))
	application, err := compose(ctx, platformconfig.Runtime{
		Role:               platformconfig.RolePaymentReconcile,
		PaymentReconcileID: 930,
		DatabaseURL:        databaseURL,
		PublicOrigin:       "https://payment-reconcile.example.test",
		ReleaseSHA:         "payment-reconcile-composition",
		WorkerOwner:        "payment-reconcile-composition",
		WorkerLimit:        1,
		GroupOps:           platformconfig.GroupOps{WebhookSecret: "payment-reconcile-composition-webhook-secret"},
		Survey:             platformconfig.Survey{DataKey: dataKey, IdentityPhoneDataKey: dataKey},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer application.Close()
	if application.paymentReconciliation == nil {
		t.Fatal("payment reconciliation service was not composed")
	}
	var runtimeApplications int
	if err = application.pool.Native().QueryRow(ctx, "SELECT count(*) FROM config_runtime_applications").Scan(&runtimeApplications); err != nil {
		t.Fatal(err)
	}
	if runtimeApplications != 0 {
		t.Fatalf("one-shot payment repair wrote %d daemon runtime applications", runtimeApplications)
	}
}
