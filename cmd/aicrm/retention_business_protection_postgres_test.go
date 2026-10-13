package main

import (
	"context"
	"crypto/sha256"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	adminops "github.com/qianlan33333-png/AI-CRM-v3/internal/adminops"
	adminstore "github.com/qianlan33333-png/AI-CRM-v3/internal/adminops/store"
	configapp "github.com/qianlan33333-png/AI-CRM-v3/internal/config/app"
	mediastore "github.com/qianlan33333-png/AI-CRM-v3/internal/media/store"
)

func TestPostgreSQLRetentionPreservesInvoiceReceiptsAndPrivateKey(t *testing.T) {
	f := newInvoiceFixture(t)
	ctx := context.Background()
	old := time.Now().UTC().Add(-90 * 24 * time.Hour)
	order := f.order(f.customer, f.customer, "paid", "Retained invoice", "wechat_pay", old)
	accepted, err := f.s.Create(ctx, f.token, order, "retention-invoice-original-command")
	if err != nil || accepted.ID == "" {
		t.Fatalf("prepare genuine Invoice acceptance: accepted=%t err=%v", accepted.ID != "", err)
	}
	// Old timestamps and synthetic callback/card facts are fixture setup only;
	// the actual business application, original receipt and audit already exist.
	if _, err = f.pool.Exec(ctx, `INSERT INTO invoice_callback_receipts(event_digest,body_digest,application_id,created_at) VALUES('retention-synthetic-event','retention-synthetic-body',$1,$2)`, accepted.ID, old); err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(ctx, `INSERT INTO invoice_card_setup_intents(merchant_id,effect_id,payload_digest,material,actor_id,created_at) VALUES('retention-synthetic-merchant','retention-synthetic-effect','retention-synthetic-payload','{}',1,$1)`, old); err != nil {
		t.Fatal(err)
	}
	files := configapp.InvoiceKeyFiles{Directory: filepath.Join(t.TempDir(), "private-invoice-materials")}
	key := strings.Repeat("K", 32) // disposable local fixture; never a live key.
	ref, err := files.StageInvoiceKey(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	media, err := mediastore.NewPostgreSQL(f.pool, f.s.UoW)
	if err != nil {
		t.Fatal(err)
	}
	snapshots, err := adminstore.NewProjectionPostgreSQL(f.pool, f.s.UoW)
	if err != nil {
		t.Fatal(err)
	}
	retention, err := adminops.NewRetentionService(f.pool, media, snapshots, true)
	if err != nil {
		t.Fatal(err)
	}
	facts := func() map[string][32]byte {
		t.Helper()
		result := map[string][32]byte{}
		for _, table := range []string{"orders", "invoice_applications", "invoice_operation_receipts", "invoice_callback_receipts", "invoice_audit_events", "invoice_card_setup_intents", "external_effects", "river_job"} {
			var raw string
			if e := f.pool.QueryRow(ctx, `SELECT COALESCE(jsonb_agg(to_jsonb(f) ORDER BY to_jsonb(f)::text),'[]'::jsonb)::text FROM `+table+` f`).Scan(&raw); e != nil {
				t.Fatal(e)
			}
			result[table] = sha256.Sum256([]byte(raw))
		}
		return result
	}
	before := facts()
	for _, name := range []string{"invoice_applications", "invoice_operation_receipts", "invoice_callback_receipts", "invoice_audit_events", "invoice_card_setup_intents", "survey_redemption_codes", "media_private_images", "/var/lib/aicrm/invoice-materials"} {
		if _, err = retention.PruneRetentionBatch(ctx, name); err == nil {
			t.Fatalf("business object accepted as cleanup policy: %s", name)
		}
	}
	if _, err = f.pool.Exec(ctx, `INSERT INTO adminops_diagnostic_events(component,code,correlation_digest,occurred_at,release_sha) VALUES('api','http_server_error','retention-synthetic-old-detail',$1,'fixture')`, old); err != nil {
		t.Fatal(err)
	}
	run, err := retention.PruneRetentionBatch(ctx, "ops_diagnostics")
	if err != nil || run.Deleted != 1 {
		t.Fatalf("approved process cleanup did not execute: deleted=%d err=%v", run.Deleted, err)
	}
	if !reflect.DeepEqual(facts(), before) {
		t.Fatal("process cleanup changed old financial facts, accepted effects, jobs or receipts")
	}
	if got, e := files.Read(ref); e != nil || got != key {
		t.Fatalf("private material was removed or changed: same=%t err=%v", got == key, e)
	}
}
