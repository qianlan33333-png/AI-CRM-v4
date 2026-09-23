package main

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/outbound"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformjobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	tagapp "github.com/qianlan33333-png/AI-CRM-v3/internal/tag/app"
	tagdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/tag/domain"
	tagport "github.com/qianlan33333-png/AI-CRM-v3/internal/tag/port"
	tagstore "github.com/qianlan33333-png/AI-CRM-v3/internal/tag/store"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
	"github.com/riverqueue/river"
)

type transactionRecordingTagReader struct {
	reader tagport.CatalogMutationDispatchReader
	tx     pgx.Tx
}

func (r *transactionRecordingTagReader) ReadCatalogMutationDispatch(ctx context.Context, source string) (tagport.CatalogMutationDispatch, error) {
	var err error
	r.tx, err = platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return tagport.CatalogMutationDispatch{}, err
	}
	return r.reader.ReadCatalogMutationDispatch(ctx, source)
}

type outsideTransactionTagWriter struct {
	t      *testing.T
	reader *transactionRecordingTagReader
	calls  int
}

func (w *outsideTransactionTagWriter) MutateTagCatalog(ctx context.Context, input wecomport.TagCatalogMutation) (wecomport.TagCatalogMutationResult, error) {
	w.t.Helper()
	if _, err := platformpostgres.RequireTransaction(ctx); err == nil {
		w.t.Fatal("provider inherited database transaction")
	}
	if w.reader.tx == nil {
		w.t.Fatal("snapshot was not read in a transaction")
	}
	if _, err := w.reader.tx.Exec(ctx, "SELECT 1"); !errors.Is(err, pgx.ErrTxClosed) {
		w.t.Fatalf("snapshot transaction remained open: %v", err)
	}
	w.calls++
	return wecomport.TagCatalogMutationResult{ProviderGroupID: "provider-group-test", ProviderTagID: "provider-tag-test"}, nil
}

type dispatchCatalogReader struct{}

func (dispatchCatalogReader) ListCatalog(context.Context) (outbound.CatalogSnapshot, error) {
	return outbound.CatalogSnapshot{}, nil
}

func TestTagCatalogDispatchPostgreSQLRecoveryAndProviderOutsideTransaction(t *testing.T) {
	url, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL not configured")
	}
	ctx := context.Background()
	pool, cleanup := customerTagRuntimePool(t, ctx, url)
	defer cleanup()
	native := pool.Native()
	migration, err := os.ReadFile("../../migrations/0106_tag_catalog_mutation_effect.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, `INSERT INTO admin_users(id,username,password_hash,display_name,is_active) OVERRIDING SYSTEM VALUE VALUES(7,'dispatch-test','$argon2id$test','Test',true)`); err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	workers := river.NewWorkers()
	river.AddWorker(workers, externaleffects.NewWorker(nil, nil))
	client, err := platformjobqueue.NewInsertClient(native, workers)
	if err != nil {
		t.Fatal(err)
	}
	effects, err := externaleffects.NewRepository(native, client)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := tagstore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	sink, err := outbound.NewTagCatalogMutationCompletionSink(repo)
	if err != nil {
		t.Fatal(err)
	}
	if err = effects.SetCompletionSink(sink); err != nil {
		t.Fatal(err)
	}
	accepter, err := outbound.NewTagCatalogMutationAccepter(effects)
	if err != nil {
		t.Fatal(err)
	}
	service := tagapp.NewService(uow, repo, repo, repo, repo)
	if err = service.BindProviderMutations(accepter); err != nil {
		t.Fatal(err)
	}
	if err = service.BindMutationRecovery(effects); err != nil {
		t.Fatal(err)
	}
	group, tag, err := service.CreateGroup(ctx, tagdomain.Command{Actor: 7, IdempotencyKey: "dispatch-uow-create", GroupName: "Test group", FirstTagName: "Test tag"})
	if err != nil {
		t.Fatal(err)
	}
	var effectID, jobID, mutationID int64
	if err = native.QueryRow(ctx, `SELECT e.id,j.river_job_id,m.id FROM external_effects e JOIN external_effect_jobs j ON j.effect_id=e.id JOIN tag_catalog_mutation_receipts m ON m.effect_ref='eer_'||e.id::text`).Scan(&effectID, &jobID, &mutationID); err != nil {
		t.Fatal(err)
	}
	recording := &transactionRecordingTagReader{reader: repo}
	writer := &outsideTransactionTagWriter{t: t, reader: recording}
	broken, err := outbound.NewTagCatalogMutationProvider(repo, writer, dispatchCatalogReader{})
	if err != nil {
		t.Fatal(err)
	}
	// Reproduce the released wiring failure: no provider call and a durable
	// dispatch_changed receipt, then recover the same effect through public Ports.
	if err = effects.RunAttempt(ctx, effectID, 1, jobID, broken); err != nil {
		t.Fatal(err)
	}
	if writer.calls != 0 {
		t.Fatal("broken wiring reached provider")
	}
	_, err = service.RetryMutation(ctx, mutationID, tagdomain.Command{Actor: 7, IdempotencyKey: "dispatch-uow-recover"})
	if err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT river_job_id FROM external_effect_jobs WHERE effect_id=$1 AND generation=2`, effectID).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	fixed, err := outbound.NewTagCatalogMutationProvider(tagCatalogDispatchReader{uow: uow, reader: recording}, writer, dispatchCatalogReader{})
	if err != nil {
		t.Fatal(err)
	}
	if err = effects.RunAttempt(ctx, effectID, 2, jobID, fixed); err != nil {
		t.Fatal(err)
	}
	if writer.calls != 1 {
		t.Fatalf("provider calls=%d", writer.calls)
	}
	var state, providerTag string
	var groups, tags, effectCount int
	if err = native.QueryRow(ctx, `SELECT state FROM external_effects WHERE id=$1`, effectID).Scan(&state); err != nil || state != "executed" {
		t.Fatalf("state=%s err=%v", state, err)
	}
	if err = native.QueryRow(ctx, `SELECT provider_tag_id FROM tag_provider_tag_bindings WHERE tag_id=$1`, tag.ID).Scan(&providerTag); err != nil || providerTag != "provider-tag-test" {
		t.Fatalf("provider binding=%s err=%v", providerTag, err)
	}
	if err = native.QueryRow(ctx, `SELECT (SELECT count(*) FROM tag_groups),(SELECT count(*) FROM tag_catalog_tags),(SELECT count(*) FROM external_effects)`).Scan(&groups, &tags, &effectCount); err != nil || groups != 1 || tags != 1 || effectCount != 1 || group.ID < 1 {
		t.Fatalf("duplicate recovery groups=%d tags=%d effects=%d err=%v", groups, tags, effectCount, err)
	}
}
