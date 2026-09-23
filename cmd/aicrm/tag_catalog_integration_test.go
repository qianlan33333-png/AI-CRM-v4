package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
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
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	effects "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/outbound"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformjobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	surveyapp "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/app"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
	surveysecure "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/secure"
	surveystore "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/store"
	tagport "github.com/qianlan33333-png/AI-CRM-v3/internal/tag/port"
	tagstore "github.com/qianlan33333-png/AI-CRM-v3/internal/tag/store"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

type integrationCatalogReader func(context.Context) (outbound.CatalogSnapshot, error)

func (f integrationCatalogReader) ListCatalog(ctx context.Context) (outbound.CatalogSnapshot, error) {
	return f(ctx)
}

type integrationAdapter func(context.Context, effectport.Envelope, effectport.Attempt) (effectport.AdapterResult, error)

// TestPostgreSQLSurveyCompletionKindRegistryAfterWelcomeQueueMigration proves
// the 0075 External Effects extension against the complete production
// migration sequence.  0066 belongs to Channel and is intentionally not
// copied into this branch; its queue constraint is applied here exactly as
// that preceding migration defines it before the registry is exercised.
func TestPostgreSQLSurveyCompletionKindRegistryAfterWelcomeQueueMigration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	if _, err = pool.Exec(ctx, `
		ALTER TABLE external_effect_jobs DROP CONSTRAINT IF EXISTS external_effect_jobs_queue_check;
		ALTER TABLE external_effect_jobs ADD CONSTRAINT external_effect_jobs_queue_check CHECK(queue IN ('outbound','outbound_welcome'));
	`); err != nil {
		t.Fatalf("apply 0066 queue predecessor: %v", err)
	}
	var queueConstraint string
	if err = pool.QueryRow(ctx, `SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid='external_effect_jobs'::regclass AND conname='external_effect_jobs_queue_check'`).Scan(&queueConstraint); err != nil || !strings.Contains(queueConstraint, "outbound_welcome") || !strings.Contains(queueConstraint, "outbound") {
		t.Fatalf("welcome queue constraint=%q err=%v", queueConstraint, err)
	}

	valid := []struct{ owner, kind string }{
		{"outbound", "survey_completion"},
		{"outbound", "channel_welcome_message"},
		{"payment", "wechat_pay_prepay_v1"},
	}
	for index, item := range valid {
		seed := fmt.Sprintf("registry-%d-%s-%s", index, item.owner, item.kind)
		if _, err = pool.Exec(ctx, `INSERT INTO external_effects(owner,kind,source_ref_digest,target_ref_digest,payload_digest,policy_version_hash,envelope_fingerprint,state) VALUES($1,$2,$3,$4,$5,$6,$7,'accepted')`, item.owner, item.kind, effectport.Hash(seed, "source"), effectport.Hash(seed, "target"), effectport.Hash(seed, "payload"), effectport.Hash(seed, "policy"), effectport.Hash(seed, "fingerprint")); err != nil {
			t.Fatalf("valid owner/kind %s/%s rejected: %v", item.owner, item.kind, err)
		}
	}
	for _, item := range []struct{ owner, kind string }{{"payment", "survey_completion"}, {"outbound", "wechat_pay_prepay_v1"}, {"outbound", "not_a_registered_kind"}} {
		seed := "invalid-" + item.owner + "-" + item.kind
		if _, err = pool.Exec(ctx, `INSERT INTO external_effects(owner,kind,source_ref_digest,target_ref_digest,payload_digest,policy_version_hash,envelope_fingerprint,state) VALUES($1,$2,$3,$4,$5,$6,$7,'accepted')`, item.owner, item.kind, effectport.Hash(seed, "source"), effectport.Hash(seed, "target"), effectport.Hash(seed, "payload"), effectport.Hash(seed, "policy"), effectport.Hash(seed, "fingerprint")); err == nil {
			t.Fatalf("invalid owner/kind %s/%s was accepted", item.owner, item.kind)
		}
	}
}

func (f integrationAdapter) Execute(ctx context.Context, e effectport.Envelope, a effectport.Attempt) (effectport.AdapterResult, error) {
	return f(ctx, e, a)
}

