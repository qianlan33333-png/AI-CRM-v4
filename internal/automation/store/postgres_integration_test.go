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
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	automationapp "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/app"
	automationdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/domain"
	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
)

// automationMediaReader is a test double for Media's stable port. The
// production composition injects Media's repository; Automation never imports
// its concrete store, including from tests.
type automationMediaReader struct{ enabled map[int64]bool }

type automationExecutionReader struct{ packageID segmentport.PackageID }

func (r automationExecutionReader) AudienceExecutionConfiguration(context.Context, segmentport.PackageID) (segmentport.ExecutionConfiguration, error) {
	return segmentport.ExecutionConfiguration{PackageID: r.packageID, Ready: true}, nil
}

type automationSnapshotReader struct{}

func (automationSnapshotReader) PublishedSnapshot(context.Context, segmentport.PackageID) (segmentport.Snapshot, bool, error) {
	return segmentport.Snapshot{}, false, nil
}
func (automationSnapshotReader) Snapshot(context.Context, segmentport.SnapshotID) (segmentport.Snapshot, bool, error) {
	return segmentport.Snapshot{}, false, nil
}
func (automationSnapshotReader) Members(context.Context, segmentport.SnapshotID, string, int) (segmentport.MemberPage, error) {
	return segmentport.MemberPage{}, nil
}

func (r automationMediaReader) ImageExists(_ context.Context, id int64) (bool, error) {
	return r.enabled[id], nil
}
func (r automationMediaReader) AttachmentExists(_ context.Context, id int64) (bool, error) {
	return r.enabled[id], nil
}
func (r automationMediaReader) MiniProgramExists(_ context.Context, id int64) (bool, error) {
	return r.enabled[id], nil
}
func (r automationMediaReader) GroupInviteExists(_ context.Context, id int64) (bool, error) {
	return r.enabled[id], nil
}

