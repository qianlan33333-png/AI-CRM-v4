package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	archiveapp "github.com/qianlan33333-png/AI-CRM-v3/internal/messagearchive/app"
	archivestore "github.com/qianlan33333-png/AI-CRM-v3/internal/messagearchive/store"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/idempotency"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/webhook"
	wecom "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

const archiveFinishFailureScope = "wecom-corp:wx-archive-finish-failure"

var errArchiveFinishFailureReadInjected = errors.New("archive reader failure injected for FinishRun test")

// TestMessageArchivePostgreSQLFinishRunFailureRecoversOnFencedInboxRetry
// injects a real PostgreSQL error from the archive sync-run UPDATE. It then
// exercises the existing Inbox retry path with the real archive owner store.
// The database helper provisions a random schema and drops it at test cleanup.
func TestMessageArchivePostgreSQLFinishRunFailureRecoversOnFencedInboxRetry(t *testing.T) {
	native, cleanup := channelWelcomeIntegrationPool(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	applyMessageArchiveJourneyMigrations(t, ctx, native)

	pool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}

	staffID := insertChannelWelcomeAdmin(t, ctx, native)
	fact, err := identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{
		Kind: identitydomain.KindWeComExternalUserID, Scope: archiveFinishFailureScope,
		Value: "wm_finish_failure_fixture", Source: "wecom.message_archive",
	})
	if err != nil {
		t.Fatal(err)
	}
	fixtureReader := &archiveJourneyReader{fact: fact}
	reader := &archiveFinishFailureReader{archiveJourneyReader: fixtureReader, failNext: true}
	store := &archiveFinishFailureStore{Store: archivestore.NewPostgreSQL()}
	inbox, err := webhook.NewService(webhook.NewPostgreSQLStore())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	archive := archiveapp.Service{
		Enabled: true, ReadEnabled: true, CorpScope: archiveFinishFailureScope,
		Reader: reader, Identity: archiveJourneyIdentity{}, Lineage: archiveJourneyLineage{},
		Staff: archiveJourneyStaff{id: staffID}, Store: store, UOW: uow,
		PageLimit: 100, PageBudget: 2, Now: func() time.Time { return now },
	}
	processor := wecom.ArchiveInboxProcessor{
		Enabled: true, Inbox: inbox, UOW: uow, Archive: archive,
		Now: func() time.Time { return now },
	}

	key, err := idempotency.Parse("wecom:message-archive:finish-failure-0001")
	if err != nil {
		t.Fatal(err)
	}
	notification, err := json.Marshal(map[string]string{
		"corp_id": "wx-archive-finish-failure", "event": "msgaudit_notify",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = uow.Within(ctx, func(tx context.Context) error {
		_, ingestErr := inbox.Ingest(tx, webhook.Ingest{
			Provider: "wecom.message_archive", IdempotencyKey: key,
			Payload: notification, MaxAttempts: 3,
		})
		return ingestErr
	}); err != nil {
		t.Fatalf("ingest archive notification: %v", err)
	}

	// The trigger fails the actual Store.FinishRun UPDATE, after StartRun has
	// committed. Since the schema is random and search_path-scoped, this cannot
	// affect another database schema or application table.
	if _, err = native.Exec(ctx, `CREATE FUNCTION fail_archive_run_failure_finish() RETURNS trigger
		LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.corp_scope = 'wecom-corp:wx-archive-finish-failure' AND NEW.status = 'failed' THEN
				RAISE EXCEPTION 'injected PostgreSQL FinishRun failure' USING ERRCODE = 'P0001';
			END IF;
			RETURN NEW;
		END
		$$`); err != nil {
		t.Fatalf("create isolated failure trigger function: %v", err)
	}
	if _, err = native.Exec(ctx, `CREATE TRIGGER fail_archive_run_failure_finish
		BEFORE UPDATE OF status ON message_archive_sync_runs
		FOR EACH ROW EXECUTE FUNCTION fail_archive_run_failure_finish()`); err != nil {
		t.Fatalf("create isolated failure trigger: %v", err)
	}

	processed, err := processor.ProcessOnce(ctx, "archive-finish-failure-first", 1)
	if err != nil || processed != 1 {
		t.Fatalf("first Inbox attempt processed=%d err=%v", processed, err)
	}
	if reader.failedReads != 1 || store.finishCalls != 1 || store.finishErr == nil || !strings.Contains(store.finishErr.Error(), "injected PostgreSQL FinishRun failure") {
		t.Fatalf("actual FinishRun failure not observed: failedReads=%d finishCalls=%d finishErr=%v", reader.failedReads, store.finishCalls, store.finishErr)
	}

	var runCount, runningCount, messages, participants int
	var cursor int64
	if err = native.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE status='running')
		FROM message_archive_sync_runs WHERE corp_scope=$1`, archiveFinishFailureScope).Scan(&runCount, &runningCount); err != nil || runCount != 1 || runningCount != 1 {
		t.Fatalf("after failed finalizer runs=%d running=%d err=%v", runCount, runningCount, err)
	}
	var inboxStatus, lastError string
	var attempts, maxAttempts int
	if err = native.QueryRow(ctx, `SELECT status,COALESCE(last_error_code,''),attempt_count,max_attempts
		FROM webhook_inbox WHERE provider='wecom.message_archive' AND idempotency_key=$1`, key).Scan(&inboxStatus, &lastError, &attempts, &maxAttempts); err != nil || inboxStatus != "retryable" || lastError != "archive_run_finalization_failed" || attempts != 1 || maxAttempts != 3 {
		t.Fatalf("Inbox after first attempt status=%q error=%q attempts=%d/%d err=%v", inboxStatus, lastError, attempts, maxAttempts, err)
	}
	if err = native.QueryRow(ctx, `SELECT last_seq FROM message_archive_sync_state WHERE corp_scope=$1`, archiveFinishFailureScope).Scan(&cursor); err != nil || cursor != 2 {
		t.Fatalf("partial failure lost committed cursor=%d err=%v", cursor, err)
	}
	if err = native.QueryRow(ctx, `SELECT (SELECT count(*) FROM message_archive_messages WHERE corp_scope=$1),
		(SELECT count(*) FROM message_archive_participants participant JOIN message_archive_messages message ON message.id=participant.message_id WHERE message.corp_scope=$1)`, archiveFinishFailureScope).Scan(&messages, &participants); err != nil || messages != 2 || participants != 4 {
		t.Fatalf("partial failure lost committed business rows messages=%d participants=%d err=%v", messages, participants, err)
	}

	if _, err = native.Exec(ctx, `DROP TRIGGER fail_archive_run_failure_finish ON message_archive_sync_runs`); err != nil {
		t.Fatalf("remove scoped failure trigger: %v", err)
	}
	if _, err = native.Exec(ctx, `DROP FUNCTION fail_archive_run_failure_finish()`); err != nil {
		t.Fatalf("remove scoped failure trigger function: %v", err)
	}

	processed, err = processor.ProcessOnce(ctx, "archive-finish-failure-retry", 1)
	if err != nil || processed != 1 {
		t.Fatalf("Inbox retry processed=%d err=%v", processed, err)
	}
	if reader.failedReads != 1 || len(fixtureReader.cursors) != 2 || fixtureReader.cursors[0] != 0 || fixtureReader.cursors[1] != 2 {
		t.Fatalf("retry did not resume from committed cursor: failedReads=%d cursors=%v", reader.failedReads, fixtureReader.cursors)
	}
	if err = native.QueryRow(ctx, `SELECT last_seq FROM message_archive_sync_state WHERE corp_scope=$1`, archiveFinishFailureScope).Scan(&cursor); err != nil || cursor != 2 {
		t.Fatalf("retried cursor=%d err=%v", cursor, err)
	}
	if err = native.QueryRow(ctx, `SELECT (SELECT count(*) FROM message_archive_messages WHERE corp_scope=$1),
		(SELECT count(*) FROM message_archive_participants participant JOIN message_archive_messages message ON message.id=participant.message_id WHERE message.corp_scope=$1)`, archiveFinishFailureScope).Scan(&messages, &participants); err != nil || messages != 2 || participants != 4 {
		t.Fatalf("retry persisted messages=%d participants=%d err=%v", messages, participants, err)
	}
	var succeededCount int
	if err = native.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE status='running'), count(*) FILTER (WHERE status='succeeded')
		FROM message_archive_sync_runs WHERE corp_scope=$1`, archiveFinishFailureScope).Scan(&runCount, &runningCount, &succeededCount); err != nil || runCount != 2 || runningCount != 0 || succeededCount != 1 {
		t.Fatalf("after retry runs=%d running=%d succeeded=%d err=%v", runCount, runningCount, succeededCount, err)
	}
	if err = native.QueryRow(ctx, `SELECT status,attempt_count,max_attempts FROM webhook_inbox WHERE provider='wecom.message_archive' AND idempotency_key=$1`, key).Scan(&inboxStatus, &attempts, &maxAttempts); err != nil || inboxStatus != "processed" || attempts != 2 || maxAttempts != 3 {
		t.Fatalf("Inbox after successful retry status=%q attempts=%d/%d err=%v", inboxStatus, attempts, maxAttempts, err)
	}

	var recoveredStatus, recoveredCode string
	var recoveredEnd, recoveredPages, recoveredInserted int
	if err = native.QueryRow(ctx, `SELECT status,error_code,end_seq,pages,inserted_count FROM message_archive_sync_runs WHERE corp_scope=$1 ORDER BY id LIMIT 1`, archiveFinishFailureScope).Scan(&recoveredStatus, &recoveredCode, &recoveredEnd, &recoveredPages, &recoveredInserted); err != nil || recoveredStatus != "failed" || recoveredCode != "previous_attempt_unfinished" || recoveredEnd != 2 || recoveredPages != 1 || recoveredInserted != 2 {
		t.Fatalf("old run recovery changed committed facts: status=%s code=%s end=%d pages=%d inserted=%d err=%v", recoveredStatus, recoveredCode, recoveredEnd, recoveredPages, recoveredInserted, err)
	}
	var replay bool
	if err = uow.Within(ctx, func(tx context.Context) error {
		result, ingestErr := inbox.Ingest(tx, webhook.Ingest{
			Provider: "wecom.message_archive", IdempotencyKey: key,
			Payload: notification, MaxAttempts: 3,
		})
		replay = result.Replay
		return ingestErr
	}); err != nil || !replay {
		t.Fatalf("processed Inbox delivery replay=%t err=%v", replay, err)
	}
	processed, err = processor.ProcessOnce(ctx, "archive-finish-failure-replay", 1)
	if err != nil || processed != 0 || len(fixtureReader.cursors) != 2 {
		t.Fatalf("completed notification replay processed=%d cursors=%v err=%v", processed, fixtureReader.cursors, err)
	}
}

