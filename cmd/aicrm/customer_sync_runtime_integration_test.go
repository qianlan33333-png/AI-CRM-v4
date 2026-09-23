package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
	"github.com/riverqueue/river/rivertype"

	customerstore "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/store"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identitystore "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/store"
	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformjobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	platformoutbox "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/outbox"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/wecom"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

func TestCustomerSyncRiverRestartResumesMultipleStaffPagesOnPostgreSQL16(t *testing.T) {
	native, cleanup := customerSyncRuntimePool(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var serverVersion int
	if err := native.QueryRow(ctx, `SELECT current_setting('server_version_num')::int`).Scan(&serverVersion); err != nil {
		t.Fatal(err)
	}
	if serverVersion < 160000 || serverVersion >= 170000 {
		t.Fatalf("PostgreSQL server_version_num=%d; want 16.x", serverVersion)
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
	audit, err := platformaudit.NewService(platformaudit.NewPostgreSQLStore())
	if err != nil {
		t.Fatal(err)
	}
	provider := &restartPagingDirectoryProvider{}
	insertWorkers := river.NewWorkers()
	insertWorker := wecom.NewCustomerSyncWorker()
	if err = river.AddWorkerSafely[wecom.CustomerSyncJobArgs](insertWorkers, insertWorker); err != nil {
		t.Fatal(err)
	}
	insertClient, err := platformjobqueue.NewInsertClient(native, insertWorkers)
	if err != nil {
		t.Fatal(err)
	}
	enqueuer, err := wecom.NewRiverCustomerSyncEnqueuer(insertClient)
	if err != nil {
		t.Fatal(err)
	}
	customerStore := customerstore.NewPostgreSQL()
	service := wecom.CustomerSyncService{
		Enabled: true, CorpID: "restart-corp", Provider: provider,
		Identity:   identityapp.OneIDService{Store: identitystore.NewPostgresStore()},
		Projection: customerStore, Timeline: customerStore,
		Store: wecom.NewPostgreSQLCustomerSyncStore(), Outbox: platformoutbox.NewPostgreSQL(),
		Enqueuer: enqueuer, Audit: audit, UOW: uow,
	}
	if err = insertWorker.BindService(service); err != nil {
		t.Fatal(err)
	}
	run, _, err := service.CreateScheduled(ctx, "initial", "initial:runtime-restart-pages")
	if err != nil {
		t.Fatal(err)
	}
	var maxAttempts int
	if err = native.QueryRow(ctx, `SELECT max_attempts FROM river_job WHERE kind=$1`, (wecom.CustomerSyncJobArgs{}).Kind()).Scan(&maxAttempts); err != nil {
		t.Fatal(err)
	}
	if maxAttempts != 12 {
		t.Fatalf("River max_attempts=%d; want existing budget 12", maxAttempts)
	}

	stopFirst := startCustomerSyncRuntime(t, ctx, native, service)
	waitCustomerSyncStatus(t, ctx, service, run.ID, wecom.SyncFailedRetryable)
	stopFirst()
	failed, err := service.Get(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if failed.StaffIndex != 0 || failed.ProviderCursor != "staff-a-page-2" || failed.LastErrorCode != "provider_unavailable" {
		t.Fatalf("persisted recovery point=%+v", failed)
	}
	if provider.callsFor("staff-a", "staff-a-page-2") != 1 {
		t.Fatalf("failed page calls=%d; want 1 before restart", provider.callsFor("staff-a", "staff-a-page-2"))
	}

	stopSecond := startCustomerSyncRuntime(t, ctx, native, service)
	waitCustomerSyncStatus(t, ctx, service, run.ID, wecom.SyncSucceeded)
	stopSecond()

	completed, err := service.Get(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Discovered != 3 || completed.Activated != 3 || completed.AlreadyLinked != 0 || completed.Projected != 3 || completed.StaffIndex != 2 || completed.ProviderCursor != "" {
		t.Fatalf("completed run=%+v", completed)
	}
	if provider.callsFor("staff-a", "") != 1 || provider.callsFor("staff-a", "staff-a-page-2") != 2 || provider.callsFor("staff-b", "") != 1 || provider.callsFor("staff-b", "staff-b-page-2") != 1 {
		t.Fatalf("provider calls=%v", provider.snapshotCalls())
	}

	var sharedCustomers, ownerRelations, syncItems int
	if err = native.QueryRow(ctx, `SELECT
		(SELECT count(DISTINCT customer_id) FROM customer_identities WHERE kind='wecom_external_userid' AND scope_key='wecom-corp:restart-corp' AND normalized_value='external-shared'),
		(SELECT count(*) FROM wecom_customer_owner_observations o JOIN customer_identities i ON i.customer_id=o.customer_id WHERE i.kind='wecom_external_userid' AND i.scope_key='wecom-corp:restart-corp' AND i.normalized_value='external-shared' AND o.relationship_status='active'),
		(SELECT count(*) FROM wecom_customer_sync_items WHERE run_id=$1)`, run.ID).Scan(&sharedCustomers, &ownerRelations, &syncItems); err != nil {
		t.Fatal(err)
	}
	if sharedCustomers != 1 || ownerRelations != 2 || syncItems != 3 {
		t.Fatalf("shared customers=%d owner relations=%d sync items=%d", sharedCustomers, ownerRelations, syncItems)
	}
}

type restartPagingDirectoryProvider struct {
	mu    sync.Mutex
	calls map[string]int
}

func (*restartPagingDirectoryProvider) DirectoryReady() bool { return true }

func (*restartPagingDirectoryProvider) ListContactStaff(context.Context) ([]string, error) {
	return []string{"staff-a", "staff-b"}, nil
}

func (provider *restartPagingDirectoryProvider) BatchExternalContacts(_ context.Context, staffID, cursor string, _ int) (wecomport.ExternalContactPage, error) {
	key := staffID + "\x00" + cursor
	provider.mu.Lock()
	if provider.calls == nil {
		provider.calls = map[string]int{}
	}
	provider.calls[key]++
	call := provider.calls[key]
	provider.mu.Unlock()
	if staffID == "staff-a" && cursor == "staff-a-page-2" && call == 1 {
		return wecomport.ExternalContactPage{}, integrationDirectoryFailure{code: "provider_unavailable", retryable: true}
	}
	contact := func(externalID, employee string) wecomport.ExternalContact {
		return wecomport.ExternalContact{ExternalUserID: externalID, Name: externalID, Gender: 1, Type: 1,
			FollowInfo: []wecomport.ExternalContactFollowInfo{{EmployeeID: employee}}}
	}
	switch {
	case staffID == "staff-a" && cursor == "":
		return wecomport.ExternalContactPage{Contacts: []wecomport.ExternalContact{contact("external-shared", "staff-a")}, NextCursor: "staff-a-page-2"}, nil
	case staffID == "staff-a" && cursor == "staff-a-page-2":
		return wecomport.ExternalContactPage{Contacts: []wecomport.ExternalContact{contact("external-a-only", "staff-a")}}, nil
	case staffID == "staff-b" && cursor == "":
		return wecomport.ExternalContactPage{Contacts: []wecomport.ExternalContact{contact("external-shared", "staff-b")}, NextCursor: "staff-b-page-2"}, nil
	case staffID == "staff-b" && cursor == "staff-b-page-2":
		return wecomport.ExternalContactPage{Contacts: []wecomport.ExternalContact{contact("external-b-only", "staff-b")}}, nil
	default:
		return wecomport.ExternalContactPage{}, integrationDirectoryFailure{code: "unexpected_provider_page"}
	}
}

func (provider *restartPagingDirectoryProvider) callsFor(staffID, cursor string) int {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return provider.calls[staffID+"\x00"+cursor]
}

func (provider *restartPagingDirectoryProvider) snapshotCalls() map[string]int {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	result := make(map[string]int, len(provider.calls))
	for key, value := range provider.calls {
		result[key] = value
	}
	return result
}

func startCustomerSyncRuntime(t *testing.T, ctx context.Context, native *pgxpool.Pool, service wecom.CustomerSyncService) func() {
	t.Helper()
	workers := river.NewWorkers()
	worker := wecom.NewCustomerSyncWorker()
	if err := worker.BindService(service); err != nil {
		t.Fatal(err)
	}
	if err := river.AddWorkerSafely[wecom.CustomerSyncJobArgs](workers, worker); err != nil {
		t.Fatal(err)
	}
	runtime, err := river.NewClient(riverpgxv5.New(native), &river.Config{
		Queues:  map[string]river.QueueConfig{wecom.CustomerSyncQueue: {MaxWorkers: 1}},
		Workers: workers, RetryPolicy: customerSyncRetryPolicy{},
	})
	if err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		if startErr := runtime.Start(context.WithoutCancel(runCtx)); startErr != nil {
			done <- startErr
			return
		}
		<-runCtx.Done()
		shutdown, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		done <- runtime.Stop(shutdown)
	}()
	return func() {
		cancel()
		select {
		case stopErr := <-done:
			if stopErr != nil {
				t.Fatalf("River runtime stop: %v", stopErr)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("River runtime did not stop")
		}
	}
}

type customerSyncRetryPolicy struct{}

func (customerSyncRetryPolicy) NextRetry(*rivertype.JobRow) time.Time {
	return time.Now().UTC().Add(time.Second)
}

func waitCustomerSyncStatus(t *testing.T, ctx context.Context, service wecom.CustomerSyncService, runID int64, want wecom.CustomerSyncStatus) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		run, err := service.Get(ctx, runID)
		if err == nil && run.Status == want {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	run, err := service.Get(ctx, runID)
	t.Fatalf("customer sync status=%s err=%v; want %s", run.Status, err, want)
}

func customerSyncRuntimePool(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	raw, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured; skipping customer sync River PostgreSQL journey")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	config, err := pgxpool.ParseConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	random := make([]byte, 8)
	if _, err = rand.Read(random); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	schema := "customer_sync_runtime_" + hex.EncodeToString(random)
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	config = config.Copy()
	config.ConnConfig.RuntimeParams["search_path"] = schema
	native, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close()
		t.Fatal(err)
	}
	migrator, err := rivermigrate.New(riverpgxv5.New(native), nil)
	if err != nil {
		native.Close()
		admin.Close()
		t.Fatal(err)
	}
	if _, err = migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		native.Close()
		admin.Close()
		t.Fatal(err)
	}
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate customer sync runtime test")
	}
	base := filepath.Join(filepath.Dir(source), "..", "..", "migrations")
	for _, migration := range []string{"0001_platform.sql", "0002_identity.sql", "0003_access.sql", "0004_wecom.sql", "0005_external_effects.sql", "0009_customer_activation.sql", "0022_customer_profile_sections.sql", "0086_wecom_profile_primary_owner.sql", "0093_customer_tag_commands.sql", "0125_outbound_material_preparation.sql", "0153_wecom_customer_detail_projection.sql", "0170_wecom_contact_description_effect.sql", "0171_wecom_contact_description_source_coverage.sql"} {
		sql, readErr := os.ReadFile(filepath.Join(base, migration))
		if readErr != nil {
			native.Close()
			admin.Close()
			t.Fatal(readErr)
		}
		if _, execErr := native.Exec(ctx, string(sql)); execErr != nil {
			native.Close()
			admin.Close()
			t.Fatalf("apply %s: %v", migration, execErr)
		}
	}
	return native, func() {
		native.Close()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_, _ = admin.Exec(cleanupCtx, "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close()
	}
}
