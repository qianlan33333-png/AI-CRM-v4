package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	customerstore "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/store"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identitystore "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/store"
	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformoutbox "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/outbox"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/wecom"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

type integrationDirectoryProvider struct{}

type integrationDirectoryFailure struct {
	code      string
	retryable bool
}

func (failure integrationDirectoryFailure) Error() string                   { return failure.code }
func (failure integrationDirectoryFailure) DirectoryFailureCode() string    { return failure.code }
func (failure integrationDirectoryFailure) DirectoryFailureRetryable() bool { return failure.retryable }

type integrationFailingDirectoryProvider struct {
	listErr  error
	batchErr error
}

// pagedIntegrationDirectoryProvider models the actual get_by_user traversal:
// one employee spans two pages and a second employee sees the same customer.
// The second employee may transiently fail, so the journey proves that no
// primary owner is published from the incomplete traversal.
type pagedIntegrationDirectoryProvider struct {
	mu         sync.Mutex
	failStaffB bool
}

type integrationSyncEnqueuer struct{ runID int64 }

func (enqueuer *integrationSyncEnqueuer) EnqueueCustomerSync(_ context.Context, runID int64) error {
	enqueuer.runID = runID
	return nil
}

func (integrationDirectoryProvider) DirectoryReady() bool { return true }
func (integrationDirectoryProvider) ListContactStaff(context.Context) ([]string, error) {
	return []string{"staff-integration"}, nil
}
func (integrationDirectoryProvider) BatchExternalContacts(context.Context, string, string, int) (wecomport.ExternalContactPage, error) {
	return wecomport.ExternalContactPage{Contacts: []wecomport.ExternalContact{{ExternalUserID: "external-integration", Name: "Integration Customer", Gender: 1, Type: 1, CorpName: "Integration Corp",
		FollowInfo: []wecomport.ExternalContactFollowInfo{{EmployeeID: "staff-integration", Tags: []wecomport.ExternalContactTag{{ProviderTagID: "provider-tag", Name: "重点客户", Type: 1}}}}}}}, nil
}

func (provider integrationFailingDirectoryProvider) DirectoryReady() bool { return true }
func (provider integrationFailingDirectoryProvider) ListContactStaff(context.Context) ([]string, error) {
	return nil, provider.listErr
}
func (provider integrationFailingDirectoryProvider) BatchExternalContacts(context.Context, string, string, int) (wecomport.ExternalContactPage, error) {
	return wecomport.ExternalContactPage{}, provider.batchErr
}

func (*pagedIntegrationDirectoryProvider) DirectoryReady() bool { return true }
func (*pagedIntegrationDirectoryProvider) ListContactStaff(context.Context) ([]string, error) {
	return []string{"staff-a", "staff-b"}, nil
}
func (provider *pagedIntegrationDirectoryProvider) BatchExternalContacts(_ context.Context, staffID, cursor string, _ int) (wecomport.ExternalContactPage, error) {
	contact := func(owner string) wecomport.ExternalContact {
		return wecomport.ExternalContact{ExternalUserID: "external-paged", Name: "Paged Customer", Gender: 1, Type: 1, CorpName: "Integration Corp", FollowInfo: []wecomport.ExternalContactFollowInfo{{EmployeeID: owner}}}
	}
	switch {
	case staffID == "staff-a" && cursor == "":
		return wecomport.ExternalContactPage{Contacts: []wecomport.ExternalContact{contact("zara")}, NextCursor: "staff-a-page-2"}, nil
	case staffID == "staff-a" && cursor == "staff-a-page-2":
		return wecomport.ExternalContactPage{}, nil
	case staffID == "staff-b" && cursor == "":
		provider.mu.Lock()
		defer provider.mu.Unlock()
		if provider.failStaffB {
			return wecomport.ExternalContactPage{}, errors.New("temporary provider failure")
		}
		return wecomport.ExternalContactPage{Contacts: []wecomport.ExternalContact{contact("bob")}}, nil
	default:
		return wecomport.ExternalContactPage{}, errors.New("unexpected paged directory request")
	}
}

