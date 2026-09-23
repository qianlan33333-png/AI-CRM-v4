package externaleffects

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	platformjobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"github.com/riverqueue/river"
)

func TestTagCatalogRetryEvidenceFailsClosed(t *testing.T) {
	disabled := Hash("provider-disabled", "7", "1")
	rejected := Hash("wecom.tag.catalog.mutation.rejected", "eer_7", "1")
	for _, tt := range []struct {
		name                              string
		state                             State
		receipt                           Digest
		called, executed, completed, want bool
	}{
		{"dispatch read blocked", StateFinalFailed, Hash("wecom.tag.catalog.mutation.dispatch_changed", string(Hash("envelope"))), false, false, true, true},
		{"dispatch hash mismatch", StateFinalFailed, Hash("wecom.tag.catalog.mutation.dispatch_changed", string(Hash("other"))), false, false, true, false},
		{"dispatch called", StateFinalFailed, Hash("wecom.tag.catalog.mutation.dispatch_changed", string(Hash("envelope"))), true, false, true, false},
		{"dispatch unknown", StateUnknown, Hash("wecom.tag.catalog.mutation.dispatch_changed", string(Hash("envelope"))), false, false, true, false},
		{"disabled", StateFinalFailed, disabled, false, false, true, true},
		{"router disabled", StateFinalFailed, Hash("outbound.provider.not-configured", string(KindWeComTagCatalogMutation)), false, false, true, true},
		{"explicit rejection", StateFinalFailed, rejected, true, false, true, true},
		{"legacy rejection", StateFinalFailed, rejected, false, false, true, true},
		{"unknown", StateUnknown, rejected, false, false, true, false},
		{"reconciled unknown", StateReconciled, rejected, false, false, true, false},
		{"executed flag", StateFinalFailed, rejected, true, true, true, false},
		{"missing completion", StateFinalFailed, disabled, false, false, false, false},
		{"unclassified failure", StateFinalFailed, Hash("unclassified"), false, false, true, false},
		{"disabled but called", StateFinalFailed, disabled, true, false, true, false},
		{"retryable pre call", StateRetryable, Hash("precall"), false, false, true, true},
		{"retryable called", StateRetryable, Hash("precall"), true, false, true, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := safeTagCatalogRetryAttempt(7, "eer_7", Hash("envelope"), 1, tt.state, tt.receipt, tt.called, tt.executed, tt.completed); got != tt.want {
				t.Fatalf("safe=%v want=%v", got, tt.want)
			}
		})
	}
}

func TestPostgreSQLTagCatalogSafeRetryPreservesEffectAndTransaction(t *testing.T) {
	pool, cleanup := effectIntegrationPool(t)
	defer cleanup()
	_, file, _, _ := runtime.Caller(0)
	migration, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "migrations", "0106_tag_catalog_mutation_effect.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(context.Background(), string(migration)); err != nil {
		t.Fatal(err)
	}
	workers := river.NewWorkers()
	if err := river.AddWorkerSafely[EffectJobArgs](workers, NewWorker(nil, nil)); err != nil {
		t.Fatal(err)
	}
	client, err := platformjobqueue.NewInsertClient(pool, workers)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := NewRepository(pool, client)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	envelope := envelopeForTest()
	envelope.Kind = KindWeComTagCatalogMutation
	envelope.Owner = OwnerOutbound
	p, _, err := repo.AcceptAndQueue(ctx, AcceptCommand{ReceiptKey: Hash("tag-retry-accept"), Envelope: envelope})
	if err != nil {
		t.Fatal(err)
	}
	id, _ := parseEffectID(p.ID)
	var job int64
	if err = pool.QueryRow(ctx, `SELECT river_job_id FROM external_effect_jobs WHERE effect_id=$1`, id).Scan(&job); err != nil {
		t.Fatal(err)
	}
	if err = repo.RunAttempt(ctx, id, 1, job, nil); err != nil {
		t.Fatal(err)
	}
	c := port.TagCatalogMutationRetryCommand{EffectID: p.ID, SourceRefDigest: envelope.SourceRefDigest, ActorAdminUserID: 1, ReceiptKey: Hash("tag-retry-command")}
	if _, err = pool.Exec(ctx, `CREATE TABLE completion_sink_facts(effect_ref TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	repo.sink = integrationCompletionSink{fail: true}
	failedTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.RetryTagCatalogMutationWithin(platformpostgres.BindTransaction(ctx, failedTx), c); err == nil {
		t.Fatal("sink failure ignored")
	}
	failedTx.Rollback(ctx)
	var facts int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM completion_sink_facts`).Scan(&facts); err != nil || facts != 0 {
		t.Fatalf("sink rollback facts=%d err=%v", facts, err)
	}
	failedProjection, _ := repo.Get(ctx, p.ID)
	if failedProjection.State != StateFinalFailed || failedProjection.Generation != 1 {
		t.Fatalf("sink failure changed effect=%+v", failedProjection)
	}
	repo.sink = integrationCompletionSink{}
	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = blocker.Exec(ctx, `SELECT id FROM external_effects WHERE id=$1 FOR UPDATE`, id); err != nil {
		t.Fatal(err)
	}
	contender, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.RetryTagCatalogMutationWithin(platformpostgres.BindTransaction(ctx, contender), c); !errors.Is(err, port.ErrReconciliationConflict) {
		t.Fatalf("concurrent effect lock=%v", err)
	}
	contender.Rollback(ctx)
	blocker.Rollback(ctx)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got, err := repo.RetryTagCatalogMutationWithin(platformpostgres.BindTransaction(ctx, tx), c)
	if err != nil || got.ID != p.ID || got.Generation != 2 || got.State != StateQueued {
		t.Fatalf("retry=%+v %v", got, err)
	}
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	current, _ := repo.Get(ctx, p.ID)
	if current.State != StateFinalFailed || current.Generation != 1 {
		t.Fatalf("rollback=%+v", current)
	}
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got, err = repo.RetryTagCatalogMutationWithin(platformpostgres.BindTransaction(ctx, tx), c)
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	replay, err := repo.RetryTagCatalogMutationWithin(platformpostgres.BindTransaction(ctx, tx), c)
	if err != nil || replay.Generation != got.Generation {
		t.Fatalf("replay=%+v %v", replay, err)
	}
	wrong := c
	wrong.SourceRefDigest = Hash("another-owner-intent")
	if _, err = repo.RetryTagCatalogMutationWithin(platformpostgres.BindTransaction(ctx, tx), wrong); !errors.Is(err, port.ErrReconciliationConflict) {
		t.Fatalf("source error=%v", err)
	}
	var jobs, attempts int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM external_effect_jobs WHERE effect_id=$1`, id).Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM external_effect_attempts WHERE effect_id=$1`, id).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if jobs != 2 || attempts != 1 {
		t.Fatalf("jobs=%d attempts=%d", jobs, attempts)
	}
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	// A later final_failed projection must not erase an older unknown result.
	if _, err = pool.Exec(ctx, `UPDATE external_effect_attempts SET state='outcome_unknown' WHERE effect_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE external_effects SET state='final_failed' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	c.ReceiptKey = Hash("new-retry-after-unknown")
	if _, err = repo.RetryTagCatalogMutationWithin(platformpostgres.BindTransaction(ctx, tx), c); !errors.Is(err, port.ErrReconciliationConflict) {
		t.Fatalf("unknown history=%v", err)
	}
	if _, err = repo.RetryTagCatalogMutationWithin(ctx, c); err == nil {
		t.Fatal("missing transaction accepted")
	}

}
