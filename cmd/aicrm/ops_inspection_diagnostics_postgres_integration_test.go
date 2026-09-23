package main

import (
	"context"
	automationstore "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/store"
	configstore "github.com/qianlan33333-png/AI-CRM-v3/internal/config/store"
	customerstore "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/store"
	identitystore "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/store"
	mediastore "github.com/qianlan33333-png/AI-CRM-v3/internal/media/store"
	operationstore "github.com/qianlan33333-png/AI-CRM-v3/internal/operationcycle/store"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/outbound"
	paymentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/store"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	segmentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/store"
	surveystore "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/store"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/wecom"
	"strings"
	"testing"
	"time"
)

// Validate the Owner aggregate SQL against the complete real PostgreSQL schema,
// not mocks or copied table definitions. Composition alone may join the Ports.
func TestPostgreSQLOpsInspectionOwnerDiagnostics(t *testing.T) {
	ctx := context.Background()
	dsn, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()
	pool, e := platformpostgres.Open(ctx, platformpostgres.Config{URL: dsn})
	if e != nil {
		t.Fatal(e)
	}
	defer pool.Close()
	p := pool.Native()
	now := time.Now().UTC()
	type reader interface {
		ReadOpsDiagnosticCounts(context.Context, time.Time) (map[string]int64, error)
	}
	readers := map[string]reader{
		"survey":   surveystore.NewOpsDiagnosticReader(p),
		"identity": identitystore.NewOpsDiagnosticReader(p), "customer": customerstore.NewOpsDiagnosticReader(p),
		"config": configstore.NewOpsDiagnosticReader(p), "payment": paymentstore.NewOpsDiagnosticReader(p),
		"outbound": outbound.NewOpsDiagnosticReader(p), "wecom": wecom.NewOpsDiagnosticReader(p),
		"segment": segmentstore.NewOpsDiagnosticReader(p), "automation": automationstore.NewOpsDiagnosticReader(p),
		"media": mediastore.NewOpsDiagnosticReader(p), "operationcycle": operationstore.NewOpsDiagnosticReader(p),
	}
	for owner, r := range readers {
		t.Run(owner+"_empty_schema", func(t *testing.T) {
			counts, e := r.ReadOpsDiagnosticCounts(ctx, now)
			if e != nil {
				t.Fatal(e)
			}
			if len(counts) == 0 {
				t.Fatal("no implemented observations")
			}
			for key, n := range counts {
				if n != 0 {
					t.Fatalf("empty %s=%d", key, n)
				}
			}
		})
	}
	var left, right int64
	if e = p.QueryRow(ctx, `INSERT INTO customers DEFAULT VALUES RETURNING id`).Scan(&left); e != nil {
		t.Fatal(e)
	}
	if e = p.QueryRow(ctx, `INSERT INTO customers DEFAULT VALUES RETURNING id`).Scan(&right); e != nil {
		t.Fatal(e)
	}
	if _, e = p.Exec(ctx, `INSERT INTO customer_identity_conflicts(left_customer_id,right_customer_id,reason) VALUES($1,$2,'diagnostic_fixture')`, left, right); e != nil {
		t.Fatal(e)
	}
	counts, e := readers["identity"].ReadOpsDiagnosticCounts(ctx, now)
	if e != nil || counts["open_conflicts"] != 1 {
		t.Fatalf("conflicts=%v %v", counts, e)
	}
	if _, e = p.Exec(ctx, `INSERT INTO config_runtime_applications(revision,source,role,release_sha,snapshot_checksum,applied_at) VALUES(7,'published','api','fixture',$1,$2)`, strings.Repeat("a", 64), now); e != nil {
		t.Fatal(e)
	}
	counts, e = readers["config"].ReadOpsDiagnosticCounts(ctx, now)
	if e != nil || counts["revision_mismatch"] != 1 || counts["roles_observed"] != 1 {
		t.Fatalf("configuration=%v %v", counts, e)
	}
	// An old queued intent may represent a future scheduled send. Its age alone
	// must not imply overdue delivery; only a started stale attempt is timed.
	if _, e = p.Exec(ctx, `INSERT INTO outbound_private_message_intents(source_reference,customer_id,staff_id,payload_reference,source_digest,target_digest,payload_digest,policy_hash,receipt_key,external_effect_id,state,created_at,updated_at)
 VALUES('diagnostic-fixture',1,1,'fixture',decode(repeat('ab',32),'hex'),decode(repeat('bc',32),'hex'),decode(repeat('cd',32),'hex'),decode(repeat('de',32),'hex'),decode(repeat('ef',32),'hex'),'eer_900001','queued',$1,$1)`, now.Add(-2*time.Hour)); e != nil {
		t.Fatal(e)
	}
	counts, e = readers["outbound"].ReadOpsDiagnosticCounts(ctx, now)
	if e != nil || counts["message_overdue"] != 0 {
		t.Fatalf("future queued false alarm=%v %v", counts, e)
	}
	if _, e = p.Exec(ctx, `UPDATE outbound_private_message_intents SET state='attempted' WHERE source_reference='diagnostic-fixture'`); e != nil {
		t.Fatal(e)
	}
	counts, e = readers["outbound"].ReadOpsDiagnosticCounts(ctx, now)
	if e != nil || counts["message_overdue"] != 1 {
		t.Fatalf("stale attempt=%v %v", counts, e)
	}
	// Source failure must be surfaced, never silently converted to an empty map.
	if _, e = p.Exec(ctx, `ALTER TABLE customer_identity_conflicts RENAME TO diagnostic_test_missing_conflicts`); e != nil {
		t.Fatal(e)
	}
	if counts, e = readers["identity"].ReadOpsDiagnosticCounts(ctx, now); e == nil || counts != nil {
		t.Fatalf("missing source=%v %v", counts, e)
	}
}
