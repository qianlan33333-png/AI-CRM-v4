package main

import (
	"context"
	"encoding/base64"
	"testing"
	"time"

	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

// The one-shot query must reuse the real Composition Root without recording a
// daemon startup or accepting a new payment effect. No Provider call is mocked
// into proof of real settlement: the isolated composition keeps it disabled.
func TestPostgreSQLProfitSharingReconcileRoleHasNoDaemonOrNewEffect(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()
	dataKey := base64.RawStdEncoding.EncodeToString(make([]byte, 32))
	application, err := compose(ctx, platformconfig.Runtime{
		Role:                     platformconfig.RoleProfitSharingReconcile,
		ProfitSharingReconcileID: 6,
		DatabaseURL:              databaseURL,
		PublicOrigin:             "https://profit-sharing-query.example.test",
		ReleaseSHA:               "profit-sharing-query-composition",
		WorkerOwner:              "profit-sharing-query-composition",
		WorkerLimit:              1,
		GroupOps:                 platformconfig.GroupOps{WebhookSecret: "profit-sharing-query-webhook-secret"},
		Survey:                   platformconfig.Survey{DataKey: dataKey, IdentityPhoneDataKey: dataKey},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer application.Close()
	if application.paymentReconciliation == nil {
		t.Fatal("original instruction query service absent")
	}
	var runtimeApplications, effects int
	if err = application.pool.Native().QueryRow(ctx, "SELECT (SELECT count(*) FROM config_runtime_applications), (SELECT count(*) FROM external_effects)").Scan(&runtimeApplications, &effects); err != nil {
		t.Fatal(err)
	}
	if runtimeApplications != 0 || effects != 0 {
		t.Fatalf("query composition accepted unintended writes: daemon=%d effects=%d", runtimeApplications, effects)
	}
}
