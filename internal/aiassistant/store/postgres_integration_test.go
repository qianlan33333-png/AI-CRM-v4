package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	aiassistantapp "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/app"
	aiassistantdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/domain"
	aiassistantport "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/port"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	operationport "github.com/qianlan33333-png/AI-CRM-v3/internal/operationcycle/port"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

type integrationCustomers struct{}

func (integrationCustomers) CustomerSnapshot(_ context.Context, id customerdomain.CustomerID) (aiassistantapp.CustomerSnapshot, error) {
	return aiassistantapp.CustomerSnapshot{CanonicalID: id, Status: customerdomain.StatusActive, DisplayName: "customer", OneIDLabel: "OneID"}, nil
}

type integrationStaff struct{}

func (integrationStaff) StaffSnapshot(_ context.Context, id int64) (aiassistantapp.StaffSnapshot, error) {
	return aiassistantapp.StaffSnapshot{ID: id, DisplayName: "staff", Active: true}, nil
}
func (integrationStaff) StaffByWeComUserID(_ context.Context, value string) (aiassistantapp.StaffSnapshot, error) {
	if value == "" {
		return aiassistantapp.StaffSnapshot{}, errors.New("missing staff")
	}
	return aiassistantapp.StaffSnapshot{ID: 21, DisplayName: "staff", Active: true}, nil
}

type integrationMaterials struct{}

func (integrationMaterials) ResolveMaterial(_ context.Context, block aiassistantport.ContentBlock) (aiassistantport.ContentBlock, error) {
	return block, nil
}
func (integrationMaterials) RegisterMaterialReference(context.Context, aiassistantport.ContentBlock, effectport.Digest) error {
	return nil
}

type integrationIdentities struct{}

type integrationExcelStrategies struct{}

func (integrationExcelStrategies) OperationCycleStrategy(_ context.Context, key string) (operationport.Strategy, error) {
	return operationport.Strategy{Key: key, Title: key, Status: "active", Version: 1, Definition: json.RawMessage(`{}`), Snapshot: json.RawMessage(`{}`)}, nil
}

func (integrationIdentities) Resolve(context.Context, identitydomain.Reference) (identityport.ResolveResult, error) {
	return identityport.ResolveResult{Status: identityport.ResolveNotFound}, nil
}
func (integrationIdentities) VerifiedExternalIdentityValue(context.Context, customerdomain.CustomerID, identitydomain.Kind, string) (string, bool, error) {
	return "", false, nil
}

