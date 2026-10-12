package main

import (
	"context"
	"errors"
	"testing"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/idempotency"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/webhook"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/wecom"
)

func TestPostgreSQLArchiveRecoveryFencedAuditedAndNoOtherProvider(t *testing.T) {
	ctx := context.Background()
	dsn, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()
	pool, err := platformpostgres.Open(ctx, platformpostgres.Config{URL: dsn})
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	inbox, err := webhook.NewService(webhook.NewPostgreSQLStore())
	if err != nil {
		t.Fatal(err)
	}
	p := wecom.ArchiveInboxProcessor{Enabled: true, Inbox: inbox, UOW: uow}
	seed := func(provider, label string) int64 {
		t.Helper()
		var d webhook.IngestResult
		key, _ := idempotency.Parse("archive-recover-fixture:" + label)
		if e := uow.Within(ctx, func(tx context.Context) error {
			var e error
			d, e = inbox.Ingest(tx, webhook.Ingest{Provider: provider, IdempotencyKey: key, Payload: []byte(`{"corp_id":"wx-fixture","event":"msgaudit_notify"}`)})
			return e
		}); e != nil {
			t.Fatal(e)
		}
		if _, e := pool.Native().Exec(ctx, `UPDATE webhook_inbox SET status='failed',attempt_count=8,max_attempts=8,last_error_code='archive_processing_failed' WHERE id=$1`, d.Delivery.ID); e != nil {
			t.Fatal(e)
		}
		return d.Delivery.ID
	}
	id := seed("wecom.message_archive", "original")
	other := seed("payment.callback", "other")
	for _, tc := range []struct {
		id           int64
		attempt, uid int
	}{{id, 8, 501}, {id, 7, 0}, {other, 8, 0}} {
		if _, e := recoverArchiveNotification(ctx, p, tc.id, tc.attempt, tc.uid); !errors.Is(e, errArchiveRecoveryRejected) {
			t.Fatal("unauthorized/stale/other-provider recovery accepted")
		}
	}
	var before []byte
	if err = pool.Native().QueryRow(ctx, `SELECT payload_hash FROM webhook_inbox WHERE id=$1`, id).Scan(&before); err != nil {
		t.Fatal(err)
	}
	got, err := recoverArchiveNotification(ctx, p, id, 8, 0)
	if err != nil || got.Status != webhook.StatusRetryable || got.AttemptCount != 8 || got.MaxAttempts != 9 {
		t.Fatal("existing retry kernel not used", err)
	}
	if _, err = recoverArchiveNotification(ctx, p, id, 8, 0); !errors.Is(err, errArchiveRecoveryRejected) {
		t.Fatal("repeat recovery requeued another attempt")
	}
	var audits int
	var preserved bool
	if err = pool.Native().QueryRow(ctx, `SELECT (SELECT count(*) FROM audit_events WHERE action='archive.notification.recover'),payload_hash=$2 FROM webhook_inbox WHERE id=$1`, id, before).Scan(&audits, &preserved); err != nil || audits != 1 || !preserved {
		t.Fatal("audit missing or authenticated payload changed")
	}
	rollback := seed("wecom.message_archive", "rollback")
	if _, err = pool.Native().Exec(ctx, `CREATE FUNCTION archive_repair_deny_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture deny audit'; END $$; CREATE TRIGGER archive_repair_audit_failure BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION archive_repair_deny_audit()`); err != nil {
		t.Fatal(err)
	}
	if _, err = recoverArchiveNotification(ctx, p, rollback, 8, 0); !errors.Is(err, errArchiveRecoveryRejected) {
		t.Fatal("audit failure accepted recovery")
	}
	var state string
	var max int
	if err = pool.Native().QueryRow(ctx, `SELECT status,max_attempts FROM webhook_inbox WHERE id=$1`, rollback).Scan(&state, &max); err != nil || state != "failed" || max != 8 {
		t.Fatal("retry escaped failed audit transaction")
	}
}
