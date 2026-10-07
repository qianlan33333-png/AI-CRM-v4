package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	aiassistantport "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/port"
	automationapp "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/app"
	automationdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/domain"
	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
	automationprovider "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/provider"
	automationstore "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/store"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformjobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

// This composition contract drives real EER claim and owner completion code.
// The model client has only this in-process RoundTripper: no socket, HTTP
// server, Provider endpoint, paid model or customer-delivery adapter is used.
type sol61ClosureModelTransport struct {
	lateState effectport.State
	entered   chan struct{}
	release   chan struct{}
	calls     atomic.Int32
}

func (m *sol61ClosureModelTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	call := m.calls.Add(1)
	if request.Method != http.MethodPost || request.URL.Host != "synthetic.invalid" || call > 3 {
		return nil, errors.New("unexpected synthetic model request")
	}
	body, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, err
	}
	var input struct {
		Messages []struct{ Role, Content string }
	}
	if json.Unmarshal(body, &input) != nil || len(input.Messages) != 2 || !strings.Contains(input.Messages[1].Content, fmt.Sprintf("【用户名】\n冻结姓名%d", call)) {
		return nil, errors.New("model did not receive the original frozen dispatch")
	}
	if call == 3 {
		close(m.entered)
		select {
		case <-m.release:
		case <-request.Context().Done():
			return nil, request.Context().Err()
		}
	}
	if call == 2 || call == 3 && m.lateState == effectport.StateUnknown {
		return nil, errors.New("synthetic model response lost after request acceptance")
	}
	text := "已落盘的本地合成建议"
	if call == 3 {
		text = "晚到的本地合成建议"
	}
	output, err := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": text}}}})
	if err != nil {
		return nil, err
	}
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(output))), Request: request}, nil
}

// Cancellation and completion do not read audiences or review plans. A
// promoted nil reader makes an unexpected read fail instead of hiding it.
type sol61ClosureUnusedAudiences struct {
	segmentport.ExecutionConfigurationReader
}
type sol61ClosureUnusedSnapshots struct{ segmentport.SnapshotReader }
type sol61ClosureUnusedContent struct {
	automationport.OutboundPublishedContentReader
}
type sol61ClosureReviewPlans struct {
	aiassistantport.Reader
	calls atomic.Int32
}

func (p *sol61ClosureReviewPlans) CreatePlanWithin(ctx context.Context, _ aiassistantport.CreatePlanCommand) (aiassistantport.CreatePlanResult, error) {
	p.calls.Add(1)
	if _, err := platformpostgres.RequireTransaction(ctx); err != nil {
		return aiassistantport.CreatePlanResult{}, err
	}
	return aiassistantport.CreatePlanResult{}, errors.New("cancelled run requested a new review plan")
}

type sol61ClosureGenerationWriter struct {
	service *automationapp.RuntimeService
	calls   atomic.Int32
}

func (w *sol61ClosureGenerationWriter) CompleteGeneration(ctx context.Context, completion automationport.GenerationCompletion) error {
	if _, err := platformpostgres.RequireTransaction(ctx); err != nil {
		return err
	}
	w.calls.Add(1)
	return w.service.CompleteGeneration(ctx, completion)
}

type sol61ClosureEffectFact struct {
	State, AttemptState, Receipt               string
	Generation, Attempts, Number, Fence, JobID int64
	CallAttempted, CallRecorded                bool
}