// This PG16 read-model test proves one bounded repository query selects only
// the newest batch per requested strategy and aggregates each batch's current
// content facts. It neither resolves identities nor creates any outbound work.
func TestPostgreSQLLatestOperationExcelBatchOverviews(t *testing.T) {
	native, cleanup := integrationPool(t)
	defer cleanup()
	wrapped, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	service, err := aiassistantapp.NewService(uow, repository, integrationCustomers{}, integrationStaff{}, integrationMaterials{}, integrationIdentities{}, integrationIdentities{})
	if err != nil {
		t.Fatal(err)
	}
	if err = service.BindExcelBatchStrategyReader(integrationExcelStrategies{}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	baseTime := time.Date(2026, time.September, 12, 9, 0, 0, 0, time.UTC)
	createBatch := func(key, strategy, suffix string, createdAt time.Time) aiassistantport.Plan {
		t.Helper()
		rows := []aiassistantport.ExcelBatchRow{
			{UnionID: "summary-" + suffix + "-one", SenderUserID: "staff-" + suffix, Text: "第一条", Card: aiassistantport.ExcelCard{AppID: "summary-app", Path: "pages/summary", Title: "Excel 标题"}, Segment: "A"},
			{UnionID: "summary-" + suffix + "-two", SenderUserID: "staff-" + suffix, Text: "第二条", Card: aiassistantport.ExcelCard{AppID: "summary-app", Path: "pages/summary", Title: ""}, Segment: "B"},
		}
		created, createErr := service.CreateOperationExcelBatch(ctx, aiassistantport.ExcelBatchCommand{Actor: aiassistantport.Actor{Kind: aiassistantport.ActorAdmin, ID: 7}, IdempotencyKey: "summary-plan-" + key, BatchKey: key, StrategyKey: strategy, Name: "摘要批次 " + suffix, Scope: "wechat-open-platform:summary", FileDigest: effectport.Hash("summary", key), Rows: rows, OccurredAt: createdAt})
		if createErr != nil {
			t.Fatal(createErr)
		}
		return created.Plan
	}
	oldWeekly := createBatch("summary-weekly-old", "weekly.review", "old", baseTime)
	newWeekly := createBatch("summary-weekly-new", "weekly.review", "new", baseTime.Add(time.Minute))
	daily := createBatch("summary-daily", "daily.review", "daily", baseTime.Add(2*time.Minute))

	overviews, err := service.LatestOperationExcelBatchOverviews(ctx, []string{"weekly.review", "daily.review", "no-batch.review"})
	if err != nil || len(overviews) != 2 {
		t.Fatalf("overviews=%+v err=%v", overviews, err)
	}
	byStrategy := make(map[string]aiassistantport.ExcelBatchOverview, len(overviews))
	for _, overview := range overviews {
		byStrategy[overview.Meta.StrategyKey] = overview
	}
	weekly, weeklyOK := byStrategy["weekly.review"]
	dailyOverview, dailyOK := byStrategy["daily.review"]
	if !weeklyOK || weekly.Meta.PlanID != newWeekly.ID || weekly.Meta.PlanID == oldWeekly.ID || weekly.Summary != (aiassistantport.ExcelBatchSummary{TotalRows: 2, ExcludedRows: 0, EmptyTitleRows: 1, ExpectedTasks: 1}) || weekly.State != newWeekly.State || weekly.PlanVersion != newWeekly.Version || weekly.SourceKind != "excel_batch" {
		t.Fatalf("weekly overview=%+v new=%+v old=%+v", weekly, newWeekly, oldWeekly)
	}
	if !dailyOK || dailyOverview.Meta.PlanID != daily.ID || dailyOverview.Summary != weekly.Summary {
		t.Fatalf("daily overview=%+v daily=%+v", dailyOverview, daily)
	}
	if _, err = service.LatestOperationExcelBatchOverviews(ctx, []string{"weekly.review", "weekly.review"}); !errors.Is(err, aiassistantapp.ErrInvalid) {
		t.Fatalf("duplicate keys err=%v", err)
	}
}

func TestPostgreSQLPlanReceiptAuditOutboxAtomicJourney(t *testing.T) {
	native, cleanup := integrationPool(t)
	defer cleanup()
	wrapped, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 4, 1, 2, 3, 0, time.UTC)
	aggregate, err := aiassistantdomain.NewPlan("retention review", "automation", effectport.Hash("source", "1"), 2, 7, now)
	if err != nil {
		t.Fatal(err)
	}
	recipients := []aiassistantport.RecipientCandidate{
		{CustomerID: customerdomain.CustomerID(11), StaffID: 21, Content: []aiassistantport.ContentBlock{{Kind: aiassistantport.ContentText, Text: "hello 11"}}},
		{CustomerID: customerdomain.CustomerID(12), StaffID: 21, Content: []aiassistantport.ContentBlock{{Kind: aiassistantport.ContentText, Text: "hello 12"}}},
	}
	keyDigest, payloadDigest := sha256.Sum256([]byte("key")), sha256.Sum256([]byte("payload"))
	var plan aiassistantport.Plan
	err = uow.Within(context.Background(), func(tx context.Context) error {
		receipt, created, reserveErr := repository.Reserve(tx, Reservation{Operation: "create", ActorScope: "service:7", KeyDigest: keyDigest, PayloadDigest: payloadDigest, CreatedAt: now})
		if reserveErr != nil || !created {
			return reserveErr
		}
		var createErr error
		plan, _, createErr = repository.CreatePlan(tx, aggregate, recipients, 7, now)
		if createErr != nil {
			return createErr
		}
		if appendErr := repository.AppendEvent(tx, aiassistantport.Event{Type: aiassistantport.EventPlanCreated, AggregateID: plan.ID, ActorID: 7, IdempotencyKey: "event-plan-1", Payload: []byte(`{"plan_id":1}`), OccurredAt: now}); appendErr != nil {
			return appendErr
		}
		_, completeErr := repository.Complete(tx, receipt.ID, []byte(`{"plan_id":1}`), now)
		return completeErr
	})
	if err != nil {
		t.Fatal(err)
	}
	var plans, recipientCount, contents, receipts, audits, outbox, effects int
	err = native.QueryRow(context.Background(), `SELECT
		(SELECT count(*) FROM ai_assistant_plans),
		(SELECT count(*) FROM ai_assistant_plan_recipients),
		(SELECT count(*) FROM ai_assistant_content_versions),
		(SELECT count(*) FROM ai_assistant_operation_receipts),
		(SELECT count(*) FROM ai_assistant_audit_events),
		(SELECT count(*) FROM ai_assistant_outbox),
		(SELECT count(*) FROM ai_assistant_effect_bindings)`).Scan(&plans, &recipientCount, &contents, &receipts, &audits, &outbox, &effects)
	if err != nil {
		t.Fatal(err)
	}
	if plans != 1 || recipientCount != 2 || contents != 2 || receipts != 1 || audits != 1 || outbox != 1 || effects != 0 {
		t.Fatalf("plans=%d recipients=%d contents=%d receipts=%d audits=%d outbox=%d effects=%d", plans, recipientCount, contents, receipts, audits, outbox, effects)
	}
	rollback := errors.New("inject rollback")
	err = uow.Within(context.Background(), func(tx context.Context) error {
		other, newErr := aiassistantdomain.NewPlan("rolled back", "automation", effectport.Hash("source", "2"), 1, 7, now)
		if newErr != nil {
			return newErr
		}
		if _, _, createErr := repository.CreatePlan(tx, other, recipients[:1], 7, now); createErr != nil {
			return createErr
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("rollback err=%v", err)
	}
	if err = native.QueryRow(context.Background(), `SELECT count(*) FROM ai_assistant_plans`).Scan(&plans); err != nil || plans != 1 {
		t.Fatalf("rolled-back plan visible count=%d err=%v", plans, err)
	}
	if _, err = native.Exec(context.Background(), `UPDATE ai_assistant_content_versions SET version=version`); err == nil {
		t.Fatal("content history accepted mutation")
	}
	if plan.State != aiassistantport.PlanPendingReview {
		t.Fatalf("state=%s", plan.State)
	}
}

func TestPostgreSQLPlanSizesAndFiftyRecipientPagination(t *testing.T) {
	native, cleanup := integrationPool(t)
	defer cleanup()
	wrapped, err := platformpostgres.Wrap(native, 60*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	service, err := aiassistantapp.NewService(uow, repository, integrationCustomers{}, integrationStaff{}, integrationMaterials{}, integrationIdentities{}, integrationIdentities{})
	if err != nil {
		t.Fatal(err)
	}
	for _, size := range []int{1, 50, 51, 5000} {
		recipients := make([]aiassistantport.RecipientCandidate, size)
		for index := range recipients {
			recipients[index] = aiassistantport.RecipientCandidate{CustomerID: customerdomain.CustomerID(index + 1), StaffID: 21, Content: []aiassistantport.ContentBlock{{Kind: aiassistantport.ContentText, Text: "hello"}}}
		}
		created, createErr := service.CreatePlan(context.Background(), aiassistantport.CreatePlanCommand{Actor: aiassistantport.Actor{Kind: aiassistantport.ActorAdmin, ID: 7}, IdempotencyKey: "size-plan-" + strconv.Itoa(size), Name: "size plan", SourceKind: "test", SourceDigest: effectport.Hash("source", strconv.Itoa(size)), Recipients: recipients, OccurredAt: time.Date(2026, 9, 4, 1, 2, 3, 0, time.UTC)})
		if createErr != nil {
			t.Fatalf("size=%d create: %v", size, createErr)
		}
		if size == 51 {
			// Exercise the real owner reader across its 50-row boundary with
			// mixed terminal facts. Automation consumes only this stable Port.
			if _, err = native.Exec(context.Background(), `UPDATE ai_assistant_plan_recipients
				SET execution_state=CASE WHEN id=(SELECT max(id) FROM ai_assistant_plan_recipients WHERE plan_id=$1) THEN 'outcome_unknown' ELSE 'retryable_failed' END
				WHERE plan_id=$1`, created.Plan.ID); err != nil {
				t.Fatalf("size=%d set mixed execution states: %v", size, err)
			}
		}
		seen, unknown, retryable, cursor := 0, 0, 0, ""
		for {
			page, pageErr := service.ListRecipients(context.Background(), aiassistantport.RecipientPageQuery{PlanID: created.Plan.ID, Limit: 50, Cursor: cursor})
			if pageErr != nil {
				t.Fatalf("size=%d page: %v", size, pageErr)
			}
			if len(page.Items) == 0 || len(page.Items) > 50 {
				t.Fatalf("size=%d invalid page length=%d", size, len(page.Items))
			}
			seen += len(page.Items)
			for _, recipient := range page.Items {
				switch recipient.ExecutionState {
				case aiassistantport.ExecutionOutcomeUnknown:
					unknown++
				case aiassistantport.ExecutionRetryableFailed:
					retryable++
				}
			}
			if page.NextCursor == "" {
				break
			}
			cursor = page.NextCursor
		}
		if seen != size || created.Plan.TargetCount != size || created.Plan.PendingCount != size {
			t.Fatalf("size=%d seen=%d plan=%+v", size, seen, created.Plan)
		}
		if size == 51 && (unknown != 1 || retryable != 50) {
			t.Fatalf("mixed second-page states unknown=%d retryable=%d", unknown, retryable)
		}
	}
}

func TestPostgreSQLCreatePlanWithinRequiresCallerTransactionAndRollsBackWithIt(t *testing.T) {
	native, cleanup := integrationPool(t)
	defer cleanup()
	wrapped, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	service, err := aiassistantapp.NewService(uow, repository, integrationCustomers{}, integrationStaff{}, integrationMaterials{}, integrationIdentities{}, integrationIdentities{})
	if err != nil {
		t.Fatal(err)
	}
	command := aiassistantport.CreatePlanCommand{Actor: aiassistantport.Actor{Kind: aiassistantport.ActorAdmin, ID: 7}, IdempotencyKey: "within-plan-rollback-0001", Name: "within plan", SourceKind: "automation.manual_audience_run.v1", SourceDigest: effectport.Hash("source", "within"), Recipients: []aiassistantport.RecipientCandidate{{CustomerID: 1, StaffID: 21, Content: []aiassistantport.ContentBlock{{Kind: aiassistantport.ContentText, Text: "hello"}}}}, OccurredAt: time.Date(2026, 9, 5, 1, 2, 3, 0, time.UTC)}
	if _, err = service.CreatePlanWithin(context.Background(), command); !errors.Is(err, aiassistantapp.ErrUnavailable) {
		t.Fatalf("unbound CreatePlanWithin err=%v", err)
	}
	rollback := errors.New("rollback caller transaction")
	err = uow.Within(context.Background(), func(tx context.Context) error {
		created, createErr := service.CreatePlanWithin(tx, command)
		if createErr != nil || created.Plan.ID < 1 {
			t.Fatalf("within create=%+v err=%v", created, createErr)
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("rollback err=%v", err)
	}
	var count int
	if err = native.QueryRow(context.Background(), `SELECT count(*) FROM ai_assistant_plans`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rolled-back plans=%d err=%v", count, err)
	}
	var first aiassistantport.CreatePlanResult
	if err = uow.Within(context.Background(), func(tx context.Context) error {
		var createErr error
		first, createErr = service.CreatePlanWithin(tx, command)
		return createErr
	}); err != nil || first.Plan.ID < 1 || first.Replayed {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	var replay aiassistantport.CreatePlanResult
	if err = uow.Within(context.Background(), func(tx context.Context) error {
		var createErr error
		replay, createErr = service.CreatePlanWithin(tx, command)
		return createErr
	}); err != nil || !replay.Replayed || replay.Plan.ID != first.Plan.ID {
		t.Fatalf("replay=%+v first=%+v err=%v", replay, first, err)
	}
}

func TestPostgreSQLConcurrentEffectCompletionsKeepNeedsAttentionProjection(t *testing.T) {
	native, cleanup := integrationPool(t)
	defer cleanup()
	wrapped, err := platformpostgres.Wrap(native, 15*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	// Repeat the pair because the old implementation could only expose the
	// stale aggregate after the two independent recipient writes overlapped.
	// Each iteration uses one plan so the assertion remains a per-aggregate
	// invariant, not a global timing assumption.
	for iteration := 0; iteration < 16; iteration++ {
		planID, effects := seedConcurrentCompletionPlan(t, ctx, native, iteration)
		start := make(chan struct{})
		errs := make(chan error, 2)
		var wait sync.WaitGroup
		for index, completion := range []struct {
			effectID string
			state    aiassistantport.ExecutionState
		}{
			{effectID: effects[0], state: aiassistantport.ExecutionProviderAccepted},
			{effectID: effects[1], state: aiassistantport.ExecutionOutcomeUnknown},
		} {
			wait.Add(1)
			go func(index int, completion struct {
				effectID string
				state    aiassistantport.ExecutionState
			}) {
				defer wait.Done()
				<-start
				errs <- uow.Within(ctx, func(tx context.Context) error {
					return repository.CompleteExternalEffect(tx, completion.effectID, completion.state, completion.state == aiassistantport.ExecutionProviderAccepted, false, effectport.Hash("completion-receipt", completion.effectID), 1, 1, 1, time.Now().UTC())
				})
			}(index, completion)
		}
		close(start)
		wait.Wait()
		close(errs)
		for completionErr := range errs {
			if completionErr != nil {
				t.Fatalf("completion iteration %d: %v", iteration, completionErr)
			}
		}
		var state aiassistantport.PlanState
		var attention int
		if err = native.QueryRow(ctx, `SELECT state,needs_attention_count FROM ai_assistant_plans WHERE id=$1`, planID).Scan(&state, &attention); err != nil {
			t.Fatalf("read completion projection iteration %d: %v", iteration, err)
		}
		if state != aiassistantport.PlanNeedsAttention || attention != 1 {
			t.Fatalf("completion projection iteration %d state=%s attention=%d", iteration, state, attention)
		}
	}
}

func seedConcurrentCompletionPlan(t *testing.T, ctx context.Context, pool *pgxpool.Pool, iteration int) (aiassistantport.PlanID, [2]string) {
	t.Helper()
	var planID aiassistantport.PlanID
	now := time.Now().UTC()
	if err := pool.QueryRow(ctx, `INSERT INTO ai_assistant_plans(name,source_kind,source_digest,state,version,target_count,pending_count,approved_count,rejected_count,ineligible_count,needs_attention_count,created_by,created_at,updated_at)
		VALUES($1,'completion-race',decode(repeat('01',32),'hex'),'dispatching',1,2,0,2,0,0,0,1,$2,$2) RETURNING id`, "completion-race-"+strconv.Itoa(iteration), now).Scan(&planID); err != nil {
		t.Fatal(err)
	}
	for recipient := 0; recipient < 2; recipient++ {
		var recipientID int64
		if err := pool.QueryRow(ctx, `INSERT INTO ai_assistant_plan_recipients(plan_id,customer_id,staff_id,review_state,execution_state,created_at,updated_at)
			VALUES($1,$2,1,'approved','queued',$3,$3) RETURNING id`, planID, iteration*10+recipient+1, now).Scan(&recipientID); err != nil {
			t.Fatal(err)
		}
		effectID := "eer_" + strconv.Itoa(iteration*2+recipient+1)
		if _, err := pool.Exec(ctx, `INSERT INTO ai_assistant_effect_bindings(recipient_id,outbound_intent_id,external_effect_id,payload_digest,state,generation,created_at,updated_at)
			VALUES($1,$2,$3,decode(repeat('02',32),'hex'),'queued',1,$4,$4)`, recipientID, iteration*2+recipient+1, effectID, now); err != nil {
			t.Fatal(err)
		}
		if recipient == 0 {
			continue
		}
		return planID, [2]string{"eer_" + strconv.Itoa(iteration*2+1), effectID}
	}
	t.Fatal("completion fixture did not create two effects")
	return 0, [2]string{}
}

func TestPostgreSQLIntegrationNonceAllowsOnlyExactReplay(t *testing.T) {
	native, cleanup := integrationPool(t)
	defer cleanup()
	wrapped, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 9, 4, 1, 2, 3, 0, time.UTC)
	payload := sha256.Sum256([]byte("same payload"))
	if err = uow.Within(context.Background(), func(tx context.Context) error {
		return repository.ReserveIntegrationNonce(tx, "integration-key", "1234567890abcdef", "idem-key-1", payload, at, at.Add(5*time.Minute))
	}); err != nil {
		t.Fatalf("reserve nonce: %v", err)
	}
	err = uow.Within(context.Background(), func(tx context.Context) error {
		return repository.ReserveIntegrationNonce(tx, "integration-key", "1234567890abcdef", "idem-key-1", payload, at, at.Add(5*time.Minute))
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("replayed nonce err=%v", err)
	}
	drift := sha256.Sum256([]byte("changed payload"))
	err = uow.Within(context.Background(), func(tx context.Context) error {
		return repository.ReserveIntegrationNonce(tx, "integration-key", "1234567890abcdef", "idem-key-1", drift, at, at.Add(5*time.Minute))
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("drift err=%v", err)
	}
}

func TestPostgreSQLMachinePlanActorScopesReceiptsFactsAndStatus(t *testing.T) {
	native, cleanup := integrationPool(t)
	defer cleanup()
	wrapped, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	service, err := aiassistantapp.NewService(uow, repository, integrationCustomers{}, integrationStaff{}, integrationMaterials{}, integrationIdentities{}, integrationIdentities{})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	machineA, err := aiassistantport.MachineActorFromAuthenticatedPrincipal("machine:review-a")
	if err != nil {
		t.Fatal(err)
	}
	machineB, err := aiassistantport.MachineActorFromAuthenticatedPrincipal("machine:review-b")
	if err != nil {
		t.Fatal(err)
	}
	command := func(actor aiassistantport.MachineActor, name string) aiassistantport.MachineCreatePlanCommand {
		return aiassistantport.MachineCreatePlanCommand{
			Actor: actor, IdempotencyKey: "machine-review-same-key-0001", Name: name, SourceKind: "open.review_plan.v1",
			SourceDigest: effectport.Hash("machine-review-source", name),
			Recipients:   []aiassistantport.RecipientCandidate{{CustomerID: 11, StaffID: 21, Content: []aiassistantport.ContentBlock{{Kind: aiassistantport.ContentText, Text: "review copy"}}}},
			OccurredAt:   at,
		}
	}
	first, err := service.CreateMachinePlan(context.Background(), command(machineA, "client A review"))
	if err != nil || first.Replayed || first.Plan.ID < 1 || first.Plan.ReviewState != aiassistantport.ReviewPending {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	replay, err := service.CreateMachinePlan(context.Background(), command(machineA, "client A review"))
	if err != nil || !replay.Replayed || replay.Plan.ID != first.Plan.ID {
		t.Fatalf("replay=%+v first=%+v err=%v", replay, first, err)
	}
	if _, err = service.CreateMachinePlan(context.Background(), command(machineA, "changed request")); !errors.Is(err, aiassistantapp.ErrIdempotencyConflict) {
		t.Fatalf("same-machine drift err=%v", err)
	}
	second, err := service.CreateMachinePlan(context.Background(), command(machineB, "client B review"))
	if err != nil || second.Replayed || second.Plan.ID == first.Plan.ID {
		t.Fatalf("cross-machine=%+v first=%+v err=%v", second, first, err)
	}
	if _, err = service.GetMachinePlan(context.Background(), machineB, first.Plan.ID); !errors.Is(err, aiassistantapp.ErrNotFound) {
		t.Fatalf("cross-machine read err=%v", err)
	}
	if _, err = service.GetMachineOperationStatus(context.Background(), machineB, first.Plan.ID); !errors.Is(err, aiassistantapp.ErrNotFound) {
		t.Fatalf("cross-machine operation status err=%v", err)
	}
	if status, getErr := service.GetMachineOperationStatus(context.Background(), machineA, first.Plan.ID); getErr != nil || status.ReviewState != aiassistantport.ReviewPending || status.OperationState != aiassistantport.MachineOperationPendingReview || status.OutcomeUnknownCount != 0 {
		t.Fatalf("pending operation status=%+v err=%v", status, getErr)
	}
	if _, err = native.Exec(context.Background(), `UPDATE ai_assistant_plans SET state='approved',pending_count=0,approved_count=1 WHERE id=$1`, first.Plan.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(context.Background(), `UPDATE ai_assistant_plan_recipients SET review_state='approved',execution_state='not_accepted' WHERE plan_id=$1`, first.Plan.ID); err != nil {
		t.Fatal(err)
	}
	if status, getErr := service.GetMachineOperationStatus(context.Background(), machineA, first.Plan.ID); getErr != nil || status.ReviewState != aiassistantport.ReviewApproved || status.OperationState != aiassistantport.MachineOperationApproved || status.OutcomeUnknownCount != 0 {
		t.Fatalf("approved but unexecuted operation status=%+v err=%v", status, getErr)
	}
	if _, err = native.Exec(context.Background(), `UPDATE ai_assistant_plans SET state='needs_attention',needs_attention_count=1 WHERE id=$1`, first.Plan.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(context.Background(), `UPDATE ai_assistant_plan_recipients SET execution_state='outcome_unknown' WHERE plan_id=$1`, first.Plan.ID); err != nil {
		t.Fatal(err)
	}
	if status, getErr := service.GetMachineOperationStatus(context.Background(), machineA, first.Plan.ID); getErr != nil || status.ReviewState != aiassistantport.ReviewApproved || status.OperationState != aiassistantport.MachineOperationOutcomeUnknown || status.OutcomeUnknownCount != 1 {
		t.Fatalf("outcome-unknown operation status=%+v err=%v", status, getErr)
	}
	if _, err = native.Exec(context.Background(), `UPDATE ai_assistant_plan_recipients SET execution_state='retryable_failed' WHERE plan_id=$1`, first.Plan.ID); err != nil {
		t.Fatal(err)
	}
	if status, getErr := service.GetMachineOperationStatus(context.Background(), machineA, first.Plan.ID); getErr != nil || status.OperationState != aiassistantport.MachineOperationNeedsAttention || status.OutcomeUnknownCount != 0 || status.RetryableFailureCount != 1 {
		t.Fatalf("retryable operation status=%+v err=%v", status, getErr)
	}
	if status, getErr := service.GetMachinePlan(context.Background(), machineA, first.Plan.ID); getErr != nil || status.ReviewState != aiassistantport.ReviewApproved {
		t.Fatalf("machine review state=%+v err=%v", status, getErr)
	}
	if _, err = native.Exec(context.Background(), `UPDATE ai_assistant_plans SET state='completed',pending_count=0,approved_count=1 WHERE id=$1`, first.Plan.ID); err != nil {
		t.Fatal(err)
	}
	if status, getErr := service.GetMachinePlan(context.Background(), machineA, first.Plan.ID); getErr != nil || status.ReviewState != aiassistantport.ReviewApproved {
		t.Fatalf("execution state leaked into machine status=%+v err=%v", status, getErr)
	}
	if _, err = native.Exec(context.Background(), `UPDATE ai_assistant_plans SET state='rejected',approved_count=0,rejected_count=1 WHERE id=$1`, first.Plan.ID); err != nil {
		t.Fatal(err)
	}
	if status, getErr := service.GetMachinePlan(context.Background(), machineA, first.Plan.ID); getErr != nil || status.ReviewState != aiassistantport.ReviewRejected {
		t.Fatalf("rejected status=%+v err=%v", status, getErr)
	}

	var planActorID, contentActorID, auditActorID *int64
	var planKind, planRef, contentKind, contentRef, auditKind, auditRef string
	if err = native.QueryRow(context.Background(), `SELECT created_by,created_actor_kind,created_actor_ref FROM ai_assistant_plans WHERE id=$1`, first.Plan.ID).Scan(&planActorID, &planKind, &planRef); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(context.Background(), `SELECT created_by,created_actor_kind,created_actor_ref FROM ai_assistant_content_versions WHERE recipient_id=(SELECT id FROM ai_assistant_plan_recipients WHERE plan_id=$1)`, first.Plan.ID).Scan(&contentActorID, &contentKind, &contentRef); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(context.Background(), `SELECT actor_id,actor_kind,actor_ref FROM ai_assistant_audit_events WHERE plan_id=$1`, first.Plan.ID).Scan(&auditActorID, &auditKind, &auditRef); err != nil {
		t.Fatal(err)
	}
	if planActorID != nil || contentActorID != nil || auditActorID != nil || planKind != aiassistantport.MachineActorKind || contentKind != aiassistantport.MachineActorKind || auditKind != aiassistantport.MachineActorKind || planRef != machineA.Reference || contentRef != machineA.Reference || auditRef != machineA.Reference {
		t.Fatalf("machine attribution plan=(%v,%q,%q) content=(%v,%q,%q) audit=(%v,%q,%q)", planActorID, planKind, planRef, contentActorID, contentKind, contentRef, auditActorID, auditKind, auditRef)
	}
	var recipientID int64
	if err = native.QueryRow(context.Background(), `SELECT id FROM ai_assistant_plan_recipients WHERE plan_id=$1`, first.Plan.ID).Scan(&recipientID); err != nil {
		t.Fatal(err)
	}
	assertActorProjectionRejected(t, native, "ck_ai_assistant_plan_machine_actor", `INSERT INTO ai_assistant_plans(name,source_kind,source_digest,state,version,target_count,pending_count,approved_count,rejected_count,ineligible_count,needs_attention_count,created_by,created_actor_kind,created_actor_ref,created_at,updated_at)
		VALUES('missing actor','test',decode(repeat('01',32),'hex'),'pending_review',1,1,1,0,0,0,0,NULL,NULL,NULL,$1,$1)`, at)
	assertActorProjectionRejected(t, native, "ck_ai_assistant_plan_machine_actor", `INSERT INTO ai_assistant_plans(name,source_kind,source_digest,state,version,target_count,pending_count,approved_count,rejected_count,ineligible_count,needs_attention_count,created_by,created_actor_kind,created_actor_ref,created_at,updated_at)
		VALUES('mixed actor','test',decode(repeat('02',32),'hex'),'pending_review',1,1,1,0,0,0,0,7,'machine','machine:review-a',$1,$1)`, at)
	assertActorProjectionRejected(t, native, "ck_ai_assistant_plan_machine_actor", `INSERT INTO ai_assistant_plans(name,source_kind,source_digest,state,version,target_count,pending_count,approved_count,rejected_count,ineligible_count,needs_attention_count,created_by,created_actor_kind,created_actor_ref,created_at,updated_at)
		VALUES('null kind','test',decode(repeat('07',32),'hex'),'pending_review',1,1,1,0,0,0,0,NULL,NULL,'machine:review-a',$1,$1)`, at)
	assertActorProjectionRejected(t, native, "ck_ai_assistant_content_machine_actor", `INSERT INTO ai_assistant_content_versions(recipient_id,version,content_digest,content_payload,created_by,created_actor_kind,created_actor_ref,created_at)
		VALUES($1,2,decode(repeat('03',32),'hex'),'[{"kind":"text","text":"missing actor"}]'::jsonb,NULL,NULL,NULL,$2)`, recipientID, at)
	assertActorProjectionRejected(t, native, "ck_ai_assistant_content_machine_actor", `INSERT INTO ai_assistant_content_versions(recipient_id,version,content_digest,content_payload,created_by,created_actor_kind,created_actor_ref,created_at)
		VALUES($1,2,decode(repeat('04',32),'hex'),'[{"kind":"text","text":"mixed actor"}]'::jsonb,7,'machine','machine:review-a',$2)`, recipientID, at)
	assertActorProjectionRejected(t, native, "ck_ai_assistant_content_machine_actor", `INSERT INTO ai_assistant_content_versions(recipient_id,version,content_digest,content_payload,created_by,created_actor_kind,created_actor_ref,created_at)
		VALUES($1,2,decode(repeat('08',32),'hex'),'[{"kind":"text","text":"null kind"}]'::jsonb,NULL,NULL,'machine:review-a',$2)`, recipientID, at)
	assertActorProjectionRejected(t, native, "ck_ai_assistant_audit_machine_actor", `INSERT INTO ai_assistant_audit_events(plan_id,operation,actor_id,actor_kind,actor_ref,payload_digest,occurred_at)
		VALUES($1,'missing actor',NULL,NULL,NULL,decode(repeat('05',32),'hex'),$2)`, first.Plan.ID, at)
	assertActorProjectionRejected(t, native, "ck_ai_assistant_audit_machine_actor", `INSERT INTO ai_assistant_audit_events(plan_id,operation,actor_id,actor_kind,actor_ref,payload_digest,occurred_at)
		VALUES($1,'mixed actor',7,'machine','machine:review-a',decode(repeat('06',32),'hex'),$2)`, first.Plan.ID, at)
	assertActorProjectionRejected(t, native, "ck_ai_assistant_audit_machine_actor", `INSERT INTO ai_assistant_audit_events(plan_id,operation,actor_id,actor_kind,actor_ref,payload_digest,occurred_at)
		VALUES($1,'null kind',NULL,NULL,'machine:review-a',decode(repeat('09',32),'hex'),$2)`, first.Plan.ID, at)
	var receiptCount int
	if err = native.QueryRow(context.Background(), `SELECT count(*) FROM ai_assistant_operation_receipts WHERE operation='machine_plan_create' AND actor_scope IN ($1,$2)`, machineA.Reference, machineB.Reference).Scan(&receiptCount); err != nil || receiptCount != 2 {
		t.Fatalf("machine receipts=%d err=%v", receiptCount, err)
	}

	human := aiassistantport.CreatePlanCommand{Actor: aiassistantport.Actor{Kind: aiassistantport.ActorAdmin, ID: 7}, IdempotencyKey: "human-receipt-legacy-0001", Name: "human compatibility", SourceKind: "manual", SourceDigest: effectport.Hash("human-source"), Recipients: []aiassistantport.RecipientCandidate{{CustomerID: 19, StaffID: 21, Content: []aiassistantport.ContentBlock{{Kind: aiassistantport.ContentText, Text: "human copy"}}}}, OccurredAt: at}
	if _, err = service.CreatePlan(context.Background(), human); err != nil {
		t.Fatalf("human create after 0100: %v", err)
	}
	expectedPayload, marshalErr := json.Marshal(human)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	expectedDigest := sha256.Sum256(expectedPayload)
	var actualDigest []byte
	var scope string
	if err = native.QueryRow(context.Background(), `SELECT actor_scope,payload_digest FROM ai_assistant_operation_receipts WHERE operation='plan_create' AND actor_scope='admin:7'`).Scan(&scope, &actualDigest); err != nil {
		t.Fatal(err)
	}
	if scope != "admin:7" || string(actualDigest) != string(expectedDigest[:]) {
		t.Fatalf("human receipt changed scope=%q digest=%x want=%x", scope, actualDigest, expectedDigest)
	}

	rollback := errors.New("machine plan caller rollback")
	within := command(machineA, "rolled back")
	within.IdempotencyKey = "machine-plan-within-rollback-0001"
	if _, err = service.CreateMachinePlanWithin(context.Background(), within); !errors.Is(err, aiassistantapp.ErrUnavailable) {
		t.Fatalf("unbound machine transactional create err=%v", err)
	}
	err = uow.Within(context.Background(), func(tx context.Context) error {
		created, createErr := service.CreateMachinePlanWithin(tx, within)
		if createErr != nil || created.Plan.ID < 1 {
			t.Fatalf("transactional machine create=%+v err=%v", created, createErr)
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("rollback err=%v", err)
	}
	var rolledBack int
	if err = native.QueryRow(context.Background(), `SELECT count(*) FROM ai_assistant_operation_receipts WHERE actor_scope=$1 AND operation='machine_plan_create' AND key_digest=$2`, machineA.Reference, machineKeyDigest(within.IdempotencyKey)).Scan(&rolledBack); err != nil {
		t.Fatal(err)
	}
	if rolledBack != 0 {
		t.Fatalf("rolled-back machine receipt remained=%d", rolledBack)
	}
}

func TestPostgreSQLMachinePlanSameClientRaceReplaysOneReceipt(t *testing.T) {
	native, cleanup := integrationPool(t)
	defer cleanup()
	wrapped, err := platformpostgres.Wrap(native, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	service, err := aiassistantapp.NewService(uow, repository, integrationCustomers{}, integrationStaff{}, integrationMaterials{}, integrationIdentities{}, integrationIdentities{})
	if err != nil {
		t.Fatal(err)
	}
	actor, err := aiassistantport.MachineActorFromAuthenticatedPrincipal("machine:review-race")
	if err != nil {
		t.Fatal(err)
	}
	command := aiassistantport.MachineCreatePlanCommand{Actor: actor, IdempotencyKey: "machine-plan-race-replay-0001", Name: "race review", SourceKind: "open.review_plan.v1", SourceDigest: effectport.Hash("machine-race"), Recipients: []aiassistantport.RecipientCandidate{{CustomerID: 31, StaffID: 21, Content: []aiassistantport.ContentBlock{{Kind: aiassistantport.ContentText, Text: "race copy"}}}}, OccurredAt: time.Date(2026, 9, 6, 13, 0, 0, 0, time.UTC)}
	const callers = 8
	start := make(chan struct{})
	results := make(chan aiassistantport.MachineCreatePlanResult, callers)
	errs := make(chan error, callers)
	var wait sync.WaitGroup
	for range callers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			result, createErr := service.CreateMachinePlan(context.Background(), command)
			if createErr != nil {
				errs <- createErr
				return
			}
			results <- result
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	close(errs)
	for createErr := range errs {
		t.Fatalf("concurrent machine create: %v", createErr)
	}
	var id aiassistantport.PlanID
	created, replayed := 0, 0
	for result := range results {
		if id == 0 {
			id = result.Plan.ID
		}
		if result.Plan.ID != id {
			t.Fatalf("different plan IDs under same-client replay: got=%d want=%d", result.Plan.ID, id)
		}
		if result.Replayed {
			replayed++
		} else {
			created++
		}
	}
	if id < 1 || created != 1 || replayed != callers-1 {
		t.Fatalf("race id=%d created=%d replayed=%d", id, created, replayed)
	}
	var plans, receipts int
	if err = native.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM ai_assistant_plans),(SELECT count(*) FROM ai_assistant_operation_receipts WHERE actor_scope=$1 AND operation='machine_plan_create')`, actor.Reference).Scan(&plans, &receipts); err != nil || plans != 1 || receipts != 1 {
		t.Fatalf("race persistence plans=%d receipts=%d err=%v", plans, receipts, err)
	}
}

func assertActorProjectionRejected(t *testing.T, pool *pgxpool.Pool, constraint, query string, args ...any) {
	t.Helper()
	_, err := pool.Exec(context.Background(), query, args...)
	if err == nil || !strings.Contains(err.Error(), constraint) {
		t.Fatalf("constraint %s accepted invalid actor projection: %v", constraint, err)
	}
}

func machineKeyDigest(value string) []byte {
	digest := sha256.Sum256([]byte(value))
	return digest[:]
}

func integrationPool(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	url, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("DATABASE_URL is not configured; skipping AI Assistant PostgreSQL integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var random [8]byte
	if _, err = rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	schema := "aicrm_aiassistant_test_" + hex.EncodeToString(random[:])
	admin, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		admin.Close(ctx)
		t.Fatal(err)
	}
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test")
	}
	for _, name := range []string{"0007_media.sql", "0036_ai_assistant_review.sql", "0100_ai_assistant_machine_actor.sql", "0120_excel_batches.sql", "0124_operation_excel_batch_lifecycle.sql", "0126_media_material_source_snapshots.sql"} {
		migration, readErr := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations", name))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, execErr := pool.Exec(ctx, string(migration)); execErr != nil {
			t.Fatalf("apply migration %s: %v", name, execErr)
		}
	}
	return pool, func() {
		pool.Close()
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_, _ = admin.Exec(cleanup, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close(cleanup)
	}
}
