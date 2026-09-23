package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	customerapp "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/app"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformoutbox "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/outbox"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// These test seams are intentionally unreachable for profile annotations;
// they make the application constructor explicit without granting a test a
// second Customer or Identity write path.
type sidebarProfileNoopPhones struct{}

func (sidebarProfileNoopPhones) AttachDeclaredPhoneToCustomer(context.Context, identityport.DeclaredPhoneCommand) (identityport.DeclaredAttachResult, error) {
	return identityport.DeclaredAttachResult{}, errors.New("phone attachment is outside profile annotation tests")
}

type sidebarProfileNoopProjection struct{}

func (sidebarProfileNoopProjection) UpsertDirectoryProjection(context.Context, customerport.DirectoryProjection) error {
	return errors.New("directory projection is outside profile annotation tests")
}
func (sidebarProfileNoopProjection) MarkDirectoryStale(context.Context, []customerdomain.CustomerID, time.Time) (int64, error) {
	return 0, errors.New("directory projection is outside profile annotation tests")
}
func (sidebarProfileNoopProjection) UpdateDirectoryPhone(context.Context, customerdomain.CustomerID, string, identitydomain.Assurance, int64, time.Time) error {
	return errors.New("phone attachment is outside profile annotation tests")
}
func (sidebarProfileNoopProjection) ClearDirectoryPhone(context.Context, customerdomain.CustomerID, time.Time) error {
	return errors.New("phone attachment is outside profile annotation tests")
}

type sidebarProfileFailingAudit struct{}

func (sidebarProfileFailingAudit) Append(context.Context, platformaudit.Event) (platformaudit.Event, error) {
	return platformaudit.Event{}, errors.New("forced audit failure")
}

func newSidebarProfilePostgreSQLService(t *testing.T, audit interface {
	Append(context.Context, platformaudit.Event) (platformaudit.Event, error)
}) (*customerapp.SidebarProfileApplication, *platformpostgres.Pool, func()) {
	t.Helper()
	url, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	pool, cleanup := tagCommandPGPool(t, ctx, url)
	root := filepath.Clean(filepath.Join("..", "..", ".."))
	for _, name := range []string{"0054_sidebar_customer_profile.sql", "0103_sidebar_customer_profile_annotations.sql"} {
		raw, readErr := os.ReadFile(filepath.Join(root, "migrations", name))
		if readErr != nil {
			cleanup()
			t.Fatal(readErr)
		}
		if _, execErr := pool.Native().Exec(ctx, string(raw)); execErr != nil {
			cleanup()
			t.Fatalf("apply %s: %v", name, execErr)
		}
	}
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		cleanup()
		t.Fatal(err)
	}
	service, err := customerapp.NewSidebarProfileApplication(uow, PostgreSQL{}, sidebarProfileNoopPhones{}, sidebarProfileNoopProjection{}, audit, platformoutbox.NewPostgreSQL())
	if err != nil {
		cleanup()
		t.Fatal(err)
	}
	return service, pool, cleanup
}