func TestPostgreSQLCatalogCompletionEndToEnd(t *testing.T) {
	pool, cleanup := catalogIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	wrapped, err := platformpostgres.Wrap(pool, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	tags, err := tagstore.NewPostgreSQL(pool, uow)
	if err != nil {
		t.Fatal(err)
	}
	sink, err := outbound.NewTagCatalogCompletionSink(tags)
	if err != nil {
		t.Fatal(err)
	}
	workers := river.NewWorkers()
	if err = river.AddWorkerSafely[effects.EffectJobArgs](workers, effects.NewWorker(nil, nil)); err != nil {
		t.Fatal(err)
	}
	client, err := platformjobqueue.NewInsertClient(pool, workers)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := effects.NewRepository(pool, client)
	if err != nil {
		t.Fatal(err)
	}
	if err = repository.SetCompletionSink(sink); err != nil {
		t.Fatal(err)
	}
	provider, _ := outbound.NewTagCatalogProvider(integrationCatalogReader(func(context.Context) (outbound.CatalogSnapshot, error) {
		return outbound.CatalogSnapshot{Groups: []outbound.CatalogGroup{{ID: "g", Name: "group", Tags: []outbound.CatalogTag{{ID: "t", Name: "tag"}}}}}, nil
	}))
	projection, id, job := acceptCatalogEffect(t, ctx, pool, repository, "executed")
	if err = repository.RunAttempt(ctx, id, 1, job, provider); err != nil {
		t.Fatal(err)
	}
	current, err := repository.Get(ctx, projection.ID)
	if err != nil || current.State != effects.StateExecuted {
		t.Fatalf("executed=%+v err=%v", current, err)
	}
	var snapshot string
	if err = pool.QueryRow(ctx, `SELECT snapshot::text FROM tag_provider_observations WHERE effect_id=$1 AND generation=1`, id).Scan(&snapshot); err != nil || snapshot == "" {
		t.Fatalf("observation=%q err=%v", snapshot, err)
	}
	var groupCount, tagCount int
	var receiptState string
	if err = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM tag_groups WHERE archived_at IS NULL),(SELECT count(*) FROM tag_catalog_tags WHERE archived_at IS NULL),state FROM tag_sync_receipts WHERE effect_id=$1`, id).Scan(&groupCount, &tagCount, &receiptState); err != nil || groupCount != 1 || tagCount != 1 || receiptState != "executed" {
		t.Fatalf("catalog projection groups=%d tags=%d receipt=%q err=%v", groupCount, tagCount, receiptState, err)
	}

	// Schema-invalid but digest-valid artifacts fail inside the real tag sink;
	// the EER completion CAS rolls back and no observation appears.
	_, badID, badJob := acceptCatalogEffect(t, ctx, pool, repository, "sink-fail")
	badPayload := []byte(`{"groups":null}`)
	badArtifact := effectport.ResultArtifact{Kind: "wecom.tag_catalog.snapshot.v1", Payload: badPayload, Digest: effectport.Hash("external-effect.artifact.v1", "wecom.tag_catalog.snapshot.v1", string(badPayload))}
	badAdapter := integrationAdapter(func(context.Context, effectport.Envelope, effectport.Attempt) (effectport.AdapterResult, error) {
		return effectport.AdapterResult{Completion: effectport.StateExecuted, ReceiptDigest: effectport.Hash("receipt", "bad"), CallAttempted: true, RealExternalCallExecuted: true, Artifact: badArtifact}, nil
	})
	if err = repository.RunAttempt(ctx, badID, 1, badJob, badAdapter); err == nil {
		t.Fatal("expected sink validation failure")
	}
	current, err = repository.Get(ctx, "eer_"+itoa(badID))
	if err != nil || current.State != effects.StateAttempted {
		t.Fatalf("sink rollback=%+v err=%v", current, err)
	}
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM tag_provider_observations WHERE effect_id=$1`, badID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("sink rollback snapshot count=%d err=%v", count, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE tag_sync_receipts SET state='final_failed',effect_state='final_failed',completed_at=clock_timestamp() WHERE effect_id=$1`, badID); err != nil {
		t.Fatal(err)
	}

	_, unknownID, unknownJob := acceptCatalogEffect(t, ctx, pool, repository, "unknown")
	unknown := integrationAdapter(func(context.Context, effectport.Envelope, effectport.Attempt) (effectport.AdapterResult, error) {
		return effectport.AdapterResult{CallAttempted: true}, errors.New("post-call")
	})
	if err = repository.RunAttempt(ctx, unknownID, 1, unknownJob, unknown); err != nil {
		t.Fatal(err)
	}
	current, err = repository.Get(ctx, "eer_"+itoa(unknownID))
	if err != nil || current.State != effects.StateUnknown {
		t.Fatalf("unknown=%+v err=%v", current, err)
	}
	if err = pool.QueryRow(ctx, `SELECT state FROM tag_sync_receipts WHERE effect_id=$1`, unknownID).Scan(&receiptState); err != nil || receiptState != "outcome_unknown" {
		t.Fatalf("unknown tag receipt=%q err=%v", receiptState, err)
	}
	if _, _, err = repository.Reconcile(ctx, effects.ControlCommand{EffectID: "eer_" + itoa(unknownID), ReceiptKey: effectport.Hash("reconcile", "unknown"), EvidenceDigest: effectport.Hash("evidence", "not-applied"), ActorAdminUserID: 7}); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT state FROM tag_sync_receipts WHERE effect_id=$1`, unknownID).Scan(&receiptState); err != nil || receiptState != "reconciled" {
		t.Fatalf("reconciled tag receipt=%q err=%v", receiptState, err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM tag_provider_observations WHERE effect_id=$1`, unknownID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("reconciled wrote snapshot count=%d err=%v", count, err)
	}

	_, finalID, finalJob := acceptCatalogEffect(t, ctx, pool, repository, "final-failed")
	final := integrationAdapter(func(context.Context, effectport.Envelope, effectport.Attempt) (effectport.AdapterResult, error) {
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash("receipt", "final")}, nil
	})
	if err = repository.RunAttempt(ctx, finalID, 1, finalJob, final); err != nil {
		t.Fatal(err)
	}
	current, err = repository.Get(ctx, "eer_"+itoa(finalID))
	if err != nil || current.State != effects.StateFinalFailed {
		t.Fatalf("final=%+v err=%v", current, err)
	}
	if err = pool.QueryRow(ctx, `SELECT state FROM tag_sync_receipts WHERE effect_id=$1`, finalID).Scan(&receiptState); err != nil || receiptState != "final_failed" {
		t.Fatalf("final tag receipt=%q err=%v", receiptState, err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM tag_provider_observations WHERE effect_id=$1`, finalID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("final wrote snapshot count=%d err=%v", count, err)
	}

	// A stale River generation returns before adapter/sink work.
	stale, staleID, staleJob := acceptCatalogEffect(t, ctx, pool, repository, "stale")
	if _, err = pool.Exec(ctx, `UPDATE external_effects SET state='retryable_failed' WHERE id=$1`, staleID); err != nil {
		t.Fatal(err)
	}
	if _, _, err = repository.Retry(ctx, effects.ControlCommand{EffectID: stale.ID, ReceiptKey: effectport.Hash("retry", "stale"), ActorAdminUserID: 7}); err != nil {
		t.Fatal(err)
	}
	if err = repository.RunAttempt(ctx, staleID, 1, staleJob, provider); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM tag_provider_observations WHERE effect_id=$1`, staleID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("stale wrote snapshot count=%d err=%v", count, err)
	}
}

func acceptCatalogEffect(t *testing.T, ctx context.Context, pool *pgxpool.Pool, repository *effects.Repository, unique string) (effectport.Projection, int64, int64) {
	t.Helper()
	e := effectport.Envelope{Owner: effectport.OwnerOutbound, Kind: effectport.KindWeComTagCatalog, SourceRefDigest: effectport.Hash("src", unique), TargetRefDigest: effectport.Hash("target"), PayloadDigest: effectport.Hash("payload", unique), PolicyVersionHash: effectport.Hash("policy")}
	p, _, err := repository.AcceptAndQueue(ctx, effectport.AcceptCommand{ReceiptKey: effectport.Hash("accept", unique), Envelope: e})
	if err != nil {
		t.Fatal(err)
	}
	var id, job int64
	if _, err = fmt.Sscanf(p.ID, "eer_%d", &id); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT river_job_id FROM external_effect_jobs WHERE effect_id=$1`, id).Scan(&job); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("tag-sync-" + unique))
	if _, err = pool.Exec(ctx, `INSERT INTO tag_sync_receipts(actor_admin_user_id,idempotency_key_digest,trace_id,sync_kind,state,event_id,queue_job_id,effect_id,effect_ref,effect_state,accept_receipt_id,queue_receipt_id,accepted_at) VALUES(7,$1,'','manual','queued',1,$2,$3,$4,'queued','accept','queue',clock_timestamp())`, digest[:], job, id, p.ID); err != nil {
		t.Fatal(err)
	}
	return p, id, job
}

