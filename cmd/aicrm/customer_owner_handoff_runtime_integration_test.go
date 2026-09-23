package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	accessstore "github.com/qianlan33333-png/AI-CRM-v3/internal/access/store"
	customer "github.com/qianlan33333-png/AI-CRM-v3/internal/customer"
	customerapp "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/app"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	externaleffects "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/outbound"
	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	platformjobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	platformoutbox "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/outbox"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
	"github.com/riverqueue/river"
)

type ownerHandoffRuntimeResolver struct {
	candidate  customerport.OwnerHandoffCandidate
	candidates []customerport.OwnerHandoffCandidate
}

func (resolver ownerHandoffRuntimeResolver) ResolveOwnerHandoffCandidates(_ context.Context, _ customerport.OwnerHandoffMode, _, _ int64, _ string, ids []customerdomain.CustomerID) ([]customerport.OwnerHandoffCandidate, error) {
	candidates := resolver.candidates
	if candidates == nil {
		candidates = []customerport.OwnerHandoffCandidate{resolver.candidate}
	}
	if len(ids) != len(candidates) {
		return nil, customer.ErrOwnerHandoffConflict
	}
	for index := range ids {
		if ids[index] != candidates[index].CustomerID {
			return nil, customer.ErrOwnerHandoffConflict
		}
	}
	return append([]customerport.OwnerHandoffCandidate(nil), candidates...), nil
}

// interruptAfterOwnerHandoffSegment makes the restart boundary explicit. River's
// graceful client stop drains workers, but does not cancel a worker context that
// was started with Runtime.Run. A later segment therefore cannot wait on ctx.Done
// to leave the first runtime: the test releases it, and the durable job snoozes
// for the fresh runtime without invoking the Customer service.
type interruptAfterOwnerHandoffSegment struct {
	service              *customerapp.OwnerHandoffService
	segmentZeroCommitted chan<- struct{}
	laterSegmentClaimed  chan<- struct{}
	laterSegmentReleased chan<- struct{}
	interrupt            <-chan struct{}
}

func (worker interruptAfterOwnerHandoffSegment) ProcessOwnerHandoffBatch(ctx context.Context, batchID string, segment int64) error {
	if segment > 0 {
		if worker.laterSegmentClaimed != nil {
			select {
			case worker.laterSegmentClaimed <- struct{}{}:
			default:
			}
		}
		// A short snooze preserves the claimed River job for the replacement
		// runtime. It is intentionally independent from ctx: Runtime.Run starts
		// River with context.WithoutCancel and graceful Stop waits for Work.
		<-worker.interrupt
		if worker.laterSegmentReleased != nil {
			select {
			case worker.laterSegmentReleased <- struct{}{}:
			default:
			}
		}
		return river.JobSnooze(2 * time.Second)
	}
	err := worker.service.ProcessOwnerHandoffBatch(ctx, batchID, segment)
	if err == nil {
		// ProcessOwnerHandoffBatch returns only after the Customer UoW has
		// committed the local rows and the following durable job. Tell the
		// interruption fixture that exact boundary has occurred; it then waits
		// for any concurrently claimed successor before stopping the runtime.
		if worker.segmentZeroCommitted != nil {
			worker.segmentZeroCommitted <- struct{}{}
		}
	}
	return err
}

type ownerHandoffRuntimeWriter struct {
	mu    sync.Mutex
	calls []struct {
		source, target, welcome string
		external                []string
	}
}

func (writer *ownerHandoffRuntimeWriter) TransferCustomer(_ context.Context, source, target string, external []string, welcome string) (wecomport.CustomerTransferResult, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if len(external) == 0 || len(external) > 100 {
		return wecomport.CustomerTransferResult{}, fmt.Errorf("unexpected transfer target count")
	}
	writer.calls = append(writer.calls, struct {
		source, target, welcome string
		external                []string
	}{source: source, target: target, welcome: welcome, external: append([]string(nil), external...)})
	return wecomport.CustomerTransferResult{AcceptedExternalUserIDs: append([]string(nil), external...)}, nil
}

func (*ownerHandoffRuntimeWriter) TransferResult(context.Context, string, string, string) (wecomport.CustomerTransferResult, error) {
	return wecomport.CustomerTransferResult{}, nil
}

