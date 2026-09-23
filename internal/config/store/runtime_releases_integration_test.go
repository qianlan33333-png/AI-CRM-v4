package store_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	configapp "github.com/qianlan33333-png/AI-CRM-v3/internal/config/app"
	configport "github.com/qianlan33333-png/AI-CRM-v3/internal/config/port"
	configstore "github.com/qianlan33333-png/AI-CRM-v3/internal/config/store"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// OneID decision: not involved. Runtime releases contain only an Automation
// numeric policy and no customer or external identity.
// Persistence decision: Config owns draft, release, active pointer, receipt,
// audit and outbox in one PostgreSQL UoW. No Provider or worker is called.
func TestPostgreSQLRuntimeReleasePublishListRollbackAndOutbox(t *testing.T) {
	pool, cleanup := runtimeConfigIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	service := runtimeConfigService(t, pool, nil)

	first := runtimeConfigCreateValidatePublish(t, ctx, service, 0, 2, "release-create-0001", "release-validate-0001", "release-publish-0001")
	second := runtimeConfigCreateValidatePublish(t, ctx, service, first.ID, 3, "release-create-0002", "release-validate-0002", "release-publish-0002")
	if second.State != configport.RuntimeReleasePublished || second.ID <= first.ID {
		t.Fatalf("published release=%+v first=%+v", second, first)
	}

	page, err := service.ListRuntimeReleases(ctx, 20)
	if err != nil {
		t.Fatal(err)
	}
	if page.ActiveRevision != second.ID || page.Effective.Revision != second.ID || page.Effective.AutomationMaxRecipients != 3 || len(page.Releases) != 2 {
		t.Fatalf("runtime release page=%+v", page)
	}
	if page.Releases[0].ID != second.ID || page.Releases[0].Settings[0].Key != configport.AutomationOperationsMaxRecipientsPerRun || string(page.Releases[0].Settings[0].Value) != "3" || page.Releases[1].ID != first.ID || string(page.Releases[1].Settings[0].Value) != "2" {
		t.Fatalf("list did not return both immutable settings: %#v", page.Releases)
	}

	var event json.RawMessage
	if err = pool.QueryRow(ctx, `SELECT payload FROM config_outbox WHERE event_type='runtime_release.published' AND idempotency_key=$1`, fmt.Sprintf("runtime_release.published:release:%d", second.ID)).Scan(&event); err != nil {
		t.Fatal(err)
	}
	var payload struct {
		ReleaseID int64 `json:"release_id"`
		Revision  int64 `json:"revision"`
	}
	if err = json.Unmarshal(event, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.ReleaseID != second.ID || payload.Revision != second.ID {
		t.Fatalf("publish outbox payload=%s", event)
	}

	rolledBack, err := service.RollbackRuntimeRelease(ctx, configport.RuntimeReleaseRollbackCommand{ReleaseID: first.ID, ExpectedBaseRevision: second.ID, Actor: "7", IdempotencyKey: "release-rollback-0001"})
	if err != nil {
		t.Fatal(err)
	}
	if rolledBack.State != configport.RuntimeReleasePublished || rolledBack.RollbackOfReleaseID == nil || *rolledBack.RollbackOfReleaseID != first.ID {
		t.Fatalf("rollback release=%+v", rolledBack)
	}
	effective, err := service.EffectiveSnapshot(ctx)
	if err != nil || effective.Revision != rolledBack.ID || effective.AutomationMaxRecipients != 2 {
		t.Fatalf("effective after rollback=%+v err=%v", effective, err)
	}
	// A process restart uses the immutable active revision, never an in-memory
	// copy of the release that performed the rollback.
	restarted := runtimeConfigService(t, pool, nil)
	restored, err := restarted.EffectiveSnapshot(ctx)
	if err != nil || restored.Revision != rolledBack.ID || restored.AutomationMaxRecipients != 2 {
		t.Fatalf("effective after service restart=%+v err=%v", restored, err)
	}
}

func TestPostgreSQLRuntimeReleaseOutboxKeysAreReleaseScopedAcrossActors(t *testing.T) {
	pool, cleanup := runtimeConfigIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	service := runtimeConfigService(t, pool, nil)
	first := runtimeConfigCreateValidateAs(t, ctx, service, 0, 2, "7", "release-create-same-key-1", "release-validate-same-key-1")
	first, err := service.PublishRuntimeRelease(ctx, configport.RuntimeReleasePublishCommand{ReleaseID: first.ID, ExpectedBaseRevision: 0, ExpectedChecksum: first.Checksum, Actor: "7", IdempotencyKey: "shared-publish-key"})
	if err != nil {
		t.Fatal(err)
	}
	second := runtimeConfigCreateValidateAs(t, ctx, service, first.ID, 3, "8", "release-create-same-key-2", "release-validate-same-key-2")
	second, err = service.PublishRuntimeRelease(ctx, configport.RuntimeReleasePublishCommand{ReleaseID: second.ID, ExpectedBaseRevision: first.ID, ExpectedChecksum: second.Checksum, Actor: "8", IdempotencyKey: "shared-publish-key"})
	if err != nil {
		t.Fatal(err)
	}
	var events int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM config_outbox WHERE idempotency_key=ANY($1::text[])`, []string{fmt.Sprintf("runtime_release.published:release:%d", first.ID), fmt.Sprintf("runtime_release.published:release:%d", second.ID)}).Scan(&events); err != nil || events != 2 {
		t.Fatalf("release-scoped publish outbox events=%d err=%v", events, err)
	}
}

func TestPostgreSQLRuntimeReleasePublishCASAndFailureRollback(t *testing.T) {
	pool, cleanup := runtimeConfigIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	service := runtimeConfigService(t, pool, nil)
	left := runtimeConfigCreateValidate(t, ctx, service, 0, 2, "release-create-left-01", "release-validate-left-01")
	right := runtimeConfigCreateValidate(t, ctx, service, 0, 3, "release-create-right-01", "release-validate-right-01")

	start := make(chan struct{})
	results := make(chan error, 2)
	for _, release := range []configport.RuntimeRelease{left, right} {
		release := release
		go func() {
			<-start
			_, err := service.PublishRuntimeRelease(ctx, configport.RuntimeReleasePublishCommand{ReleaseID: release.ID, ExpectedBaseRevision: 0, ExpectedChecksum: release.Checksum, Actor: "7", IdempotencyKey: "release-publish-race-" + hex.EncodeToString([]byte{byte(release.ID)})})
			results <- err
		}()
	}
	close(start)
	var succeeded, conflicted int
	for range 2 {
		err := <-results
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, configport.ErrRuntimeReleaseConflict):
			conflicted++
		default:
			t.Fatalf("publish race error=%v", err)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("publish race success/conflict=%d/%d", succeeded, conflicted)
	}

	failing := runtimeConfigService(t, pool, failingRuntimeAppender{})
	active, err := service.EffectiveSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	candidate := runtimeConfigCreateValidate(t, ctx, service, active.Revision, 4, "release-create-fail-01", "release-validate-fail-01")
	_, err = failing.PublishRuntimeRelease(ctx, configport.RuntimeReleasePublishCommand{ReleaseID: candidate.ID, ExpectedBaseRevision: active.Revision, ExpectedChecksum: candidate.Checksum, Actor: "7", IdempotencyKey: "release-publish-fail-01"})
	if !errors.Is(err, errRuntimeOutbox) {
		t.Fatalf("publish failure=%v", err)
	}
	after, err := service.EffectiveSnapshot(ctx)
	if err != nil || after.Revision != active.Revision {
		t.Fatalf("failure changed active snapshot=%+v err=%v", after, err)
	}
	var state, receiptState string
	var receiptCount, auditCount, outboxCount int
	if err = pool.QueryRow(ctx, `SELECT state FROM config_runtime_releases WHERE id=$1`, candidate.ID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*),COALESCE(max(state),'') FROM config_runtime_release_command_receipts WHERE action='runtime_release.publish' AND idempotency_key='release-publish-fail-01'`).Scan(&receiptCount, &receiptState); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM config_runtime_release_audits WHERE release_id=$1 AND action='published'`, candidate.ID).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM config_outbox WHERE idempotency_key='runtime_release.published:release-publish-fail-01'`).Scan(&outboxCount); err != nil {
		t.Fatal(err)
	}
	if state != string(configport.RuntimeReleaseValidated) || receiptCount != 0 || receiptState != "" || auditCount != 0 || outboxCount != 0 {
		t.Fatalf("failed publish leaked state=%q receipt=%d/%q audit=%d outbox=%d", state, receiptCount, receiptState, auditCount, outboxCount)
	}
}