func TestPostgreSQLAgentFixedContentPublishActivatePauseJourney(t *testing.T) {
	native, cleanup := automationIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	wrapped, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	automationRepository, err := NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	mediaReader := automationMediaReader{enabled: map[int64]bool{11: true, 12: true, 13: true, 14: true}}
	service := automationapp.NewAgentServiceWithMediaReferences(uow, automationRepository, mediaReader, mediaReader, mediaReader, mediaReader, automationRepository)

	created, err := service.Create(ctx, automationport.CreateCommand{Actor: 7, IdempotencyKey: "automation-pg-create-0001", Agent: automationport.Agent{
		AgentName: "固定欢迎话术", AgentCode: "fixed_welcome", AutomationType: automationport.AutomationTypeFixedScript,
		Status: automationport.AgentStatusPaused, DraftRolePrompt: "保持准确", DraftTaskPrompt: "使用已发布内容",
	}})
	if err != nil {
		t.Fatal(err)
	}
	// The page's high-entropy host suggestion is intentionally not a database
	// reservation. The create command remains the sole concurrency authority:
	// PostgreSQL rejects a duplicate code and rolls back its receipt/audit/outbox.
	if _, err = service.Create(ctx, automationport.CreateCommand{Actor: 7, IdempotencyKey: "automation-pg-create-0002", Agent: automationport.Agent{
		AgentName: "重复编码", AgentCode: created.AgentCode, AutomationType: automationport.AutomationTypeAgent,
		Status: automationport.AgentStatusPaused,
	}}); !errors.Is(err, automationapp.ErrAgentConflict) {
		t.Fatalf("duplicate agent code err=%v", err)
	}
	var conflictAgents, conflictReceipts, conflictAudits, conflictOutbox int
	if err = native.QueryRow(ctx, `SELECT (SELECT count(*) FROM automation_agents),(SELECT count(*) FROM automation_operation_receipts),(SELECT count(*) FROM automation_audit_events),(SELECT count(*) FROM automation_outbox)`).Scan(&conflictAgents, &conflictReceipts, &conflictAudits, &conflictOutbox); err != nil {
		t.Fatal(err)
	}
	if conflictAgents != 1 || conflictReceipts != 1 || conflictAudits != 1 || conflictOutbox != 1 {
		t.Fatalf("duplicate create was not fully rolled back agents=%d receipts=%d audits=%d outbox=%d", conflictAgents, conflictReceipts, conflictAudits, conflictOutbox)
	}
	content := automationport.FixedContentPackage{ContentText: "欢迎加入", ImageLibraryIDs: []int64{11}, AttachmentLibraryIDs: []int64{12}, MiniprogramLibraryIDs: []int64{13}, GroupInviteLibraryIDs: []int64{14}}
	saved, err := service.SaveFixedContent(ctx, automationport.FixedContentCommand{ID: created.ID, ContentPackage: content, Actor: 7, IdempotencyKey: "automation-pg-content-01"})
	if err != nil || saved.DraftVersion != 2 || saved.PublishedVersion != 1 {
		t.Fatalf("saved=%+v err=%v", saved, err)
	}
	if _, err = service.SetStatus(ctx, automationport.MutationCommand{ID: created.ID, Actor: 7, IdempotencyKey: "automation-pg-active-01"}, automationport.AgentStatusActive); !errors.Is(err, automationapp.ErrAgentConflict) {
		t.Fatalf("activation before publish err=%v", err)
	}
	published, err := service.Publish(ctx, automationport.MutationCommand{ID: created.ID, Actor: 7, IdempotencyKey: "automation-pg-publish-01"})
	if err != nil || published.DraftVersion != published.PublishedVersion {
		t.Fatalf("published=%+v err=%v", published, err)
	}
	active, err := service.SetStatus(ctx, automationport.MutationCommand{ID: created.ID, Actor: 7, IdempotencyKey: "automation-pg-active-02"}, automationport.AgentStatusActive)
	if err != nil || active.Status != automationport.AgentStatusActive || !active.ExecutionEnabled {
		t.Fatalf("active=%+v err=%v", active, err)
	}
	paused, err := service.SetStatus(ctx, automationport.MutationCommand{ID: created.ID, Actor: 7, IdempotencyKey: "automation-pg-pause-0001"}, automationport.AgentStatusPaused)
	if err != nil || paused.Status != automationport.AgentStatusPaused || paused.ExecutionEnabled {
		t.Fatalf("paused=%+v err=%v", paused, err)
	}
	replayed, err := service.SaveFixedContent(ctx, automationport.FixedContentCommand{ID: created.ID, ContentPackage: content, Actor: 7, IdempotencyKey: "automation-pg-content-01"})
	if err != nil || replayed.ID != saved.ID || replayed.DraftVersion != saved.DraftVersion {
		t.Fatalf("replay=%+v err=%v", replayed, err)
	}
	if _, err = service.SaveFixedContent(ctx, automationport.FixedContentCommand{ID: created.ID, ContentPackage: automationport.FixedContentPackage{ContentText: "drift", ImageLibraryIDs: []int64{11}}, Actor: 7, IdempotencyKey: "automation-pg-content-01"}); !errors.Is(err, automationapp.ErrAgentConflict) {
		t.Fatalf("payload drift err=%v", err)
	}
	got, err := service.Get(ctx, created.ID)
	if err != nil || len(got.FixedContentPackage.ImageLibraryIDs) != 1 || got.FixedContentPackage.ImageLibraryIDs[0] != 11 || len(got.FixedContentPackage.AttachmentLibraryIDs) != 1 || got.FixedContentPackage.AttachmentLibraryIDs[0] != 12 || len(got.FixedContentPackage.MiniprogramLibraryIDs) != 1 || got.FixedContentPackage.MiniprogramLibraryIDs[0] != 13 || len(got.FixedContentPackage.GroupInviteLibraryIDs) != 1 || got.FixedContentPackage.GroupInviteLibraryIDs[0] != 14 {
		t.Fatalf("read=%+v err=%v", got, err)
	}
	var agents, receipts, audits, outbox int
	if err = native.QueryRow(ctx, `SELECT (SELECT count(*) FROM automation_agents),(SELECT count(*) FROM automation_operation_receipts),(SELECT count(*) FROM automation_audit_events),(SELECT count(*) FROM automation_outbox)`).Scan(&agents, &receipts, &audits, &outbox); err != nil {
		t.Fatal(err)
	}
	if agents != 1 || receipts != 5 || audits != 5 || outbox != 5 {
		t.Fatalf("atomic persisted facts agents=%d receipts=%d audits=%d outbox=%d", agents, receipts, audits, outbox)
	}
	if _, err = native.Exec(ctx, `UPDATE automation_audit_events SET operation=operation`); err == nil {
		t.Fatal("automation audit unexpectedly mutable")
	}
}