func sol61ClosureReadEffect(t *testing.T, ctx context.Context, pool *pgxpool.Pool, effectID string) sol61ClosureEffectFact {
	t.Helper()
	id, err := strconv.ParseInt(strings.TrimPrefix(effectID, "eer_"), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	var fact sol61ClosureEffectFact
	err = pool.QueryRow(ctx, `SELECT e.state,e.generation,e.attempt_count,a.state,a.number,a.fence,a.call_attempted,a.real_external_call_executed,COALESCE(a.receipt_digest,''),j.river_job_id FROM external_effects e JOIN external_effect_attempts a ON a.effect_id=e.id AND a.generation=e.generation AND a.number=1 JOIN external_effect_jobs j ON j.effect_id=e.id AND j.generation=e.generation WHERE e.id=$1`, id).Scan(&fact.State, &fact.Generation, &fact.Attempts, &fact.AttemptState, &fact.Number, &fact.Fence, &fact.CallAttempted, &fact.CallRecorded, &fact.Receipt, &fact.JobID)
	if err != nil {
		t.Fatal(err)
	}
	return fact
}

func sol61ClosureReadGeneration(t *testing.T, ctx context.Context, uow *platformpostgres.UnitOfWork, owner *automationstore.Repository, effectID string) automationdomain.GenerationItem {
	t.Helper()
	var item automationdomain.GenerationItem
	if err := uow.Within(ctx, func(tx context.Context) error {
		var err error
		item, err = owner.GenerationByEffect(tx, effectID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return item
}

func TestSol61PostgreSQLInflightGenerationCancelRetainsFactsWithoutReviewPlan(t *testing.T) {
	for _, lateState := range []effectport.State{effectport.StateExecuted, effectport.StateUnknown} {
		t.Run(string(lateState), func(t *testing.T) {
			pool := sol61ClosureAutomationPool(t)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			wrapped, err := platformpostgres.Wrap(pool, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer wrapped.Close()
			uow, err := platformpostgres.NewUnitOfWork(wrapped)
			if err != nil {
				t.Fatal(err)
			}
			owner, err := automationstore.NewPostgreSQL(pool, uow)
			if err != nil {
				t.Fatal(err)
			}
			service, err := automationapp.NewRuntimeService(uow, owner, sol61ClosureUnusedAudiences{}, sol61ClosureUnusedSnapshots{}, 100)
			if err != nil {
				t.Fatal(err)
			}
			plans := &sol61ClosureReviewPlans{}
			if err = service.SetReviewPlanIntake(plans, sol61ClosureUnusedContent{}); err != nil {
				t.Fatal(err)
			}
			transport := &sol61ClosureModelTransport{lateState: lateState, entered: make(chan struct{}), release: make(chan struct{})}
			config := automationprovider.GenerationConfig{Enabled: true, BaseURL: "https://synthetic.invalid", APIKey: "synthetic-not-a-real-key", Model: "local-contract", Timeout: 10 * time.Second, Client: &http.Client{Transport: transport}}
			policy, err := config.Policy()
			if err != nil {
				t.Fatal(err)
			}
			provider, err := automationprovider.NewGenerationProvider(config, service)
			if err != nil {
				t.Fatal(err)
			}
			workers := river.NewWorkers()
			if err = river.AddWorkerSafely[externaleffects.EffectJobArgs](workers, externaleffects.NewWorker(nil, provider)); err != nil {
				t.Fatal(err)
			}
			client, err := platformjobqueue.NewInsertClient(pool, workers)
			if err != nil {
				t.Fatal(err)
			}
			effects, err := externaleffects.NewRepository(pool, client)
			if err != nil {
				t.Fatal(err)
			}
			writer := &sol61ClosureGenerationWriter{service: service}
			sink, err := automationprovider.NewGenerationCompletionSink(writer)
			if err != nil {
				t.Fatal(err)
			}
			if err = effects.SetCompletionSink(sink); err != nil {
				t.Fatal(err)
			}

			now := time.Now().UTC()
			run := automationdomain.RuntimeRun{PackageID: 1, PackageVersion: 1, SnapshotID: 1, AgentID: 1, AgentPublishedVersion: 1, BindingVersion: 1, SenderSetVersion: 1, PreviewDigest: sha256.Sum256([]byte("closure-inflight-preview")), State: automationport.RunPreparing, TargetCount: 3, CreatedBy: 7, CreatedAt: now, UpdatedAt: now}
			items := make([]automationdomain.GenerationItem, 3)
			projections := make([]effectport.Projection, 3)
			if err = uow.Within(ctx, func(tx context.Context) error {
				var createErr error
				run, _, createErr = owner.CreateRun(tx, run, nil)
				if createErr != nil {
					return createErr
				}
				for index := range items {
					digest := func(kind string) [32]byte {
						return sha256.Sum256([]byte(fmt.Sprintf("closure-%s-%d-%d", kind, run.ID, index)))
					}
					items[index] = automationdomain.GenerationItem{RunID: run.ID, CustomerID: int64(1001 + index), SenderStaffID: 7, AgentID: 1, AgentPublishedVersion: 1, AgentCode: "closure_dynamic", RolePrompt: "给出简洁建议", TaskPrompt: "结合冻结上下文生成一条消息", Context: automationport.GenerationContext{CustomerName: fmt.Sprintf("冻结姓名%d", index+1), Questionnaire: "目标增长", RecentChats: "本地合成上下文", Tags: "合成标签", Activation: "activated"}, ModelPolicy: policy, SourceDigest: digest("source"), TargetDigest: digest("target"), PayloadDigest: digest("payload"), PolicyDigest: digest("policy"), ReceiptKeyDigest: digest("receipt"), State: "accepted", CreatedAt: now, UpdatedAt: now}
				}
				items, createErr = owner.CreateGenerationItems(tx, items)
				if createErr != nil {
					return createErr
				}
				for index := range items {
					item := &items[index]
					digest := func(kind string, value [32]byte) effectport.Digest {
						return effectport.Hash("automation.dynamic-text."+kind, hex.EncodeToString(value[:]))
					}
					envelope := effectport.Envelope{Owner: effectport.OwnerAutomation, Kind: effectport.KindAIAgentGenerate, SourceRefDigest: digest("source", item.SourceDigest), TargetRefDigest: digest("target", item.TargetDigest), PayloadDigest: digest("payload", item.PayloadDigest), PolicyVersionHash: digest("policy", item.PolicyDigest)}
					projection, receipt, acceptErr := effects.AcceptAndQueueWithin(tx, effectport.AcceptCommand{ReceiptKey: digest("accept", item.ReceiptKeyDigest), Envelope: envelope})
					if acceptErr != nil {
						return acceptErr
					}
					if projection.QueueJobID < 1 || receipt.QueueReceiptID == "" {
						return errors.New("acceptance did not persist its original River binding")
					}
					projections[index], item.EffectID = projection, projection.ID
					if bindErr := owner.BindGenerationEffect(tx, item.ID, item.EffectID, now); bindErr != nil {
						return bindErr
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			execute := func(index int) error {
				id, parseErr := strconv.ParseInt(strings.TrimPrefix(projections[index].ID, "eer_"), 10, 64)
				if parseErr != nil {
					return parseErr
				}
				return effects.RunAttempt(ctx, id, 1, projections[index].QueueJobID, provider)
			}
			for index := range 2 {
				if err = execute(index); err != nil {
					t.Fatal(err)
				}
			}
			historyItems := []automationdomain.GenerationItem{sol61ClosureReadGeneration(t, ctx, uow, owner, items[0].EffectID), sol61ClosureReadGeneration(t, ctx, uow, owner, items[1].EffectID)}
			historyEffects := []sol61ClosureEffectFact{sol61ClosureReadEffect(t, ctx, pool, items[0].EffectID), sol61ClosureReadEffect(t, ctx, pool, items[1].EffectID)}
			if historyItems[0].State != "executed" || historyItems[0].GeneratedText != "已落盘的本地合成建议" || historyItems[0].ResultDigest == ([32]byte{}) || historyItems[1].State != "outcome_unknown" || historyItems[1].AttemptCount != 1 || plans.calls.Load() != 0 {
				t.Fatal("historical success/unknown controls were not actually settled before the blocked model request")
			}

			done := make(chan error, 1)
			go func() { done <- execute(2) }()
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(transport.release) }) }
			finished := false
			defer func() {
				release()
				if !finished {
					cancel()
					select {
					case <-done:
					case <-time.After(5 * time.Second):
						t.Error("blocked generation did not terminate")
					}
				}
			}()
			select {
			case <-transport.entered:
			case err = <-done:
				finished = true
				t.Fatalf("generation completed before the blocking request: %v", err)
			case <-ctx.Done():
				t.Fatal("model request was never reached")
			}
			inflight := sol61ClosureReadEffect(t, ctx, pool, items[2].EffectID)
			if inflight.State != "attempted" || inflight.AttemptState != "attempted" || inflight.Attempts != 1 || inflight.Number != 1 || inflight.Generation != 1 || inflight.JobID != projections[2].QueueJobID || transport.calls.Load() != 3 {
				t.Fatalf("no actual accepted/inflight EER call: %+v requests=%d", inflight, transport.calls.Load())
			}
			cancelled, err := service.CancelRun(ctx, run.ID, 7, "sol61-closure-cancel-inflight-0001")
			if err != nil || cancelled.State != automationport.RunCancelled {
				t.Fatalf("cancel did not commit before model release: state=%s err=%v", cancelled.State, err)
			}
			if afterCancel := sol61ClosureReadEffect(t, ctx, pool, items[2].EffectID); afterCancel != inflight {
				t.Fatalf("owner cancellation rewrote the inflight EER attempt: before=%+v after=%+v", inflight, afterCancel)
			}
			release()
			select {
			case err = <-done:
				finished = true
				if err != nil {
					t.Fatalf("late completion failed to settle the original EER: %v", err)
				}
			case <-ctx.Done():
				t.Fatal("late completion did not settle")
			}

			finalEffect := sol61ClosureReadEffect(t, ctx, pool, items[2].EffectID)
			if finalEffect.State != string(lateState) || finalEffect.AttemptState != string(lateState) || finalEffect.Generation != inflight.Generation || finalEffect.Number != inflight.Number || finalEffect.Fence != inflight.Fence || finalEffect.JobID != inflight.JobID || !finalEffect.CallAttempted || !finalEffect.CallRecorded || !effectport.ValidDigest(effectport.Digest(finalEffect.Receipt)) {
				t.Fatalf("late result lost original EER call evidence: %+v", finalEffect)
			}
			lateItem := sol61ClosureReadGeneration(t, ctx, uow, owner, items[2].EffectID)
			// This is the existing cancelled-owner semantics: new late text is
			// excluded; owner attempt_count=0 does not deny the EER call above.
			if lateItem.State != "cancelled" || lateItem.GeneratedText != "" || lateItem.AttemptCount != 0 {
				t.Fatalf("late result changed a cancelled generation item: state=%s attempts=%d text_present=%t", lateItem.State, lateItem.AttemptCount, lateItem.GeneratedText != "")
			}
			var finalRun automationdomain.RuntimeRun
			if err = uow.Within(ctx, func(tx context.Context) error {
				var readErr error
				finalRun, readErr = owner.Run(tx, run.ID)
				return readErr
			}); err != nil {
				t.Fatal(err)
			}
			if finalRun.State != automationport.RunCancelled || finalRun.AIPlanID != 0 || plans.calls.Load() != 0 {
				t.Fatalf("late model result revived a cancelled run or requested review: run=%s plan_id=%d intake_calls=%d", finalRun.State, finalRun.AIPlanID, plans.calls.Load())
			}
			for index := range 2 {
				if after := sol61ClosureReadGeneration(t, ctx, uow, owner, items[index].EffectID); !reflect.DeepEqual(historyItems[index], after) {
					t.Fatalf("cancellation changed historical generation facts for item %d", index)
				}
				if after := sol61ClosureReadEffect(t, ctx, pool, items[index].EffectID); historyEffects[index] != after {
					t.Fatalf("cancellation changed historical EER facts for item %d", index)
				}
			}
			if err = execute(2); err != nil {
				t.Fatal(err)
			}
			var attempts, generations, jobs int
			if err = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM external_effect_attempts),(SELECT count(*) FROM external_effect_generations),(SELECT count(*) FROM external_effect_jobs)`).Scan(&attempts, &generations, &jobs); err != nil {
				t.Fatal(err)
			}
			if transport.calls.Load() != 3 || writer.calls.Load() != 3 || attempts != 3 || generations != 3 || jobs != 3 || plans.calls.Load() != 0 {
				t.Fatalf("late completion/replay produced additional calls or work: requests=%d completions=%d attempts=%d generations=%d jobs=%d plans=%d", transport.calls.Load(), writer.calls.Load(), attempts, generations, jobs, plans.calls.Load())
			}
			t.Logf("late=%s cancelled_run=%s review_intake=0 historical_text_digest_and_unknown_unchanged=true original_eer_attempt=1 generation=1 call_recorded=true synthetic_requests=3 replay_requests=0", lateState, finalRun.State)
		})
	}
}

func sol61ClosureAutomationPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Fatal("closure contract requires the authorized isolated PostgreSQL URL")
	}
	baseConfig, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	if baseConfig.ConnConfig.Host != "127.0.0.1" || (!strings.HasSuffix(baseConfig.ConnConfig.Database, "_acceptance_test") && baseConfig.ConnConfig.Database != "aicrm_test_sol61_async") || len(baseConfig.ConnConfig.Fallbacks) != 0 {
		t.Fatal("Automation closure requires the authorized dedicated loopback test database with no fallback")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := pgx.ConnectConfig(ctx, baseConfig.ConnConfig.Copy())
	if err != nil {
		t.Fatal(err)
	}
	var random [8]byte
	if _, err = rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	schema := "aicrm_sol61_automation_inflight_" + hex.EncodeToString(random[:])
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	config := baseConfig.Copy()
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = admin.Exec(cleanup, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		_ = admin.Close(cleanup)
	})
	migrator, err := rivermigrate.New(riverpgxv5.New(pool), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		t.Fatal(err)
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate closure migrations")
	}
	// Production table constraints are retained. The generic immutable-fact
	// trigger helper is qualified to this private schema; no shared public
	// function or payment/order/refund schema is written by this fixture.
	for _, name := range []string{"0005_external_effects.sql", "0013_automation_agents.sql", "0015_config_adminops.sql", "0043_automation_runtime.sql", "0087_automation_manual_ai_review.sql", "0094_runtime_config_releases.sql", "0115_automation_dynamic_text_generation.sql", "0125_outbound_material_preparation.sql", "0212_automation_prompt_length_unbounded.sql"} {
		migration, readErr := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "migrations", name))
		if readErr != nil {
			t.Fatal(readErr)
		}
		ddl := string(migration)
		if name == "0005_external_effects.sql" {
			if strings.Count(ddl, "public.external_effects_reject_delete") != 6 {
				t.Fatal("unexpected immutable-fact helper migration shape")
			}
			ddl = strings.ReplaceAll(ddl, "public.external_effects_reject_delete", pgx.Identifier{schema, "external_effects_reject_delete"}.Sanitize())
		}
		if strings.Contains(ddl, "public.") {
			t.Fatalf("migration %s escaped the private closure schema", name)
		}
		if _, err = pool.Exec(ctx, ddl); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
	}
	return pool
}