func (provider *pagedIntegrationDirectoryProvider) recoverStaffB() {
	provider.mu.Lock()
	provider.failStaffB = false
	provider.mu.Unlock()
}

func TestCustomerSyncJourneyPostgreSQL(t *testing.T) {
	databaseURL, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("DATABASE_URL is not configured; skipping customer sync integration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.NewWithConfig(ctx, config.Copy())
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	random := make([]byte, 8)
	if _, err = rand.Read(random); err != nil {
		t.Fatal(err)
	}
	schema := "aicrm_customer_sync_" + hex.EncodeToString(random)
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(context.Background(), "DROP SCHEMA "+identifier+" CASCADE")
	testConfig := config.Copy()
	testConfig.ConnConfig.RuntimeParams["search_path"] = schema
	native, err := pgxpool.NewWithConfig(ctx, testConfig)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join("..", "..")
	for _, name := range []string{"0001_platform.sql", "0002_identity.sql", "0003_access.sql", "0004_wecom.sql", "0005_external_effects.sql", "0009_customer_activation.sql", "0022_customer_profile_sections.sql", "0086_wecom_profile_primary_owner.sql", "0093_customer_tag_commands.sql", "0153_wecom_customer_detail_projection.sql", "0170_wecom_contact_description_effect.sql", "0171_wecom_contact_description_source_coverage.sql"} {
		raw, readErr := os.ReadFile(filepath.Join(root, "migrations", name))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, err = native.Exec(ctx, string(raw)); err != nil {
			t.Fatalf("migration %s: %v", name, err)
		}
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
	customerStore := customerstore.NewPostgreSQL()
	enqueuer := &integrationSyncEnqueuer{}
	service := wecom.CustomerSyncService{Enabled: true, CorpID: "integration-corp", Provider: integrationDirectoryProvider{}, Identity: identityapp.OneIDService{Store: identitystore.NewPostgresStore()}, Projection: customerStore, Timeline: customerStore, Store: wecom.NewPostgreSQLCustomerSyncStore(), Outbox: platformoutbox.NewPostgreSQL(), Enqueuer: enqueuer, Audit: audit, UOW: uow, Now: func() time.Time { return time.Now().UTC() }}
	run, _, err := service.CreateScheduled(ctx, "initial", "initial:integration-customer-sync")
	if err != nil {
		t.Fatal(err)
	}
	if enqueuer.runID != run.ID {
		t.Fatalf("queued run=%d, want %d", enqueuer.runID, run.ID)
	}
	worker := wecom.NewCustomerSyncWorker()
	if err = worker.BindService(service); err != nil {
		t.Fatal(err)
	}
	if err = worker.Work(ctx, &river.Job[wecom.CustomerSyncJobArgs]{JobRow: &rivertype.JobRow{Attempt: 1, MaxAttempts: 12}, Args: wecom.CustomerSyncJobArgs{RunID: run.ID}}); err != nil {
		t.Fatal(err)
	}
	run, err = service.Get(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != wecom.SyncSucceeded || run.Discovered != 1 || run.Activated != 1 || run.Projected != 1 {
		t.Fatalf("run=%+v", run)
	}
	var identities, projections, receipts, pending, owners, tags, timeline int
	if err = pool.Native().QueryRow(ctx, `SELECT (SELECT count(*) FROM customer_identities WHERE kind='wecom_external_userid' AND assurance='verified'),
		(SELECT count(*) FROM customer_directory_projection),(SELECT count(*) FROM wecom_customer_sync_items WHERE run_id=$1),
		(SELECT count(*) FROM outbox_events WHERE processed_at IS NULL),(SELECT count(*) FROM wecom_customer_owner_observations WHERE last_seen_run_id=$1),
		(SELECT count(*) FROM wecom_customer_tag_observations WHERE last_seen_run_id=$1),(SELECT count(*) FROM customer_timeline_projection WHERE source_domain='wecom')`, run.ID).Scan(&identities, &projections, &receipts, &pending, &owners, &tags, &timeline); err != nil {
		t.Fatal(err)
	}
	if identities != 1 || projections != 1 || receipts != 1 || pending != 0 || owners != 1 || tags != 1 || timeline != 1 {
		t.Fatalf("identities=%d projections=%d receipts=%d pending=%d owners=%d tags=%d timeline=%d", identities, projections, receipts, pending, owners, tags, timeline)
	}
	var primaryOwner string
	var primaryRunID int64
	if err = pool.Native().QueryRow(ctx, `SELECT primary_owner_userid,primary_owner_run_id FROM wecom_external_contact_profiles`).Scan(&primaryOwner, &primaryRunID); err != nil {
		t.Fatal(err)
	}
	if primaryOwner != "staff-integration" || primaryRunID != run.ID {
		t.Fatalf("primary owner=%q run=%d, want staff-integration/%d", primaryOwner, primaryRunID, run.ID)
	}

	var recoveryRunID int64
	if err = pool.Native().QueryRow(ctx, `INSERT INTO wecom_customer_sync_runs(run_key,trigger_type,status,corp_scope,staff_ids,staff_index,provider_cursor,started_at)
		VALUES('manual:integration-recovery','manual','fetching_profiles','wecom-corp:integration-corp','["staff-integration"]',0,'cursor-committed',clock_timestamp()) RETURNING id`).Scan(&recoveryRunID); err != nil {
		t.Fatal(err)
	}
	syncStore := wecom.NewPostgreSQLCustomerSyncStore()
	var recovery wecom.CustomerSyncRun
	if err = uow.Within(ctx, func(txContext context.Context) error {
		var loadErr error
		recovery, loadErr = syncStore.Get(txContext, recoveryRunID)
		if loadErr != nil {
			return loadErr
		}
		return syncStore.Fail(txContext, recovery.ID, recovery.Version, wecom.SyncFailedRetryable, "provider_unavailable")
	}); err != nil {
		t.Fatal(err)
	}
	if err = uow.Within(ctx, func(txContext context.Context) error {
		var loadErr error
		recovery, loadErr = syncStore.Get(txContext, recoveryRunID)
		if loadErr != nil {
			return loadErr
		}
		return syncStore.Transition(txContext, recovery.ID, recovery.Version, recovery.Status, recovery.ResumeStatus)
	}); err != nil {
		t.Fatal(err)
	}
	if err = uow.Within(ctx, func(txContext context.Context) error {
		var loadErr error
		recovery, loadErr = syncStore.Get(txContext, recoveryRunID)
		return loadErr
	}); err != nil {
		t.Fatal(err)
	}
	if recovery.Status != wecom.SyncFetchingProfiles || recovery.ProviderCursor != "cursor-committed" || recovery.StaffIndex != 0 {
		t.Fatalf("recovery did not preserve cursor: %+v", recovery)
	}
	// The recovery assertion intentionally leaves the persisted cursor in an
	// active state. End that isolated run before creating independent failure
	// journeys: production permits only one active run per corporation.
	if err = uow.Within(ctx, func(txContext context.Context) error {
		return syncStore.Terminate(txContext, recovery.ID, "test_recovery_complete")
	}); err != nil {
		t.Fatal(err)
	}

	assertFailureRun := func(name string, provider wecomport.DirectoryProvider, attempt, maxAttempts int, wantStatus wecom.CustomerSyncStatus, wantCode string) {
		t.Helper()
		failureService := service
		failureService.Provider = provider
		failureRun, _, createErr := failureService.CreateScheduled(ctx, "initial", "initial:integration-failure-"+name)
		if createErr != nil {
			t.Fatal(createErr)
		}
		failureWorker := wecom.NewCustomerSyncWorker()
		if bindErr := failureWorker.BindService(failureService); bindErr != nil {
			t.Fatal(bindErr)
		}
		if workErr := failureWorker.Work(ctx, &river.Job[wecom.CustomerSyncJobArgs]{JobRow: &rivertype.JobRow{Attempt: attempt, MaxAttempts: maxAttempts}, Args: wecom.CustomerSyncJobArgs{RunID: failureRun.ID}}); workErr != nil && wantStatus == wecom.SyncFailedTerminal {
			t.Fatalf("name=%s terminal work err=%v", name, workErr)
		}
		failureRun, getErr := failureService.Get(ctx, failureRun.ID)
		if getErr != nil || failureRun.Status != wantStatus || failureRun.LastErrorCode != wantCode {
			t.Fatalf("name=%s get=%v run=%+v", name, getErr, failureRun)
		}
	}
	assertFailureRun("disabled", integrationFailingDirectoryProvider{listErr: wecomport.ErrDirectoryDisabled}, 1, 12, wecom.SyncFailedTerminal, "provider_disabled")
	assertFailureRun("permission", integrationFailingDirectoryProvider{listErr: integrationDirectoryFailure{code: "provider_permission_denied"}}, 1, 12, wecom.SyncFailedTerminal, "provider_permission_denied")

	var retryRunID int64
	if _, err = pool.Native().Exec(ctx, `INSERT INTO wecom_customer_sync_runs(run_key,trigger_type,status,corp_scope,staff_ids,staff_index,provider_cursor,started_at)
		VALUES('initial:integration-rate-limited','initial','fetching_profiles','wecom-corp:integration-corp','["staff-integration"]',0,'cursor-preserved',clock_timestamp())`); err != nil {
		t.Fatal(err)
	}
	if err = pool.Native().QueryRow(ctx, `SELECT id FROM wecom_customer_sync_runs WHERE run_key='initial:integration-rate-limited'`).Scan(&retryRunID); err != nil {
		t.Fatal(err)
	}
	retryService := service
	retryService.Provider = integrationFailingDirectoryProvider{batchErr: integrationDirectoryFailure{code: "provider_rate_limited", retryable: true}}
	retryWorker := wecom.NewCustomerSyncWorker()
	if err = retryWorker.BindService(retryService); err != nil {
		t.Fatal(err)
	}
	if err = retryWorker.Work(ctx, &river.Job[wecom.CustomerSyncJobArgs]{JobRow: &rivertype.JobRow{Attempt: 1, MaxAttempts: 12}, Args: wecom.CustomerSyncJobArgs{RunID: retryRunID}}); err == nil {
		t.Fatal("rate-limited worker must return a retryable River error")
	}
	retryRun, err := retryService.Get(ctx, retryRunID)
	if err != nil || retryRun.Status != wecom.SyncFailedRetryable || retryRun.LastErrorCode != "provider_rate_limited" || retryRun.ProviderCursor != "cursor-preserved" {
		t.Fatalf("retryable get=%v run=%+v", err, retryRun)
	}
	if err = retryWorker.Work(ctx, &river.Job[wecom.CustomerSyncJobArgs]{JobRow: &rivertype.JobRow{Attempt: 12, MaxAttempts: 12}, Args: wecom.CustomerSyncJobArgs{RunID: retryRunID}}); err != nil {
		t.Fatalf("exhausted worker err=%v", err)
	}
	retryRun, err = retryService.Get(ctx, retryRunID)
	if err != nil || retryRun.Status != wecom.SyncFailedTerminal || retryRun.LastErrorCode != "retry_exhausted:provider_rate_limited" || retryRun.ProviderCursor != "cursor-preserved" {
		t.Fatalf("exhausted get=%v run=%+v", err, retryRun)
	}
	var successfulProfiles int
	if err = pool.Native().QueryRow(ctx, `SELECT count(*) FROM wecom_external_contact_profiles WHERE activation_status='active'`).Scan(&successfulProfiles); err != nil || successfulProfiles != 1 {
		t.Fatalf("successful profile changed after failed rounds: count=%d err=%v", successfulProfiles, err)
	}

	// Run the real River worker through two pages and two employees. A failure
	// after the first employee commits must retain that relationship without
	// publishing a partial primary; retrying the same durable run then derives
	// the provider's lexicographically first owner from its complete scope.
	pagedProvider := &pagedIntegrationDirectoryProvider{failStaffB: true}
	pagedService := service
	pagedService.Provider = pagedProvider
	pagedRun, _, err := pagedService.CreateScheduled(ctx, "initial", "initial:integration-paged-sync")
	if err != nil {
		t.Fatal(err)
	}
	pagedWorker := wecom.NewCustomerSyncWorker()
	if err = pagedWorker.BindService(pagedService); err != nil {
		t.Fatal(err)
	}
	if err = pagedWorker.Work(ctx, &river.Job[wecom.CustomerSyncJobArgs]{JobRow: &rivertype.JobRow{Attempt: 1, MaxAttempts: 12}, Args: wecom.CustomerSyncJobArgs{RunID: pagedRun.ID}}); err == nil {
		t.Fatal("paged provider failure did not leave the run retryable")
	}
	pagedRun, err = pagedService.Get(ctx, pagedRun.ID)
	if err != nil || pagedRun.Status != wecom.SyncFailedRetryable || pagedRun.StaffIndex != 1 {
		t.Fatalf("partial paged run=%+v err=%v", pagedRun, err)
	}
	var partialPrimary string
	var retainedOwners int
	if err = pool.Native().QueryRow(ctx, `SELECT COALESCE(profile.primary_owner_userid,''),count(observation.employee_id)
		FROM wecom_external_contact_profiles profile
		JOIN customer_identities identity ON identity.id=profile.external_identity_id
		LEFT JOIN wecom_customer_owner_observations observation ON observation.customer_id=profile.customer_id AND observation.corp_scope=profile.corp_scope AND observation.relationship_status='active'
		WHERE identity.normalized_value='external-paged'
		GROUP BY profile.primary_owner_userid`).Scan(&partialPrimary, &retainedOwners); err != nil || partialPrimary != "" || retainedOwners != 1 {
		t.Fatalf("partial primary=%q retained owners=%d err=%v", partialPrimary, retainedOwners, err)
	}
	pagedProvider.recoverStaffB()
	if err = pagedWorker.Work(ctx, &river.Job[wecom.CustomerSyncJobArgs]{JobRow: &rivertype.JobRow{Attempt: 2, MaxAttempts: 12}, Args: wecom.CustomerSyncJobArgs{RunID: pagedRun.ID}}); err != nil {
		t.Fatal(err)
	}
	pagedRun, err = pagedService.Get(ctx, pagedRun.ID)
	if err != nil || pagedRun.Status != wecom.SyncSucceeded {
		t.Fatalf("recovered paged run=%+v err=%v", pagedRun, err)
	}
	var completedPrimary string
	if err = pool.Native().QueryRow(ctx, `SELECT COALESCE(profile.primary_owner_userid,''),count(observation.employee_id)
		FROM wecom_external_contact_profiles profile
		JOIN customer_identities identity ON identity.id=profile.external_identity_id
		LEFT JOIN wecom_customer_owner_observations observation ON observation.customer_id=profile.customer_id AND observation.corp_scope=profile.corp_scope AND observation.relationship_status='active'
		WHERE identity.normalized_value='external-paged'
		GROUP BY profile.primary_owner_userid`).Scan(&completedPrimary, &retainedOwners); err != nil || completedPrimary != "bob" || retainedOwners != 2 {
		t.Fatalf("completed primary=%q owners=%d err=%v", completedPrimary, retainedOwners, err)
	}
}