func TestPostgreSQLPolicyCreateVersionLifecycleAndReplayJourney(t *testing.T) {
	native, cleanup := automationRuntimeIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
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
	packageID := segmentport.PackageID(17)
	service, err := automationapp.NewRuntimeService(uow, repository, automationExecutionReader{packageID: packageID}, automationSnapshotReader{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	approval := int64(7)
	command := automationapp.PolicyCommand{Code: "pg-lifecycle", Name: "PostgreSQL lifecycle", PackageID: packageID, TriggerKind: automationport.TriggerAudienceMemberEnteredV1, ActionKind: automationport.ActionRecord, ActionConfig: json.RawMessage(`{"record_type":"entry"}`), QuietHours: json.RawMessage(`{"timezone":"UTC","start":"22:00","end":"08:00"}`), SingleRunLimit: 10, ApprovalStaffID: &approval, Actor: 7, IdempotencyKey: "policy-postgres-create-0001"}
	created, err := service.CreatePolicy(ctx, command)
	if err != nil || created.Version != 2 || created.Lifecycle != automationdomain.PolicyPaused {
		t.Fatalf("created=%+v err=%v", created, err)
	}
	versionCommand := command
	versionCommand.PolicyID, versionCommand.ExpectedVersion, versionCommand.IdempotencyKey = created.ID, created.Version, "policy-postgres-version-0001"
	versioned, err := service.PutPolicyVersion(ctx, versionCommand)
	if err != nil || versioned.Version != 2 {
		t.Fatalf("versioned=%+v err=%v", versioned, err)
	}
	// Keep the direct Store probe transaction-only: on a PostgreSQL regression
	// it identifies the failing lifecycle write without leaving a test row, and
	// normal behavior is still verified by the real transitions below.
	probeRollback := errors.New("rollback policy lifecycle probe")
	probeErr := uow.Within(ctx, func(tx context.Context) error {
		current, e := repository.LockPolicy(tx, created.ID)
		if e != nil {
			return e
		}
		if _, e = repository.SetPolicyLifecycle(tx, current.ID, current.Version, 7, automationdomain.PolicyActive, time.Date(2026, 9, 5, 20, 0, 0, 0, time.UTC)); e != nil {
			return e
		}
		return probeRollback
	})
	if !errors.Is(probeErr, probeRollback) {
		t.Fatalf("policy lifecycle rollback probe: %v", probeErr)
	}
	active, err := service.TransitionPolicy(ctx, automationapp.PolicyLifecycleCommand{PolicyID: created.ID, ExpectedVersion: 3, Actor: 7, Target: automationdomain.PolicyActive, IdempotencyKey: "policy-postgres-active-0001"})
	if err != nil || active.Lifecycle != automationdomain.PolicyActive || active.Version != 4 {
		t.Fatalf("active=%+v err=%v", active, err)
	}
	replayed, err := service.TransitionPolicy(ctx, automationapp.PolicyLifecycleCommand{PolicyID: created.ID, ExpectedVersion: 3, Actor: 7, Target: automationdomain.PolicyActive, IdempotencyKey: "policy-postgres-active-0001"})
	if err != nil || replayed.ID != active.ID || replayed.Version != active.Version {
		t.Fatalf("active replay=%+v err=%v", replayed, err)
	}
	paused, err := service.TransitionPolicy(ctx, automationapp.PolicyLifecycleCommand{PolicyID: created.ID, ExpectedVersion: active.Version, Actor: 7, Target: automationdomain.PolicyPaused, IdempotencyKey: "policy-postgres-pause-0001"})
	if err != nil || paused.Lifecycle != automationdomain.PolicyPaused || paused.Version != 5 {
		t.Fatalf("paused=%+v err=%v", paused, err)
	}
	archived, err := service.TransitionPolicy(ctx, automationapp.PolicyLifecycleCommand{PolicyID: created.ID, ExpectedVersion: paused.Version, Actor: 7, Target: automationdomain.PolicyArchived, IdempotencyKey: "policy-postgres-archive-0001"})
	if err != nil || archived.Lifecycle != automationdomain.PolicyArchived || archived.ArchivedAt == nil || archived.Version != 6 {
		t.Fatalf("archived=%+v err=%v", archived, err)
	}
	if _, err = service.TransitionPolicy(ctx, automationapp.PolicyLifecycleCommand{PolicyID: created.ID, ExpectedVersion: archived.Version, Actor: 7, Target: automationdomain.PolicyArchived, IdempotencyKey: "policy-postgres-archive-0002"}); !errors.Is(err, automationapp.ErrRuntimeConflict) {
		t.Fatalf("second archive err=%v", err)
	}
}

func TestPostgreSQLRuntimeConfigObservedShapeRejectsIncompleteSnapshot(t *testing.T) {
	native, cleanup := automationRuntimeIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	preview := `INSERT INTO automation_run_previews(
		package_id,package_version,snapshot_id,configuration_version_id,agent_id,agent_published_version,binding_version,sender_set_version,
		target_count,skipped_count,preview_digest,created_by,created_at,expires_at,
		runtime_config_observed,runtime_config_revision,max_recipients_per_run
	) VALUES(1,1,1,1,1,1,1,1,0,0,decode(repeat('00',32),'hex'),1,clock_timestamp(),clock_timestamp()+interval '5 minutes',TRUE,NULL,NULL)`
	if _, err := native.Exec(ctx, preview); err == nil {
		t.Fatal("observed preview without a complete runtime snapshot was accepted")
	}
	run := `INSERT INTO automation_runs(
		policy_id,policy_version,package_id,package_version,snapshot_id,agent_id,agent_published_version,binding_version,sender_set_version,
		preview_digest,state,target_count,skipped_count,created_by,created_at,updated_at,
		runtime_config_observed,runtime_config_revision,max_recipients_per_run
	) VALUES(NULL,NULL,1,1,1,1,1,1,1,decode(repeat('00',32),'hex'),'ready',0,0,1,clock_timestamp(),clock_timestamp(),TRUE,NULL,NULL)`
	if _, err := native.Exec(ctx, run); err == nil {
		t.Fatal("observed run without a complete runtime snapshot was accepted")
	}
}

// OneID decision: these owner rows retain opaque customer IDs and do not
// resolve or provision identities. Persistence decision: item creation,
// effect binding and terminal aggregation are each exercised in PostgreSQL
// UoWs; the concurrent final completions prove the run lock admits one plan
// creation decision only. Provider decision: no provider call is made here.
func TestPostgreSQLDynamicGenerationUOWConcurrencyAndReplay(t *testing.T) {
	native, cleanup := automationRuntimeIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
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
	// 0115 must admit Automation's single generation envelope in the shared
	// EER registry. It deliberately does not open any outbound delivery kind.
	var generationEffectID int64
	err = native.QueryRow(ctx, `INSERT INTO external_effects(owner,kind,source_ref_digest,target_ref_digest,payload_digest,policy_version_hash,envelope_fingerprint,state) VALUES('automation','ai_agent_generate',$1,$2,$3,$4,$5,'queued') RETURNING id`, effectport.Hash("generation-eer", "source"), effectport.Hash("generation-eer", "target"), effectport.Hash("generation-eer", "payload"), effectport.Hash("generation-eer", "policy"), effectport.Hash("generation-eer", "envelope")).Scan(&generationEffectID)
	if err != nil || generationEffectID < 1 {
		t.Fatalf("automation generation effect constraint=%d err=%v", generationEffectID, err)
	}
	var tagMutationEffectID int64
	err = native.QueryRow(ctx, `INSERT INTO external_effects(owner,kind,source_ref_digest,target_ref_digest,payload_digest,policy_version_hash,envelope_fingerprint,state) VALUES('outbound','wecom_tag_catalog_mutation',$1,$2,$3,$4,$5,'queued') RETURNING id`, effectport.Hash("tag-mutation-eer", "source"), effectport.Hash("tag-mutation-eer", "target"), effectport.Hash("tag-mutation-eer", "payload"), effectport.Hash("tag-mutation-eer", "policy"), effectport.Hash("tag-mutation-eer", "envelope")).Scan(&tagMutationEffectID)
	if err != nil || tagMutationEffectID < 1 {
		t.Fatalf("0115 narrowed tag mutation effect constraint=%d err=%v", tagMutationEffectID, err)
	}
	runID := insertGenerationTestRun(t, native)
	items := []automationdomain.GenerationItem{generationTestItem(runID, 1001, "eer_1"), generationTestItem(runID, 1002, "eer_2")}
	rollback := errors.New("rollback dynamic generation item creation")
	if err = uow.Within(ctx, func(tx context.Context) error {
		created, createErr := repository.CreateGenerationItems(tx, items)
		if createErr != nil {
			return createErr
		}
		for index := range created {
			if bindErr := repository.BindGenerationEffect(tx, created[index].ID, items[index].EffectID, time.Date(2026, 9, 8, 8, 0, 0, 0, time.UTC)); bindErr != nil {
				return bindErr
			}
		}
		return rollback
	}); !errors.Is(err, rollback) {
		t.Fatalf("rollback err=%v", err)
	}
	var count int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM automation_generation_items WHERE run_id=$1`, runID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rolled-back items=%d err=%v", count, err)
	}
	if err = uow.Within(ctx, func(tx context.Context) error {
		created, createErr := repository.CreateGenerationItems(tx, items)
		if createErr != nil {
			return createErr
		}
		for index := range created {
			if bindErr := repository.BindGenerationEffect(tx, created[index].ID, items[index].EffectID, time.Date(2026, 9, 8, 8, 0, 0, 0, time.UTC)); bindErr != nil {
				return bindErr
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	results := make(chan bool, 2)
	errorsByCompletion := make(chan error, 2)
	var wait sync.WaitGroup
	complete := func(effectID string, state effectport.State, body string) {
		defer wait.Done()
		<-start
		completion := generationTestCompletion(effectID, state, body)
		err := uow.Within(ctx, func(tx context.Context) error {
			_, _, createPlan, settleErr := repository.SettleGeneration(tx, completion)
			if settleErr == nil {
				results <- createPlan
			}
			return settleErr
		})
		errorsByCompletion <- err
	}
	wait.Add(2)
	go complete("eer_1", effectport.StateExecuted, "为你准备的专属建议")
	go complete("eer_2", effectport.StateUnknown, "")
	close(start)
	wait.Wait()
	close(results)
	close(errorsByCompletion)
	for completeErr := range errorsByCompletion {
		if completeErr != nil {
			t.Fatal(completeErr)
		}
	}
	planDecisions := 0
	for decision := range results {
		if decision {
			planDecisions++
		}
	}
	if planDecisions != 1 {
		t.Fatalf("plan decisions=%d, want exactly one", planDecisions)
	}
	var progress automationport.GenerationProgress
	err = uow.Within(ctx, func(tx context.Context) error {
		var progressErr error
		progress, progressErr = repository.GenerationProgress(tx, runID)
		return progressErr
	})
	if err != nil || progress.Total != 2 || progress.Succeeded != 1 || progress.Unknown != 1 || progress.Queued != 0 || progress.Failed != 0 {
		t.Fatalf("progress=%+v err=%v", progress, err)
	}
	var planItems []automationdomain.GenerationItem
	err = uow.Within(ctx, func(tx context.Context) error {
		var itemErr error
		planItems, itemErr = repository.GenerationItemsForPlan(tx, runID)
		return itemErr
	})
	if err != nil || len(planItems) != 1 || planItems[0].GeneratedText != "为你准备的专属建议" {
		t.Fatalf("plan items=%+v err=%v", planItems, err)
	}
	if err = uow.Within(ctx, func(tx context.Context) error {
		_, _, createPlan, settleErr := repository.SettleGeneration(tx, generationTestCompletion("eer_1", effectport.StateExecuted, "为你准备的专属建议"))
		if settleErr != nil {
			return settleErr
		}
		if createPlan {
			return errors.New("terminal generation replay requested a second review plan")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func insertGenerationTestRun(t *testing.T, native *pgxpool.Pool) int64 {
	t.Helper()
	var id int64
	err := native.QueryRow(context.Background(), `INSERT INTO automation_runs(package_id,package_version,snapshot_id,agent_id,agent_published_version,binding_version,sender_set_version,preview_digest,state,target_count,skipped_count,created_by,created_at,updated_at) VALUES(1,1,1,1,1,1,1,decode(repeat('1a',32),'hex'),'preparing',2,0,7,clock_timestamp(),clock_timestamp()) RETURNING id`).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func generationTestItem(runID, customerID int64, effectID string) automationdomain.GenerationItem {
	now := time.Date(2026, 9, 8, 8, 0, 0, 0, time.UTC)
	digest := func(namespace string) [32]byte { return sha256.Sum256([]byte(namespace + effectID)) }
	return automationdomain.GenerationItem{RunID: runID, CustomerID: customerID, SenderStaffID: 7, AgentID: 1, AgentPublishedVersion: 1, AgentCode: "dynamic_text", RolePrompt: "给出简洁建议", TaskPrompt: "结合冻结上下文生成一条消息", Context: automationport.GenerationContext{Questionnaire: "目标：增长", RecentChats: "想了解", Tags: "活跃", Activation: "activated"}, ModelPolicy: automationport.GenerationModelPolicy{Mode: "enabled", Endpoint: "https://model.example.test/chat/completions", Model: "test-model", Temperature: 0.4}, SourceDigest: digest("source"), TargetDigest: digest("target"), PayloadDigest: digest("payload"), PolicyDigest: digest("policy"), ReceiptKeyDigest: digest("receipt"), State: "accepted", CreatedAt: now, UpdatedAt: now, EffectID: effectID}
}

func generationTestCompletion(effectID string, state effectport.State, body string) automationport.GenerationCompletion {
	completion := automationport.GenerationCompletion{EffectID: effectID, State: state, Attempt: effectport.Attempt{EffectID: effectID, Number: 1, Generation: 1, Fence: 1}, ReceiptDigest: effectport.Hash("generation-test-receipt", effectID), CompletedAt: time.Date(2026, 9, 8, 8, 1, 0, 0, time.UTC), FailureCode: "generation_call_unknown"}
	if state == effectport.StateExecuted {
		completion.Artifact = effectport.ResultArtifact{Kind: "automation.ai_agent_generate.text.v1", Payload: []byte(body)}
		completion.Artifact.Digest = effectport.Hash("external-effect.artifact.v1", completion.Artifact.Kind, body)
	}
	return completion
}

func automationIntegrationPool(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	url, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("DATABASE_URL is not configured; skipping automation PostgreSQL integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var random [8]byte
	if _, err = rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	schema := "aicrm_automation_test_" + hex.EncodeToString(random[:])
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
	for _, name := range []string{"0013_automation_agents.sql"} {
		migration, readErr := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations", name))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, execErr := pool.Exec(ctx, string(migration)); execErr != nil {
			t.Fatalf("apply %s: %v", name, execErr)
		}
	}
	return pool, func() {
		pool.Close()
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = admin.Exec(cleanup, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close(cleanup)
	}
}

func automationRuntimeIntegrationPool(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	pool, cleanup := automationIntegrationPoolWithMigrations(t, []string{"0005_external_effects.sql", "0013_automation_agents.sql", "0043_automation_runtime.sql", "0087_automation_manual_ai_review.sql", "0015_config_adminops.sql", "0094_runtime_config_releases.sql", "0115_automation_dynamic_text_generation.sql"})
	return pool, cleanup
}

func automationIntegrationPoolWithMigrations(t *testing.T, migrations []string) (*pgxpool.Pool, func()) {
	t.Helper()
	url, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("DATABASE_URL is not configured; skipping automation PostgreSQL integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var random [8]byte
	if _, err = rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	schema := "aicrm_automation_runtime_test_" + hex.EncodeToString(random[:])
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
	for _, name := range migrations {
		migration, readErr := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations", name))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, execErr := pool.Exec(ctx, string(migration)); execErr != nil {
			t.Fatalf("apply %s: %v", name, execErr)
		}
	}
	return pool, func() {
		pool.Close()
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = admin.Exec(cleanup, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close(cleanup)
	}
}