func seedSidebarProfileCustomer(t *testing.T, ctx context.Context, pool *platformpostgres.Pool, customerID int64, directoryVersion int64) {
	t.Helper()
	if _, err := pool.Native().Exec(ctx, `INSERT INTO customers(id,status) OVERRIDING SYSTEM VALUE VALUES($1,'active')`, customerID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Native().Exec(ctx, `INSERT INTO customer_directory_projection(customer_id,customer_status,display_name,activation_status,source,source_version,updated_at) VALUES($1,'active','Sidebar profile fixture','active','fixture_directory',$2,clock_timestamp())`, customerID, directoryVersion); err != nil {
		t.Fatal(err)
	}
}

func profileAnnotationCommand(customerID customerdomain.CustomerID, key string, expectedProfileVersion int64, source string) customerport.SidebarProfileUpdate {
	return customerport.SidebarProfileUpdate{
		CustomerID:             customerID,
		EmployeeID:             "sidebar-fixture-employee",
		ExpectedProfileVersion: expectedProfileVersion,
		IdempotencyKey:         key,
		ProfileSource:          source,
		SourceSet:              true,
	}
}

func TestSidebarProfilePostgreSQLAnnotationCASReplayAndFacts(t *testing.T) {
	ctx := context.Background()
	service, pool, cleanup := newSidebarProfilePostgreSQLService(t, platformaudit.NewPostgreSQLStore())
	defer cleanup()
	seedSidebarProfileCustomer(t, ctx, pool, 101, 7)

	firstCommand := profileAnnotationCommand(101, "sidebar-profile-first-replay", 0, "活动报名")
	first, err := service.UpdateSidebarProfile(ctx, firstCommand)
	if err != nil || first.ProfileVersion != 1 || first.Version != 7 || first.ProfileSource != "活动报名" {
		t.Fatalf("first annotation profile=%+v err=%v", first, err)
	}
	replayed, err := service.UpdateSidebarProfile(ctx, firstCommand)
	if err != nil || replayed != first {
		t.Fatalf("same receipt replay=%+v first=%+v err=%v", replayed, first, err)
	}
	second := firstCommand
	second.IdempotencyKey = "sidebar-profile-second-edit"
	second.ExpectedProfileVersion = 1
	second.SourceSet = false
	second.IndustrySet = true
	second.Industry = "软件"
	updated, err := service.UpdateSidebarProfile(ctx, second)
	if err != nil || updated.ProfileVersion != 2 || updated.Version != 7 || updated.ProfileSource != "活动报名" || updated.Industry != "软件" {
		t.Fatalf("subsequent annotation profile=%+v err=%v", updated, err)
	}

	var directoryVersion, profileVersion, receipts, audits, outbox int64
	if err = pool.Native().QueryRow(ctx, `SELECT source_version FROM customer_directory_projection WHERE customer_id=101`).Scan(&directoryVersion); err != nil || directoryVersion != 7 {
		t.Fatalf("directory version=%d err=%v", directoryVersion, err)
	}
	if err = pool.Native().QueryRow(ctx, `SELECT version FROM customer_sidebar_profiles WHERE customer_id=101`).Scan(&profileVersion); err != nil || profileVersion != 2 {
		t.Fatalf("profile version=%d err=%v", profileVersion, err)
	}
	if err = pool.Native().QueryRow(ctx, `SELECT count(*) FROM customer_sidebar_profile_receipts WHERE customer_id=101`).Scan(&receipts); err != nil || receipts != 2 {
		t.Fatalf("receipts=%d err=%v", receipts, err)
	}
	if err = pool.Native().QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE resource_id='101'`).Scan(&audits); err != nil || audits != 2 {
		t.Fatalf("audits=%d err=%v", audits, err)
	}
	if err = pool.Native().QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE aggregate_id='101'`).Scan(&outbox); err != nil || outbox != 2 {
		t.Fatalf("outbox=%d err=%v", outbox, err)
	}
	var payload []byte
	if err = pool.Native().QueryRow(ctx, `SELECT payload FROM audit_events WHERE resource_id='101' ORDER BY id DESC LIMIT 1`).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	var facts map[string]any
	if err = json.Unmarshal(payload, &facts); err != nil {
		t.Fatal(err)
	}
	if facts["change_kind"] != "sidebar_profile_annotations" || facts["profile_version"] != float64(2) || bytes.Contains(payload, []byte("软件")) {
		t.Fatalf("annotation facts=%s", payload)
	}
	fields, ok := facts["changed_fields"].([]any)
	if !ok || len(fields) != 1 || fields[0] != "industry" {
		t.Fatalf("changed fields=%#v", facts["changed_fields"])
	}
}