func catalogIntegrationPool(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	raw, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cfg, err := pgxpool.ParseConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	var b [8]byte
	if _, err = rand.Read(b[:]); err != nil {
		t.Fatal(err)
	}
	schema := "aicrm_catalog_effect_" + hex.EncodeToString(b[:])
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	cfg = cfg.Copy()
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	migrator, err := rivermigrate.New(riverpgxv5.New(pool), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		t.Fatal(err)
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate")
	}
	base := filepath.Join(filepath.Dir(file), "..", "..", "migrations")
	for _, name := range []string{"0005_external_effects.sql", "0008_tag_catalog.sql", "0019_tag_catalog_sync_projection.sql", "0125_outbound_material_preparation.sql"} {
		sql, readErr := os.ReadFile(filepath.Join(base, name))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, err = pool.Exec(ctx, string(sql)); err != nil {
			t.Fatal(err)
		}
	}
	return pool, func() {
		pool.Close()
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close()
	}
}

func itoa(value int64) string { return strconv.FormatInt(value, 10) }

var _ tagport.SyncCompletionWriter = (*tagstore.Repository)(nil)

type surveyCompletionIntegrationIdentity struct{}

func (surveyCompletionIntegrationIdentity) VerifiedExternalIdentityValue(context.Context, customerdomain.CustomerID, identitydomain.Kind, string) (string, bool, error) {
	return "union-integration", true, nil
}