var errRuntimeOutbox = errors.New("runtime outbox rejected")

type failingRuntimeAppender struct{}

func (failingRuntimeAppender) Append(context.Context, configport.Event) (configport.EventID, error) {
	return 0, errRuntimeOutbox
}

func runtimeConfigCreateValidatePublish(t *testing.T, ctx context.Context, service *configapp.RuntimeReleaseService, base int64, limit int, createKey, validateKey, publishKey string) configport.RuntimeRelease {
	t.Helper()
	release := runtimeConfigCreateValidate(t, ctx, service, base, limit, createKey, validateKey)
	out, err := service.PublishRuntimeRelease(ctx, configport.RuntimeReleasePublishCommand{ReleaseID: release.ID, ExpectedBaseRevision: base, ExpectedChecksum: release.Checksum, Actor: "7", IdempotencyKey: publishKey})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func runtimeConfigCreateValidate(t *testing.T, ctx context.Context, service *configapp.RuntimeReleaseService, base int64, limit int, createKey, validateKey string) configport.RuntimeRelease {
	return runtimeConfigCreateValidateAs(t, ctx, service, base, limit, "7", createKey, validateKey)
}
func runtimeConfigCreateValidateAs(t *testing.T, ctx context.Context, service *configapp.RuntimeReleaseService, base int64, limit int, actor, createKey, validateKey string) configport.RuntimeRelease {
	t.Helper()
	value, err := json.Marshal(limit)
	if err != nil {
		t.Fatal(err)
	}
	draft, err := service.CreateRuntimeReleaseDraft(ctx, configport.RuntimeReleaseDraftCommand{ExpectedBaseRevision: base, Settings: []configport.RuntimeSetting{{Key: configport.AutomationOperationsMaxRecipientsPerRun, Value: value}}, Actor: actor, IdempotencyKey: createKey})
	if err != nil {
		t.Fatal(err)
	}
	out, err := service.ValidateRuntimeRelease(ctx, configport.RuntimeReleaseMutationCommand{ReleaseID: draft.ID, Actor: actor, IdempotencyKey: validateKey})
	if err != nil {
		t.Fatal(err)
	}
	if out.State != configport.RuntimeReleaseValidated {
		t.Fatalf("validated=%+v", out)
	}
	return out
}
func runtimeConfigService(t *testing.T, pool *pgxpool.Pool, appender configport.EventAppender) *configapp.RuntimeReleaseService {
	t.Helper()
	wrapped, err := platformpostgres.Wrap(pool, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := configstore.NewPostgreSQL(pool, uow)
	if err != nil {
		t.Fatal(err)
	}
	if appender == nil {
		appender = repository
	}
	service, err := configapp.NewRuntimeReleaseService(uow, repository, appender, 1)
	if err != nil {
		t.Fatal(err)
	}
	return service
}
func runtimeConfigIntegrationPool(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	url, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("DATABASE_URL is not configured; skipping Config PostgreSQL integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var random [8]byte
	if _, err = rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	schema := "aicrm_runtime_config_test_" + hex.EncodeToString(random[:])
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
		admin.Close(ctx)
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		admin.Close(ctx)
		t.Fatal(err)
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		pool.Close()
		admin.Close(ctx)
		t.Fatal("locate test")
	}
	for _, name := range []string{"0013_automation_agents.sql", "0015_config_adminops.sql", "0043_automation_runtime.sql", "0094_runtime_config_releases.sql"} {
		payload, readErr := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations", name))
		if readErr != nil {
			pool.Close()
			admin.Close(ctx)
			t.Fatal(readErr)
		}
		if _, execErr := pool.Exec(ctx, string(payload)); execErr != nil {
			pool.Close()
			admin.Close(ctx)
			t.Fatalf("apply %s: %v", name, execErr)
		}
	}
	return pool, func() {
		pool.Close()
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = admin.Exec(cleanupCtx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close(cleanupCtx)
	}
}

var _ sync.Locker