func TestSidebarProfilePostgreSQLReceiptReplayCanonicalizesNonNilTimes(t *testing.T) {
	ctx := context.Background()
	service, pool, cleanup := newSidebarProfilePostgreSQLService(t, platformaudit.NewPostgreSQLStore())
	defer cleanup()
	seedSidebarProfileCustomer(t, ctx, pool, 104, 9)

	lastSyncedAt := time.Date(2026, time.September, 8, 12, 34, 56, 123456000, time.FixedZone("fixture-offset", 8*60*60))
	if _, err := pool.Native().Exec(ctx, `UPDATE customer_directory_projection SET last_synced_at=$2 WHERE customer_id=$1`, 104, lastSyncedAt); err != nil {
		t.Fatal(err)
	}
	command := profileAnnotationCommand(104, "sidebar-profile-non-nil-time-replay", 0, "非空时间回放")
	first, err := service.UpdateSidebarProfile(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := service.UpdateSidebarProfile(ctx, command)
	if err != nil {
		t.Fatal(err)
	}

	var storedUpdatedAt time.Time
	if err = pool.Native().QueryRow(ctx, `SELECT GREATEST(d.updated_at,s.updated_at) FROM customer_directory_projection d JOIN customer_sidebar_profiles s USING(customer_id) WHERE d.customer_id=$1`, 104).Scan(&storedUpdatedAt); err != nil {
		t.Fatal(err)
	}
	if first.LastSyncedAt == nil || replayed.LastSyncedAt == nil || !first.LastSyncedAt.Equal(lastSyncedAt) || !replayed.LastSyncedAt.Equal(lastSyncedAt) {
		t.Fatalf("last synced first=%v replay=%v want=%v", first.LastSyncedAt, replayed.LastSyncedAt, lastSyncedAt)
	}
	if !first.UpdatedAt.Equal(storedUpdatedAt) || !replayed.UpdatedAt.Equal(storedUpdatedAt) {
		t.Fatalf("updated first=%v replay=%v stored=%v", first.UpdatedAt, replayed.UpdatedAt, storedUpdatedAt)
	}
	if first.LastSyncedAt.Location() != time.UTC || replayed.LastSyncedAt.Location() != time.UTC || first.UpdatedAt.Location() != time.UTC || replayed.UpdatedAt.Location() != time.UTC {
		t.Fatalf("non-canonical locations first_last=%v replay_last=%v first_updated=%v replay_updated=%v", first.LastSyncedAt.Location(), replayed.LastSyncedAt.Location(), first.UpdatedAt.Location(), replayed.UpdatedAt.Location())
	}
	if !reflect.DeepEqual(replayed, first) {
		t.Fatalf("complete replay mismatch replay=%+v first=%+v", replayed, first)
	}
}

func TestSidebarProfilePostgreSQLFirstWriteRaceAndAuditRollback(t *testing.T) {
	ctx := context.Background()
	service, pool, cleanup := newSidebarProfilePostgreSQLService(t, platformaudit.NewPostgreSQLStore())
	defer cleanup()
	seedSidebarProfileCustomer(t, ctx, pool, 102, 3)

	start := make(chan struct{})
	results := make(chan error, 2)
	for _, source := range []string{"来源甲", "来源乙"} {
		source := source
		go func() {
			<-start
			_, err := service.UpdateSidebarProfile(ctx, profileAnnotationCommand(102, "sidebar-profile-race-"+source, 0, source))
			results <- err
		}()
	}
	close(start)
	successes, conflicts := 0, 0
	for range 2 {
		err := <-results
		if err == nil {
			successes++
		} else if errors.Is(err, customerapp.ErrSidebarProfileConflict) {
			conflicts++
		} else {
			t.Fatalf("race error=%v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("first-write race successes=%d conflicts=%d", successes, conflicts)
	}
	var rows, version, directoryVersion int64
	if err := pool.Native().QueryRow(ctx, `SELECT count(*),coalesce(max(version),0) FROM customer_sidebar_profiles WHERE customer_id=102`).Scan(&rows, &version); err != nil || rows != 1 || version != 1 {
		t.Fatalf("race profile rows=%d version=%d err=%v", rows, version, err)
	}
	if err := pool.Native().QueryRow(ctx, `SELECT source_version FROM customer_directory_projection WHERE customer_id=102`).Scan(&directoryVersion); err != nil || directoryVersion != 3 {
		t.Fatalf("race directory version=%d err=%v", directoryVersion, err)
	}

	failing, failingPool, failingCleanup := newSidebarProfilePostgreSQLService(t, sidebarProfileFailingAudit{})
	defer failingCleanup()
	seedSidebarProfileCustomer(t, ctx, failingPool, 103, 5)
	_, err := failing.UpdateSidebarProfile(ctx, profileAnnotationCommand(103, "sidebar-profile-audit-rollback", 0, "不会提交"))
	if err == nil || !bytes.Contains([]byte(err.Error()), []byte("forced audit failure")) {
		t.Fatalf("audit failure err=%v", err)
	}
	var profiles, receipts, outboxRows int64
	if err = failingPool.Native().QueryRow(ctx, `SELECT count(*) FROM customer_sidebar_profiles WHERE customer_id=103`).Scan(&profiles); err != nil || profiles != 0 {
		t.Fatalf("rolled-back profiles=%d err=%v", profiles, err)
	}
	if err = failingPool.Native().QueryRow(ctx, `SELECT count(*) FROM customer_sidebar_profile_receipts WHERE customer_id=103`).Scan(&receipts); err != nil || receipts != 0 {
		t.Fatalf("rolled-back receipts=%d err=%v", receipts, err)
	}
	if err = failingPool.Native().QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE aggregate_id='103'`).Scan(&outboxRows); err != nil || outboxRows != 0 {
		t.Fatalf("rolled-back outbox=%d err=%v", outboxRows, err)
	}
}