// These dependencies are required by the public submission service even when
// the fixture has no mobile question. They deliberately expose no identity
// resolution or customer creation behavior.
type surveyCompletionNoopPhone struct{}

func (surveyCompletionNoopPhone) AttachDeclaredPhoneToCustomer(context.Context, identityport.DeclaredPhoneCommand) (identityport.DeclaredAttachResult, error) {
	return identityport.DeclaredAttachResult{}, nil
}

type surveyCompletionNoopProjection struct{}

func (surveyCompletionNoopProjection) UpsertDirectoryProjection(context.Context, customerport.DirectoryProjection) error {
	return nil
}
func (surveyCompletionNoopProjection) MarkDirectoryStale(context.Context, []customerdomain.CustomerID, time.Time) (int64, error) {
	return 0, nil
}
func (surveyCompletionNoopProjection) UpdateDirectoryPhone(context.Context, customerdomain.CustomerID, string, identitydomain.Assurance, int64, time.Time) error {
	return nil
}
func (surveyCompletionNoopProjection) ClearDirectoryPhone(context.Context, customerdomain.CustomerID, time.Time) error {
	return nil
}

func TestPostgreSQLSurveySyntheticPushSurvivesRepositoryRestartAndDoesNotBlindRetryUnknown(t *testing.T) {
	pool, cleanup := catalogIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	applySurveyCompletionMigrations(t, ctx, pool)
	wrapped, err := platformpostgres.Wrap(pool, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := surveysecure.NewCipher(base64.RawStdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	surveys, err := surveystore.NewPostgreSQL(pool, uow, cipher)
	if err != nil {
		t.Fatal(err)
	}
	var actor, questionnaire int64
	if err = pool.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name) VALUES('survey-synthetic-effect','$argon2id$test','Survey synthetic effect') RETURNING id`).Scan(&actor); err != nil {
		t.Fatal(err)
	}
	// Seed through the normal definition application. QueueCompletionTest reads
	// the active immutable definition before it accepts an effect; a bare
	// questionnaire row is intentionally unavailable to production code.
	definitionService := surveyapp.NewService(uow, surveys)
	definition, err := definitionService.Create(ctx, surveyport.CreateCommand{
		Questionnaire: surveyport.Questionnaire{
			Name:              "Synthetic effect",
			Title:             "Synthetic effect title",
			Mode:              surveyport.ModeSurvey,
			AnswerDisplayMode: "all_in_one",
			Slug:              "synthetic-effect",
			Status:            surveyport.StatusDisabled,
			Questions: []surveyport.Question{{
				Type:      surveyport.QuestionTextarea,
				Title:     "测试问题",
				SortOrder: 0,
			}},
		},
		ActorID:        actor,
		IdempotencyKey: "survey-synthetic-definition-create-0001",
	})
	if err != nil {
		t.Fatal(err)
	}
	questionnaire = int64(definition.ID)
	var receivedMu sync.Mutex
	received := make([]string, 0, 1)
	receivedSignal := make(chan struct{}, 2)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		receivedMu.Lock()
		received = append(received, string(body))
		receivedMu.Unlock()
		select {
		case receivedSignal <- struct{}{}:
		default:
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	day, frequency, expiresAtTS := int64(15), int64(3), int64(2147483647)
	endpoint, network := surveyCompletionTLSEndpoint(t, server, "example.com")
	target := outbound.SurveyCompletionTarget{Reference: "local-webhook", Endpoint: endpoint, SigningKey: []byte(strings.Repeat("s", 32)), ClientID: "survey-v3-test", Version: "v1", IdentityKind: identitydomain.KindUnionID, IdentityScope: "wechat-open-platform:primary", Day: &day, Frequency: &frequency, ExpiresAtTS: &expiresAtTS, CustomParams: map[string]string{"campaign": "control", "unionid": "must-not-send"}}
	provider, err := outbound.NewSurveyCompletionProvider(outbound.SurveyCompletionProviderConfig{Enabled: true, Targets: []outbound.SurveyCompletionTarget{target}, Reader: surveys, Client: server.Client(), Network: network, Identities: surveyCompletionIntegrationIdentity{}})
	if err != nil {
		t.Fatal(err)
	}
	sink, err := outbound.NewSurveyCompletionSink(surveys)
	if err != nil {
		t.Fatal(err)
	}
	effectRepository, runtime := newSurveyEffectRuntime(t, pool, sink, provider)
	service := surveyapp.NewSubmissionService(uow, surveys, cipher)
	if err = service.BindCompletionPolicy(provider); err != nil {
		t.Fatal(err)
	}
	if err = service.BindCompletionIntent(surveyCompletionEffectAccepter{effects: effectRepository}); err != nil {
		t.Fatal(err)
	}
	if err = service.BindCompletionIdentity(provider); err != nil {
		t.Fatal(err)
	}
	if err = service.BindDeclaredPhone(surveyCompletionNoopPhone{}, surveyCompletionNoopProjection{}); err != nil {
		t.Fatal(err)
	}
	config, err := service.GetOperationConfiguration(ctx, surveyport.ID(questionnaire))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.SaveOperationConfiguration(ctx, surveyport.OperationConfiguration{QuestionnaireID: surveyport.ID(questionnaire), ExternalPushEnabled: true, ExternalPushConfigurationRef: target.Reference, ExternalPushMetadata: json.RawMessage(`{"remark":"frozen","custom_params":{"campaign":"control","unionid":"must-not-send"}}`), Version: config.Version}, actor, "survey-synthetic-config-0001"); err != nil {
		t.Fatal(err)
	}
	key := "survey-synthetic-test-effect-0001"
	first, err := service.QueueCompletionTest(ctx, surveyport.ID(questionnaire), actor, key)
	if err != nil || first.EffectID == "" || first.State != "queued" {
		t.Fatalf("accept=%+v err=%v", first, err)
	}
	// Change current metadata before replay. The stored target/body/timestamp
	// must win, so no new effect or River job is created.
	current, err := service.GetOperationConfiguration(ctx, surveyport.ID(questionnaire))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.SaveOperationConfiguration(ctx, surveyport.OperationConfiguration{QuestionnaireID: surveyport.ID(questionnaire), ExternalPushEnabled: true, ExternalPushConfigurationRef: target.Reference, ExternalPushMetadata: json.RawMessage(`{"remark":"changed","custom_params":{"campaign":"changed"}}`), Version: current.Version}, actor, "survey-synthetic-config-0002"); err != nil {
		t.Fatal(err)
	}
	var frozen surveyapp.CompletionTestSnapshot
	var frozenFound bool
	if err = uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		frozen, frozenFound, readErr = surveys.GetCompletionTestSnapshot(tx, surveyport.ID(questionnaire), key)
		return readErr
	}); err != nil || !frozenFound || frozen.Policy.Remark != "frozen" || frozen.Policy.CustomParams["campaign"] != "control" {
		t.Fatalf("frozen replay snapshot=%+v found=%t err=%v", frozen, frozenFound, err)
	}
	replay, err := service.QueueCompletionTest(ctx, surveyport.ID(questionnaire), actor, key)
	if err != nil || replay != first {
		t.Fatalf("replay=%+v first=%+v err=%v", replay, first, err)
	}
	var effectCount int64
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM external_effects`).Scan(&effectCount); err != nil || effectCount != 1 {
		t.Fatalf("effect count=%d err=%v", effectCount, err)
	}

	// This starts the real River runtime. The effect was already committed to
	// River before runtime startup, so a process restart cannot lose it.
	stop, done := startSurveyEffectRuntime(t, runtime)
	select {
	case <-receivedSignal:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for River to deliver synthetic test")
	}
	waitForSurveyEffectState(t, effectRepository, first.EffectID, effects.StateExecuted)
	stopSurveyEffectRuntime(t, stop, done)
	receivedMu.Lock()
	firstBody := append([]string(nil), received...)
	receivedMu.Unlock()
	if len(firstBody) != 1 || strings.Contains(firstBody[0], "changed") || !strings.Contains(firstBody[0], `"remark":"frozen"`) || !strings.Contains(firstBody[0], `"user_id":"questionnaire_test"`) || !strings.Contains(firstBody[0], `"day":15`) || !strings.Contains(firstBody[0], `"frequency":3`) || !strings.Contains(firstBody[0], `"expires_at_ts":2147483647`) || strings.Contains(firstBody[0], "unionid") || strings.Contains(firstBody[0], "must-not-send") {
		t.Fatalf("runtime body=%v", firstBody)
	}
	var receiptState string
	var callAttempted, realCall, resultReceived bool
	var attemptNumber int32
	if err = pool.QueryRow(ctx, `SELECT status,provider_call_attempted,provider_real_call_executed,provider_result_received,provider_attempt_number FROM survey_external_operation_receipts WHERE effect_id=$1`, first.EffectID).Scan(&receiptState, &callAttempted, &realCall, &resultReceived, &attemptNumber); err != nil || receiptState != "executed" || !callAttempted || !realCall || !resultReceived || attemptNumber != 1 {
		t.Fatalf("executed receipt state=%q call=%t real=%t result=%t attempt=%d err=%v", receiptState, callAttempted, realCall, resultReceived, attemptNumber, err)
	}

	// A normal public submission is accepted in its own UoW, then the restarted
	// River runtime reads its frozen protected payload outside that transaction
	// and reaches the local receiver once. This is intentionally distinct from
	// the synthetic operator test above.
	if _, err = definitionService.Publish(ctx, surveyport.ID(questionnaire), definition.Version, actor, "survey-normal-publish-0001"); err != nil {
		t.Fatal(err)
	}
	var questionID, customerID int64
	if err = pool.QueryRow(ctx, `SELECT question.id FROM survey_definition_questions question JOIN survey_questionnaires q ON q.active_definition_version_id=question.definition_version_id WHERE q.id=$1 ORDER BY question.id LIMIT 1`, questionnaire).Scan(&questionID); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `INSERT INTO customers DEFAULT VALUES RETURNING id`).Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	current, err = service.GetOperationConfiguration(ctx, surveyport.ID(questionnaire))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.SaveOperationConfiguration(ctx, surveyport.OperationConfiguration{QuestionnaireID: surveyport.ID(questionnaire), ExternalPushEnabled: true, ExternalPushConfigurationRef: target.Reference, ExternalPushMetadata: json.RawMessage(`{"remark":"frozen","custom_params":{"campaign":"control"}}`), Version: current.Version}, actor, "survey-normal-config-0001"); err != nil {
		t.Fatal(err)
	}
	customer := customerdomain.CustomerID(customerID)
	var published surveyport.Questionnaire
	if err = uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		published, readErr = surveys.GetPublishedBySlug(tx, "synthetic-effect")
		return readErr
	}); err != nil {
		t.Fatalf("normal submission fixture is not published: %v", err)
	}
	if published.DefinitionVersion < 1 || len(published.Questions) != 1 {
		t.Fatalf("normal submission published definition=%+v", published)
	}
	normal, err := service.Submit(ctx, surveyport.SubmitCommand{Slug: "synthetic-effect", DefinitionVersion: published.DefinitionVersion, SubmissionKey: strings.Repeat("n", 43), Identity: surveyport.SubmissionIdentity{State: surveyport.IdentityResolved, CustomerID: &customer, EvidenceDigest: strings.Repeat("a", 64)}, Answers: []surveyport.SubmissionAnswer{{QuestionID: surveyport.ID(questionID), TextValue: "正常提交"}}})
	if err != nil || normal.SubmissionID < 1 {
		t.Fatalf("normal submission=%+v err=%v", normal, err)
	}
	normalRepository, normalRuntime := newSurveyEffectRuntime(t, pool, sink, provider)
	stop, done = startSurveyEffectRuntime(t, normalRuntime)
	select {
	case <-receivedSignal:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for River to deliver normal submission")
	}
	var normalEffectID string
	if err = pool.QueryRow(ctx, `SELECT effect_id FROM survey_external_operation_receipts WHERE submission_id=$1 AND operation_kind='external_push'`, normal.SubmissionID).Scan(&normalEffectID); err != nil {
		t.Fatal(err)
	}
	waitForSurveyEffectState(t, normalRepository, normalEffectID, effects.StateExecuted)
	stopSurveyEffectRuntime(t, stop, done)
	receivedMu.Lock()
	normalBodies := append([]string(nil), received...)
	receivedMu.Unlock()
	if len(normalBodies) != 2 || !strings.Contains(normalBodies[1], `"user_id":"union-integration"`) || !strings.Contains(normalBodies[1], "正常提交") {
		t.Fatalf("normal runtime receiver did not receive the frozen submission once (count=%d)", len(normalBodies))
	}

	// A fresh runtime observes no re-delivery after the prior process stopped.
	_, restartedRuntime := newSurveyEffectRuntime(t, pool, sink, provider)
	stop, done = startSurveyEffectRuntime(t, restartedRuntime)
	time.Sleep(250 * time.Millisecond)
	stopSurveyEffectRuntime(t, stop, done)
	receivedMu.Lock()
	if len(received) != 2 {
		receivedMu.Unlock()
		t.Fatalf("runtime restart repeated provider call: %d", len(received))
	}
	receivedMu.Unlock()
	afterExecuted, err := service.QueueCompletionTest(ctx, surveyport.ID(questionnaire), actor, key)
	if err != nil || afterExecuted.EffectID != first.EffectID || afterExecuted.State != "executed" {
		t.Fatalf("terminal replay=%+v err=%v", afterExecuted, err)
	}

	unknown, err := service.QueueCompletionTest(ctx, surveyport.ID(questionnaire), actor, "survey-synthetic-test-unknown-0002")
	if err != nil {
		t.Fatal(err)
	}
	var unknownMu sync.Mutex
	unknownCalls := 0
	unknownSignal := make(chan struct{}, 2)
	unknownAdapter := integrationAdapter(func(context.Context, effectport.Envelope, effectport.Attempt) (effectport.AdapterResult, error) {
		unknownMu.Lock()
		unknownCalls++
		unknownMu.Unlock()
		select {
		case unknownSignal <- struct{}{}:
		default:
		}
		return effectport.AdapterResult{CallAttempted: true, RealExternalCallExecuted: true}, errors.New("post-call unknown")
	})
	unknownRepository, unknownRuntime := newSurveyEffectRuntime(t, pool, sink, unknownAdapter)
	stop, done = startSurveyEffectRuntime(t, unknownRuntime)
	select {
	case <-unknownSignal:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for River unknown attempt")
	}
	waitForSurveyEffectState(t, unknownRepository, unknown.EffectID, effects.StateUnknown)
	stopSurveyEffectRuntime(t, stop, done)
	_, unknownRestart := newSurveyEffectRuntime(t, pool, sink, unknownAdapter)
	stop, done = startSurveyEffectRuntime(t, unknownRestart)
	time.Sleep(250 * time.Millisecond)
	stopSurveyEffectRuntime(t, stop, done)
	unknownMu.Lock()
	if unknownCalls != 1 {
		unknownMu.Unlock()
		t.Fatalf("outcome_unknown was resent %d times", unknownCalls)
	}
	unknownMu.Unlock()
	var unknownCall, unknownReal, unknownResult bool
	var unknownAttempt int32
	if err = pool.QueryRow(ctx, `SELECT provider_call_attempted,provider_real_call_executed,provider_result_received,provider_attempt_number FROM survey_external_operation_receipts WHERE effect_id=$1`, unknown.EffectID).Scan(&unknownCall, &unknownReal, &unknownResult, &unknownAttempt); err != nil || !unknownCall || !unknownReal || unknownResult || unknownAttempt != 1 {
		t.Fatalf("unknown receipt call=%t real=%t result=%t attempt=%d err=%v", unknownCall, unknownReal, unknownResult, unknownAttempt, err)
	}

	preCall, err := service.QueueCompletionTest(ctx, surveyport.ID(questionnaire), actor, "survey-synthetic-test-pre-call-0003")
	if err != nil {
		t.Fatal(err)
	}
	preCallAdapter := integrationAdapter(func(context.Context, effectport.Envelope, effectport.Attempt) (effectport.AdapterResult, error) {
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash("survey-pre-call-rejected")}, nil
	})
	preCallRepository, preCallRuntime := newSurveyEffectRuntime(t, pool, sink, preCallAdapter)
	stop, done = startSurveyEffectRuntime(t, preCallRuntime)
	waitForSurveyEffectState(t, preCallRepository, preCall.EffectID, effects.StateFinalFailed)
	stopSurveyEffectRuntime(t, stop, done)
	var preCallAttempted, preCallReal, preCallResult bool
	var preCallAttemptNumber int32
	if err = pool.QueryRow(ctx, `SELECT provider_call_attempted,provider_real_call_executed,provider_result_received,provider_attempt_number FROM survey_external_operation_receipts WHERE effect_id=$1`, preCall.EffectID).Scan(&preCallAttempted, &preCallReal, &preCallResult, &preCallAttemptNumber); err != nil || preCallAttempted || preCallReal || preCallResult || preCallAttemptNumber != 1 {
		t.Fatalf("pre-call receipt call=%t real=%t result=%t attempt=%d err=%v", preCallAttempted, preCallReal, preCallResult, preCallAttemptNumber, err)
	}

}