// TestCustomerOwnerHandoffRiverExecutesFrozenTransferThenLocalCAS covers the
// actual bounded path: 101 eligible rows freeze into two EER intents (100+1),
// River executes both bounded outbound calls after their Customer UoWs commit,
// and completion atomically projects only accepted frozen targets locally.
func TestCustomerOwnerHandoffRiverExecutesFrozenTransferThenLocalCAS(t *testing.T) {
	native, cleanup := channelWelcomeIntegrationPool(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate owner-handoff runtime migration")
	}
	migration, err := os.ReadFile(filepath.Join(filepath.Dir(source), "..", "..", "migrations", "0092_customer_owner_handoff.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}
	pool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	workers := river.NewWorkers()
	if err = river.AddWorkerSafely[externaleffects.EffectJobArgs](workers, externaleffects.NewWorker(nil, nil)); err != nil {
		t.Fatal(err)
	}
	if err = river.AddWorkerSafely[customer.OwnerHandoffBatchJobArgs](workers, customer.NewOwnerHandoffBatchWorker()); err != nil {
		t.Fatal(err)
	}
	insert, err := platformjobqueue.NewInsertClient(native, workers)
	if err != nil {
		t.Fatal(err)
	}
	effects, err := externaleffects.NewRepository(native, insert)
	if err != nil {
		t.Fatal(err)
	}
	batchEnqueuer, err := customer.NewRiverOwnerHandoffEnqueuer(insert)
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := customer.NewOwnerHandoffCipher("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	if err != nil {
		t.Fatal(err)
	}
	store := customer.NewPostgreSQLOwnerHandoffStoreWithCipher(cipher)
	staff := accessstore.NewPostgreSQL()
	var sourceID, targetID int64
	const handoffRows = 101
	ids := make([]customerdomain.CustomerID, 0, handoffRows)
	candidates := make([]customerport.OwnerHandoffCandidate, 0, handoffRows)
	if err = uow.Within(ctx, func(txctx context.Context) error {
		tx, txErr := platformpostgres.RequireTransaction(txctx)
		if txErr != nil {
			return txErr
		}
		if txErr = tx.QueryRow(txctx, `INSERT INTO admin_users(username,password_hash,display_name,wecom_userid,is_active,login_enabled) VALUES('handoff-runtime-source','$argon2id$fixture','Former','former-user',false,false) RETURNING id`).Scan(&sourceID); txErr != nil {
			return txErr
		}
		if txErr = tx.QueryRow(txctx, `INSERT INTO admin_users(username,password_hash,display_name,wecom_userid,is_active,login_enabled) VALUES('handoff-runtime-target','$argon2id$fixture','Next','next-user',true,false) RETURNING id`).Scan(&targetID); txErr != nil {
			return txErr
		}
		for index := 0; index < handoffRows; index++ {
			var customerID customerdomain.CustomerID
			if txErr = tx.QueryRow(txctx, `INSERT INTO customers(status) VALUES('active') RETURNING id`).Scan(&customerID); txErr != nil {
				return txErr
			}
			ids = append(ids, customerID)
			candidates = append(candidates, customerport.OwnerHandoffCandidate{CustomerID: customerID, State: "ready", RelationshipDigest: sha256.Sum256([]byte(fmt.Sprintf("runtime-trusted-relation-%d", index))), SourceUserID: "former-user", TargetUserID: "next-user", ExternalUserID: fmt.Sprintf("external-runtime-%03d", index)})
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	audit, err := platformaudit.NewService(platformaudit.NewPostgreSQLStore())
	if err != nil {
		t.Fatal(err)
	}
	service, err := customerapp.NewOwnerHandoffService(uow, store, staff, ownerHandoffRuntimeResolver{candidates: candidates}, audit, platformoutbox.NewPostgreSQL())
	if err != nil {
		t.Fatal(err)
	}
	if err = service.SetExternalEffectAccepter(effects); err != nil {
		t.Fatal(err)
	}
	if err = service.SetBatchEnqueuer(batchEnqueuer); err != nil {
		t.Fatal(err)
	}
	service.SetWeComProviderEnabled(true)
	preview, err := service.PreviewOwnerHandoff(ctx, customerport.OwnerHandoffPreviewCommand{ActorAdminUserID: sourceID, Mode: customerport.OwnerHandoffWeComThenCRM, SourceStaffID: sourceID, TargetStaffID: targetID, CorpScope: "wecom-corp:runtime", CustomerIDs: ids, WelcomeMessage: "欢迎", ConfirmationPhrase: "CONFIRM", IdempotencyKey: "runtime-preview-key"})
	if err != nil {
		t.Fatal(err)
	}
	// A second independently generated preview sees the same still-current
	// relation. Confirm both concurrently: the Customer row lock must allow
	// exactly one EER acceptance, rather than issue transfer_customer twice.
	secondPreview, err := service.PreviewOwnerHandoff(ctx, customerport.OwnerHandoffPreviewCommand{ActorAdminUserID: sourceID, Mode: customerport.OwnerHandoffWeComThenCRM, SourceStaffID: sourceID, TargetStaffID: targetID, CorpScope: "wecom-corp:runtime", CustomerIDs: ids, WelcomeMessage: "欢迎", ConfirmationPhrase: "CONFIRM", IdempotencyKey: "runtime-preview-key-second"})
	if err != nil {
		t.Fatal(err)
	}
	type confirmation struct {
		batch customerport.OwnerHandoffBatch
		err   error
	}
	start := make(chan struct{})
	results := make(chan confirmation, 2)
	for _, input := range []struct {
		preview customerport.OwnerHandoffPreview
		key     string
	}{{preview: preview, key: "runtime-confirm-key-first"}, {preview: secondPreview, key: "runtime-confirm-key-second"}} {
		input := input
		go func() {
			<-start
			batch, confirmErr := service.ConfirmOwnerHandoff(ctx, customerport.OwnerHandoffConfirmCommand{ActorAdminUserID: sourceID, PreviewID: input.preview.ID, PreviewHash: input.preview.Hash, ConfirmationPhrase: "CONFIRM", IdempotencyKey: input.key})
			results <- confirmation{batch: batch, err: confirmErr}
		}()
	}
	close(start)
	var batch customerport.OwnerHandoffBatch
	accepted, conflicts := 0, 0
	for range 2 {
		result := <-results
		if result.err == nil {
			accepted++
			batch = result.batch
			continue
		}
		if errors.Is(result.err, customer.ErrOwnerHandoffConflict) {
			conflicts++
			continue
		}
		t.Fatalf("concurrent confirmation: %v", result.err)
	}
	if accepted != 1 || conflicts != 1 || len(batch.Lines) != handoffRows || batch.Lines[0].State != "queued" || batch.Lines[0].EffectID != "" {
		t.Fatalf("accepted=%d conflicts=%d batch=%+v", accepted, conflicts, batch)
	}
	var acceptedEffects int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM external_effects WHERE kind='customer_owner_handoff'`).Scan(&acceptedEffects); err != nil || acceptedEffects != 0 {
		t.Fatalf("handoff effects before batch worker=%d err=%v", acceptedEffects, err)
	}
	writer := &ownerHandoffRuntimeWriter{}
	provider, err := outbound.NewCustomerOwnerHandoffProvider(customerOwnerHandoffExecutionAdapter{uow: uow, executions: store, staff: staff}, writer)
	if err != nil {
		t.Fatal(err)
	}
	workers = river.NewWorkers()
	if err = river.AddWorkerSafely[externaleffects.EffectJobArgs](workers, externaleffects.NewWorker(effects, provider)); err != nil {
		t.Fatal(err)
	}
	runtimeBatchWorker := customer.NewOwnerHandoffBatchWorker()
	if err = runtimeBatchWorker.Bind(service); err != nil {
		t.Fatal(err)
	}
	if err = river.AddWorkerSafely[customer.OwnerHandoffBatchJobArgs](workers, runtimeBatchWorker); err != nil {
		t.Fatal(err)
	}
	completion, err := outbound.NewCustomerOwnerHandoffCompletionSink(store)
	if err != nil {
		t.Fatal(err)
	}
	if err = effects.SetCompletionSink(completion); err != nil {
		t.Fatal(err)
	}
	// Commit the first Customer segment in an isolated runtime, then make the
	// next job claim observable before stopping. River stops gracefully, so the
	// fixture explicitly releases the claimed successor as a durable snooze;
	// waiting for its worker context would instead block client.Stop.
	firstSegmentCommitted := make(chan struct{}, 1)
	firstLaterSegmentClaimed := make(chan struct{}, 1)
	firstLaterSegmentReleased := make(chan struct{}, 1)
	firstInterrupt := make(chan struct{})
	firstBatchWorker := customer.NewOwnerHandoffBatchWorker()
	if err = firstBatchWorker.Bind(interruptAfterOwnerHandoffSegment{service: service, segmentZeroCommitted: firstSegmentCommitted, laterSegmentClaimed: firstLaterSegmentClaimed, laterSegmentReleased: firstLaterSegmentReleased, interrupt: firstInterrupt}); err != nil {
		t.Fatal(err)
	}
	firstWorkers := river.NewWorkers()
	if err = river.AddWorkerSafely[customer.OwnerHandoffBatchJobArgs](firstWorkers, firstBatchWorker); err != nil {
		t.Fatal(err)
	}
	firstRuntime, err := platformjobqueue.NewRuntime(native, firstWorkers, customer.OwnerHandoffQueue)
	if err != nil {
		t.Fatal(err)
	}
	firstCtx, cancelFirst := context.WithCancel(ctx)
	firstDone := make(chan error, 1)
	go func() { firstDone <- firstRuntime.Run(firstCtx) }()
	var releaseFirstRuntimeOnce sync.Once
	releaseFirstRuntime := func() {
		releaseFirstRuntimeOnce.Do(func() {
			cancelFirst()
			close(firstInterrupt)
		})
	}
	defer releaseFirstRuntime()
	select {
	case <-firstSegmentCommitted:
	case <-time.After(5 * time.Second):
		releaseFirstRuntime()
		select {
		case <-firstDone:
		case <-time.After(5 * time.Second):
			t.Fatal("first owner handoff runtime did not stop while cleaning up an uncommitted segment")
		}
		t.Fatal("first owner handoff segment did not commit")
	}
	select {
	case <-firstLaterSegmentClaimed:
	case <-time.After(5 * time.Second):
		releaseFirstRuntime()
		select {
		case <-firstDone:
		case <-time.After(5 * time.Second):
			t.Fatal("first owner handoff runtime did not stop while cleaning up an unclaimed successor")
		}
		t.Fatal("first owner handoff successor was not claimed")
	}
	releaseFirstRuntime()
	select {
	case <-firstLaterSegmentReleased:
	case <-time.After(5 * time.Second):
		t.Fatal("first owner handoff successor did not leave the interruption gate")
	}
	select {
	case runErr := <-firstDone:
		if runErr != nil && runErr != context.Canceled {
			t.Fatalf("first owner handoff runtime stop: %v", runErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first owner handoff runtime did not stop after its successor was released")
	}
	var firstEffects int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM external_effects WHERE kind='customer_owner_handoff'`).Scan(&firstEffects); err != nil || firstEffects != 1 {
		t.Fatalf("first frozen effect=%d err=%v", firstEffects, err)
	}
	writer.mu.Lock()
	if len(writer.calls) != 0 {
		writer.mu.Unlock()
		t.Fatalf("provider called before restart: %+v", writer.calls)
	}
	writer.mu.Unlock()
	runtimeService, err := platformjobqueue.NewRuntime(native, workers, platformjobqueue.OutboundQueue, customer.OwnerHandoffQueue)
	if err != nil {
		t.Fatal(err)
	}
	runCtx, stopRun := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- runtimeService.Run(runCtx) }()
	defer func() {
		stopRun()
		select {
		case runErr := <-done:
			if runErr != nil && runErr != context.Canceled {
				t.Errorf("runtime stop: %v", runErr)
			}
		case <-time.After(5 * time.Second):
			t.Error("owner-handoff runtime did not stop")
		}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var acceptedLines, acceptedEffects, localOwners int
		err = native.QueryRow(ctx, `SELECT
			(SELECT count(*) FROM customer_owner_handoff_lines WHERE batch_id=$1 AND state='provider_accepted'),
			(SELECT count(*) FROM external_effects WHERE kind='customer_owner_handoff' AND state='executed'),
			(SELECT count(*) FROM customer_local_owners WHERE staff_id=$2 AND source='owner_handoff_wecom_then_crm')`, batch.ID, targetID).Scan(&acceptedLines, &acceptedEffects, &localOwners)
		if err == nil && acceptedLines == handoffRows && acceptedEffects == 2 && localOwners == handoffRows {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	var acceptedLines, executedEffects, localOwners int
	if err = native.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM customer_owner_handoff_lines WHERE batch_id=$1 AND state='provider_accepted'),
		(SELECT count(*) FROM external_effects WHERE kind='customer_owner_handoff' AND state='executed'),
		(SELECT count(*) FROM customer_local_owners WHERE staff_id=$2 AND source='owner_handoff_wecom_then_crm')`, batch.ID, targetID).Scan(&acceptedLines, &executedEffects, &localOwners); err != nil || acceptedLines != handoffRows || executedEffects != 2 || localOwners != handoffRows {
		t.Fatalf("completion lines=%d effects=%d owners=%d err=%v", acceptedLines, executedEffects, localOwners, err)
	}
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if len(writer.calls) != 2 || len(writer.calls[0].external) != 100 || len(writer.calls[1].external) != 1 || writer.calls[0].source != "former-user" || writer.calls[0].target != "next-user" || writer.calls[1].source != "former-user" || writer.calls[1].target != "next-user" || writer.calls[0].welcome != "欢迎" || writer.calls[1].welcome != "欢迎" || writer.calls[0].external[0] != "external-runtime-000" || writer.calls[0].external[99] != "external-runtime-099" || writer.calls[1].external[0] != "external-runtime-100" {
		t.Fatalf("provider calls=%+v", writer.calls)
	}
}

var _ wecomport.CustomerTransferWriter = (*ownerHandoffRuntimeWriter)(nil)
var _ pgx.Tx

// TestCustomerOwnerHandoffRiverSegmentsLocalOnly20000 verifies the approved 20k
// maximum with the actual River runtime. local_only still has no
// provider/EER write, but each segment commits its local CAS/audit/outbox facts
// before it atomically creates the following River job.
func TestCustomerOwnerHandoffRiverSegmentsLocalOnly20000(t *testing.T) {
	native, cleanup := channelWelcomeIntegrationPool(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate owner-handoff runtime migration")
	}
	migration, err := os.ReadFile(filepath.Join(filepath.Dir(source), "..", "..", "migrations", "0092_customer_owner_handoff.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}
	pool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	workers := river.NewWorkers()
	if err = river.AddWorkerSafely[customer.OwnerHandoffBatchJobArgs](workers, customer.NewOwnerHandoffBatchWorker()); err != nil {
		t.Fatal(err)
	}
	insert, err := platformjobqueue.NewInsertClient(native, workers)
	if err != nil {
		t.Fatal(err)
	}
	enqueuer, err := customer.NewRiverOwnerHandoffEnqueuer(insert)
	if err != nil {
		t.Fatal(err)
	}
	store := customer.NewPostgreSQLOwnerHandoffStore()
	staff := accessstore.NewPostgreSQL()
	var sourceID, targetID int64
	const handoffRows = 20000
	ids := make([]customerdomain.CustomerID, 0, handoffRows)
	candidates := make([]customerport.OwnerHandoffCandidate, 0, handoffRows)
	if err = uow.Within(ctx, func(txctx context.Context) error {
		tx, txErr := platformpostgres.RequireTransaction(txctx)
		if txErr != nil {
			return txErr
		}
		if txErr = tx.QueryRow(txctx, `INSERT INTO admin_users(username,password_hash,display_name,wecom_userid,is_active,login_enabled) VALUES('handoff-segment-source','$argon2id$fixture','Former','segment-former',false,false) RETURNING id`).Scan(&sourceID); txErr != nil {
			return txErr
		}
		if txErr = tx.QueryRow(txctx, `INSERT INTO admin_users(username,password_hash,display_name,wecom_userid,is_active,login_enabled) VALUES('handoff-segment-target','$argon2id$fixture','Next','segment-next',true,false) RETURNING id`).Scan(&targetID); txErr != nil {
			return txErr
		}
		for index := 0; index < handoffRows; index++ {
			var customerID customerdomain.CustomerID
			if txErr = tx.QueryRow(txctx, `INSERT INTO customers(status) VALUES('active') RETURNING id`).Scan(&customerID); txErr != nil {
				return txErr
			}
			ids = append(ids, customerID)
			candidates = append(candidates, customerport.OwnerHandoffCandidate{CustomerID: customerID, State: "ready", RelationshipDigest: sha256.Sum256([]byte(fmt.Sprintf("segment-%d", index)))})
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	audit, err := platformaudit.NewService(platformaudit.NewPostgreSQLStore())
	if err != nil {
		t.Fatal(err)
	}
	service, err := customerapp.NewOwnerHandoffService(uow, store, staff, ownerHandoffRuntimeResolver{candidates: candidates}, audit, platformoutbox.NewPostgreSQL())
	if err != nil {
		t.Fatal(err)
	}
	if err = service.SetBatchEnqueuer(enqueuer); err != nil {
		t.Fatal(err)
	}
	preview, err := service.PreviewOwnerHandoff(ctx, customerport.OwnerHandoffPreviewCommand{ActorAdminUserID: sourceID, Mode: customerport.OwnerHandoffLocalOnly, SourceStaffID: sourceID, TargetStaffID: targetID, CorpScope: "wecom-corp:runtime", CustomerIDs: ids, ConfirmationPhrase: "CONFIRM", IdempotencyKey: "segment-preview-20000"})
	if err != nil {
		t.Fatal(err)
	}
	batch, err := service.ConfirmOwnerHandoff(ctx, customerport.OwnerHandoffConfirmCommand{ActorAdminUserID: sourceID, PreviewID: preview.ID, PreviewHash: preview.Hash, ConfirmationPhrase: "CONFIRM", IdempotencyKey: "segment-confirm-20000"})
	if err != nil || len(batch.Lines) != handoffRows || batch.State != "accepted" {
		t.Fatalf("accept batch=%+v err=%v", batch, err)
	}
	runCtx, stopRun := context.WithCancel(ctx)
	// Commit segment zero, then wait until River has actually claimed segment
	// one. This makes the shutdown boundary deterministic under -race: the
	// successor must be returned to River durably without calling the service.
	firstSegmentCommitted := make(chan struct{}, 1)
	firstLaterSegmentClaimed := make(chan struct{}, 1)
	firstLaterSegmentReleased := make(chan struct{}, 1)
	firstInterrupt := make(chan struct{})
	firstWorker := customer.NewOwnerHandoffBatchWorker()
	if err = firstWorker.Bind(interruptAfterOwnerHandoffSegment{service: service, segmentZeroCommitted: firstSegmentCommitted, laterSegmentClaimed: firstLaterSegmentClaimed, laterSegmentReleased: firstLaterSegmentReleased, interrupt: firstInterrupt}); err != nil {
		t.Fatal(err)
	}
	workers = river.NewWorkers()
	if err = river.AddWorkerSafely[customer.OwnerHandoffBatchJobArgs](workers, firstWorker); err != nil {
		t.Fatal(err)
	}
	firstRuntime, err := platformjobqueue.NewRuntime(native, workers, customer.OwnerHandoffQueue)
	if err != nil {
		t.Fatal(err)
	}
	firstDone := make(chan error, 1)
	go func() { firstDone <- firstRuntime.Run(runCtx) }()
	var releaseFirstRuntimeOnce sync.Once
	releaseFirstRuntime := func() {
		releaseFirstRuntimeOnce.Do(func() {
			stopRun()
			close(firstInterrupt)
		})
	}
	defer releaseFirstRuntime()
	select {
	case <-firstSegmentCommitted:
		// The explicit boundary above distinguishes a slow River claim from a
		// failed first Customer segment. Runtime shutdown is checked below.
	case <-time.After(30 * time.Second):
		var updated, queued, jobs int
		queryErr := native.QueryRow(ctx, `SELECT
			(SELECT count(*) FROM customer_owner_handoff_lines WHERE batch_id=$1 AND state='local_updated'),
			(SELECT count(*) FROM customer_owner_handoff_lines WHERE batch_id=$1 AND state='queued'),
			(SELECT count(*) FROM river_job WHERE kind='customer.owner-handoff.v1')`, batch.ID).Scan(&updated, &queued, &jobs)
		releaseFirstRuntime()
		select {
		case <-firstDone:
		case <-time.After(20 * time.Second):
		}
		t.Fatalf("first owner-handoff segment did not commit within bounded start window: updated=%d queued=%d jobs=%d query_err=%v", updated, queued, jobs, queryErr)
	}
	select {
	case <-firstLaterSegmentClaimed:
	case <-time.After(30 * time.Second):
		releaseFirstRuntime()
		select {
		case <-firstDone:
		case <-time.After(20 * time.Second):
		}
		t.Fatal("first owner-handoff successor was not claimed before interruption")
	}
	var heldUpdated int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM customer_owner_handoff_lines WHERE batch_id=$1 AND state='local_updated'`, batch.ID).Scan(&heldUpdated); err != nil || heldUpdated != 100 {
		t.Fatalf("claimed successor entered Customer service before interruption: updated=%d err=%v", heldUpdated, err)
	}
	releaseFirstRuntime()
	select {
	case <-firstLaterSegmentReleased:
	case <-time.After(20 * time.Second):
		t.Fatal("first owner-handoff successor did not leave the interruption gate")
	}
	select {
	case runErr := <-firstDone:
		if runErr != nil && runErr != context.Canceled {
			t.Fatalf("first runtime stop: %v", runErr)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("first owner-handoff runtime did not stop after its successor was released")
	}
	var firstUpdated, firstQueued, resumableJobs int
	if err = native.QueryRow(ctx, `SELECT
		count(*) FILTER (WHERE state='local_updated'),
		count(*) FILTER (WHERE state='queued')
		FROM customer_owner_handoff_lines WHERE batch_id=$1`, batch.ID).Scan(&firstUpdated, &firstQueued); err != nil || firstUpdated != 100 || firstQueued != handoffRows-100 {
		t.Fatalf("first segment updated=%d queued=%d err=%v", firstUpdated, firstQueued, err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM river_job WHERE kind='customer.owner-handoff.v1' AND state IN ('available','scheduled')`).Scan(&resumableJobs); err != nil || resumableJobs != 1 {
		t.Fatalf("interrupted successor resumable_jobs=%d err=%v", resumableJobs, err)
	}
	restartWorker := customer.NewOwnerHandoffBatchWorker()
	if err = restartWorker.Bind(service); err != nil {
		t.Fatal(err)
	}
	workers = river.NewWorkers()
	if err = river.AddWorkerSafely[customer.OwnerHandoffBatchJobArgs](workers, restartWorker); err != nil {
		t.Fatal(err)
	}
	restartRuntime, err := platformjobqueue.NewRuntime(native, workers, customer.OwnerHandoffQueue)
	if err != nil {
		t.Fatal(err)
	}
	restartCtx, restartStop := context.WithCancel(ctx)
	restartDone := make(chan error, 1)
	go func() { restartDone <- restartRuntime.Run(restartCtx) }()
	defer func() {
		restartStop()
		select {
		case runErr := <-restartDone:
			if runErr != nil && runErr != context.Canceled {
				t.Errorf("restart runtime stop: %v", runErr)
			}
		case <-time.After(5 * time.Second):
			t.Error("owner-handoff restart runtime did not stop")
		}
	}()
	deadline := time.Now().Add(150 * time.Second)
	for time.Now().Before(deadline) {
		var updated, owners, jobs, effects int
		err = native.QueryRow(ctx, `SELECT
			(SELECT count(*) FROM customer_owner_handoff_lines WHERE batch_id=$1 AND state='local_updated'),
			(SELECT count(*) FROM customer_local_owners WHERE source='owner_handoff_local_only'),
			(SELECT count(*) FROM river_job WHERE kind='customer.owner-handoff.v1'),
			(SELECT count(*) FROM external_effects WHERE kind='customer_owner_handoff')`, batch.ID).Scan(&updated, &owners, &jobs, &effects)
		if err == nil && updated == handoffRows && owners == handoffRows && jobs == handoffRows/100 && effects == 0 {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}

	var updated, owners, jobs, effects int
	if err = native.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM customer_owner_handoff_lines WHERE batch_id=$1 AND state='local_updated'),
		(SELECT count(*) FROM customer_local_owners WHERE source='owner_handoff_local_only'),
		(SELECT count(*) FROM river_job WHERE kind='customer.owner-handoff.v1'),
		(SELECT count(*) FROM external_effects WHERE kind='customer_owner_handoff')`, batch.ID).Scan(&updated, &owners, &jobs, &effects); err != nil {
		t.Fatal(err)
	}
	t.Fatalf("segment completion updated=%d owners=%d jobs=%d effects=%d", updated, owners, jobs, effects)
}