type archiveFinishFailureReader struct {
	*archiveJourneyReader
	failNext    bool
	failedReads int
}

func (reader *archiveFinishFailureReader) GetChatData(ctx context.Context, cursor uint64, limit uint32) ([]wecomport.EncryptedArchiveRecord, error) {
	if reader.failNext && cursor > 0 {
		reader.failNext = false
		reader.failedReads++
		return nil, errArchiveFinishFailureReadInjected
	}
	return reader.archiveJourneyReader.GetChatData(ctx, cursor, limit)
}

type archiveFinishFailureStore struct {
	archiveapp.Store
	finishCalls int
	finishErr   error
}

func (store *archiveFinishFailureStore) FinishRun(ctx context.Context, id int64, finish archiveapp.SyncRunFinish) error {
	store.finishCalls++
	err := store.Store.FinishRun(ctx, id, finish)
	if err != nil {
		store.finishErr = err
	}
	return err
}

// A real Inbox lease expires while the old SDK read is still active. The new
// attempt can finish; the old attempt must never commit or overwrite it.
func TestMessageArchivePostgreSQLSupersededAttemptCannotCommit(t *testing.T) {
	native, cleanup := channelWelcomeIntegrationPool(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	applyMessageArchiveJourneyMigrations(t, ctx, native)
	pool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	staffID := insertChannelWelcomeAdmin(t, ctx, native)
	fact, err := identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{Kind: identitydomain.KindWeComExternalUserID, Scope: archiveFinishFailureScope, Value: "wm_fenced_attempt_fixture", Source: "wecom.message_archive"})
	if err != nil {
		t.Fatal(err)
	}
	oldReader := &archiveLeaseBarrierReader{archiveJourneyReader: &archiveJourneyReader{fact: fact}, started: make(chan struct{}), release: make(chan struct{})}
	defer oldReader.unblock()
	inbox, err := webhook.NewService(webhook.NewPostgreSQLStore())
	if err != nil {
		t.Fatal(err)
	}
	archive := archiveapp.Service{Enabled: true, ReadEnabled: true, CorpScope: archiveFinishFailureScope, Reader: oldReader, Identity: archiveJourneyIdentity{}, Lineage: archiveJourneyLineage{}, Staff: archiveJourneyStaff{id: staffID}, Store: archivestore.NewPostgreSQL(), UOW: uow, PageLimit: 100, PageBudget: 2}
	oldProcessor := wecom.ArchiveInboxProcessor{Enabled: true, Inbox: inbox, UOW: uow, Archive: archive}
	key, _ := idempotency.Parse("wecom:message-archive:fenced-attempt-0001")
	var deliveryID int64
	if err = uow.Within(ctx, func(tx context.Context) error {
		result, e := inbox.Ingest(tx, webhook.Ingest{Provider: "wecom.message_archive", IdempotencyKey: key, Payload: []byte(`{"corp_id":"wx-archive-finish-failure","event":"msgaudit_notify"}`), MaxAttempts: 3})
		deliveryID = result.Delivery.ID
		return e
	}); err != nil {
		t.Fatal(err)
	}
	oldDone := make(chan error, 1)
	go func() { _, e := oldProcessor.ProcessOnce(ctx, "archive-old-attempt", 1); oldDone <- e }()
	select {
	case <-oldReader.started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	// Controlled expiry of this isolated Inbox only; no sleeps or production
	// clock changes. The old reader is outside the database transaction.
	if _, err = native.Exec(ctx, `UPDATE webhook_inbox SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, deliveryID); err != nil {
		t.Fatal(err)
	}
	held := make(chan struct{})
	unlock := make(chan struct{})
	guarded := make(chan error, 1)
	go func() {
		guarded <- uow.Within(ctx, func(tx context.Context) error {
			if e := inbox.RequireCurrentClaim(tx, deliveryID, "wecom.message_archive", 1); e != nil {
				return e
			}
			close(held)
			select {
			case <-unlock:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	select {
	case <-held:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	var skipped int
	err = uow.Within(ctx, func(tx context.Context) error {
		rows, e := inbox.Claim(tx, webhook.Claim{Provider: "wecom.message_archive", Owner: "archive-competing-claim", Limit: 1, LeaseDuration: time.Minute})
		skipped = len(rows)
		return e
	})
	close(unlock)
	if guardErr := <-guarded; guardErr != nil {
		t.Fatal(guardErr)
	}
	if err != nil || skipped != 0 {
		t.Fatalf("claim advanced while page held Inbox lock: claimed=%d err=%v", skipped, err)
	}
	archive.Reader = &archiveJourneyReader{fact: fact}
	newer := wecom.ArchiveInboxProcessor{Enabled: true, Inbox: inbox, UOW: uow, Archive: archive}
	if count, e := newer.ProcessOnce(ctx, "archive-new-attempt", 1); e != nil || count != 1 {
		t.Fatalf("new attempt count=%d err=%v", count, e)
	}
	oldReader.unblock()
	select {
	case e := <-oldDone:
		if !errors.Is(e, webhook.ErrConcurrentUpdate) {
			t.Fatalf("old attempt did not reject lost claim: %v", e)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	var status string
	var attempt, messages, runs, failed, succeeded int
	var cursor int64
	if err = native.QueryRow(ctx, `SELECT status,attempt_count FROM webhook_inbox WHERE id=$1`, deliveryID).Scan(&status, &attempt); err != nil || status != "processed" || attempt != 2 {
		t.Fatalf("new Inbox overwritten status=%s attempt=%d err=%v", status, attempt, err)
	}
	if err = native.QueryRow(ctx, `SELECT (SELECT last_seq FROM message_archive_sync_state WHERE corp_scope=$1),(SELECT count(*) FROM message_archive_messages WHERE corp_scope=$1),count(*),count(*) FILTER(WHERE status='failed' AND error_code='previous_attempt_unfinished'),count(*) FILTER(WHERE status='succeeded') FROM message_archive_sync_runs WHERE corp_scope=$1`, archiveFinishFailureScope).Scan(&cursor, &messages, &runs, &failed, &succeeded); err != nil || cursor != 2 || messages != 2 || runs != 2 || failed != 1 || succeeded != 1 {
		t.Fatalf("stale attempt changed facts cursor=%d messages=%d runs=%d failed=%d succeeded=%d err=%v", cursor, messages, runs, failed, succeeded, err)
	}
}

type archiveLeaseBarrierReader struct {
	*archiveJourneyReader
	started, release chan struct{}
	releaseOnce      sync.Once
}

func (r *archiveLeaseBarrierReader) unblock() { r.releaseOnce.Do(func() { close(r.release) }) }
func (r *archiveLeaseBarrierReader) GetChatData(ctx context.Context, cursor uint64, limit uint32) ([]wecomport.EncryptedArchiveRecord, error) {
	close(r.started)
	select {
	case <-r.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return r.archiveJourneyReader.GetChatData(ctx, cursor, limit)
}

func TestMessageArchivePostgreSQLBudgetContinuationSurvivesFinishFailure(t *testing.T) {
	native, cleanup := channelWelcomeIntegrationPool(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	applyMessageArchiveJourneyMigrations(t, ctx, native)
	pool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	staffID := insertChannelWelcomeAdmin(t, ctx, native)
	fact, err := identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{Kind: identitydomain.KindWeComExternalUserID, Scope: archiveFinishFailureScope, Value: "wm_budget_finish_fixture", Source: "wecom.message_archive"})
	if err != nil {
		t.Fatal(err)
	}
	reader := &archiveJourneyReader{fact: fact}
	inbox, err := webhook.NewService(webhook.NewPostgreSQLStore())
	if err != nil {
		t.Fatal(err)
	}
	archive := archiveapp.Service{Enabled: true, CorpScope: archiveFinishFailureScope, Reader: reader, Identity: archiveJourneyIdentity{}, Lineage: archiveJourneyLineage{}, Staff: archiveJourneyStaff{id: staffID}, Store: archivestore.NewPostgreSQL(), UOW: uow, PageLimit: 100, PageBudget: 1}
	processor := wecom.ArchiveInboxProcessor{Enabled: true, Inbox: inbox, UOW: uow, Archive: archive}
	key, _ := idempotency.Parse("wecom:message-archive:budget-finish-failure-0001")
	var deliveryID int64
	if err = uow.Within(ctx, func(tx context.Context) error {
		result, e := inbox.Ingest(tx, webhook.Ingest{Provider: "wecom.message_archive", IdempotencyKey: key, Payload: []byte(`{"corp_id":"wx-archive-finish-failure","event":"msgaudit_notify"}`), MaxAttempts: 1})
		deliveryID = result.Delivery.ID
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, `CREATE FUNCTION fail_archive_budget_finish() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.status='failed' THEN RAISE EXCEPTION 'budget finalizer injection' USING ERRCODE='P0001'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER fail_archive_budget_finish BEFORE UPDATE OF status ON message_archive_sync_runs FOR EACH ROW EXECUTE FUNCTION fail_archive_budget_finish()`); err != nil {
		t.Fatal(err)
	}
	if count, e := processor.ProcessOnce(ctx, "archive-budget-first", 1); e != nil || count != 1 {
		t.Fatalf("first count=%d err=%v", count, e)
	}
	var status, code string
	var attempt, maxAttempts int
	if err = native.QueryRow(ctx, `SELECT status,last_error_code,attempt_count,max_attempts FROM webhook_inbox WHERE id=$1`, deliveryID).Scan(&status, &code, &attempt, &maxAttempts); err != nil || status != "retryable" || code != "archive_run_finalization_failed" || attempt != 1 || maxAttempts != 2 {
		t.Fatalf("committed progress lost continuation: status=%s code=%s attempt=%d/%d err=%v", status, code, attempt, maxAttempts, err)
	}
	if _, err = native.Exec(ctx, `DROP TRIGGER fail_archive_budget_finish ON message_archive_sync_runs; DROP FUNCTION fail_archive_budget_finish()`); err != nil {
		t.Fatal(err)
	}
	if count, e := processor.ProcessOnce(ctx, "archive-budget-next", 1); e != nil || count != 1 {
		t.Fatalf("continuation count=%d err=%v", count, e)
	}
	var cursor int64
	var messages, running int
	if err = native.QueryRow(ctx, `SELECT (SELECT last_seq FROM message_archive_sync_state WHERE corp_scope=$1),(SELECT count(*) FROM message_archive_messages WHERE corp_scope=$1),(SELECT count(*) FROM message_archive_sync_runs WHERE corp_scope=$1 AND status='running'),status,attempt_count FROM webhook_inbox WHERE id=$2`, archiveFinishFailureScope, deliveryID).Scan(&cursor, &messages, &running, &status, &attempt); err != nil || cursor != 2 || messages != 2 || running != 0 || status != "processed" || attempt != 2 {
		t.Fatalf("continuation did not preserve page: cursor=%d messages=%d running=%d status=%s attempt=%d err=%v", cursor, messages, running, status, attempt, err)
	}
	if len(reader.cursors) != 2 || reader.cursors[0] != 0 || reader.cursors[1] != 2 {
		t.Fatalf("continuation restarted committed page: %v", reader.cursors)
	}
}