func newSurveyEffectRuntime(t *testing.T, pool *pgxpool.Pool, sink effectport.CompletionSink, adapter effectport.ProviderAdapter) (*effects.Repository, *platformjobqueue.Runtime) {
	t.Helper()
	workers := river.NewWorkers()
	worker := effects.NewWorker(nil, adapter)
	if err := river.AddWorkerSafely[effects.EffectJobArgs](workers, worker); err != nil {
		t.Fatal(err)
	}
	client, err := platformjobqueue.NewInsertClient(pool, workers)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := effects.NewRepository(pool, client)
	if err != nil {
		t.Fatal(err)
	}
	if err = repository.SetCompletionSink(sink); err != nil {
		t.Fatal(err)
	}
	if err = worker.BindRepository(repository); err != nil {
		t.Fatal(err)
	}
	runtime, err := platformjobqueue.NewRuntime(pool, workers, platformjobqueue.OutboundQueue)
	if err != nil {
		t.Fatal(err)
	}
	return repository, runtime
}

func startSurveyEffectRuntime(t *testing.T, runtime *platformjobqueue.Runtime) (context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runtime.Run(ctx) }()
	return cancel, done
}

func stopSurveyEffectRuntime(t *testing.T, cancel context.CancelFunc, done <-chan error) {
	t.Helper()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out stopping River runtime")
	}
}

func waitForSurveyEffectState(t *testing.T, repository *effects.Repository, effectID string, want effects.State) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		current, err := repository.Get(context.Background(), effectID)
		if err == nil && current.State == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	current, err := repository.Get(context.Background(), effectID)
	t.Fatalf("effect %s state=%+v err=%v want=%s", effectID, current, err, want)
}

func applySurveyCompletionMigrations(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate")
	}
	base := filepath.Join(filepath.Dir(file), "..", "..", "migrations")
	for _, name := range []string{"0001_platform.sql", "0002_identity.sql", "0003_access.sql", "0018_survey.sql", "0038_survey_oauth_phone_vault.sql", "0067_survey_completion_snapshots.sql", "0073_survey_completion_test_push_snapshots.sql", "0074_survey_external_operation_execution_facts.sql", "0075_external_effects_survey_completion_kind.sql", "0176_survey_single_submission_claims.sql", "0177_survey_operation_legacy_parity.sql"} {
		sql, err := os.ReadFile(filepath.Join(base, name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, string(sql)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
	}
}
