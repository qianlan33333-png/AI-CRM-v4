package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/hxcdashboard/domain"
	hxcport "github.com/qianlan33333-png/AI-CRM-v3/internal/hxcdashboard/port"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func TestPublishRetainsReceiptLineageAfterEightProjections(t *testing.T) {
	native, uow, cleanup := hxcIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	store := NewPostgreSQL(native)

	var firstVersion int64
	for index := 1; index <= 9; index++ {
		var runID int64
		requestDigest := make([]byte, 32)
		requestDigest[31] = byte(index)
		if err := native.QueryRow(ctx, `INSERT INTO hxc_dashboard_refresh_runs(
			run_key,request_digest,trigger,identity_mode,status,source_count,processed_count,identity_replay_verified_count
		) VALUES($1,$2,'initial','apply','publishing',1,1,1) RETURNING id`, "retention-run-"+time.Unix(int64(index), 0).UTC().Format(time.RFC3339), requestDigest).Scan(&runID); err != nil {
			t.Fatal(err)
		}
		projection := hxcRetentionProjection(index)
		if err := uow.Within(ctx, func(txCtx context.Context) error {
			version, publishErr := store.Publish(txCtx, runID, projection)
			if index == 1 {
				firstVersion = version
			}
			return publishErr
		}); err != nil {
			t.Fatalf("publish projection %d: %v", index, err)
		}
	}
	if _, err := store.SharedFactsAtVersion(ctx, firstVersion, []customerdomain.CustomerID{1}); !errors.Is(err, hxcport.ErrSharedFactsVersionUnavailable) {
		t.Fatalf("pruned immutable generation must return a stable Port error, got %v", err)
	}

	var versions, receipts, retainedRows, oldestVersionRows int64
	if err := native.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM hxc_dashboard_versions),
		(SELECT count(*) FROM hxc_dashboard_refresh_runs WHERE status='succeeded' AND projection_id IS NOT NULL),
		(SELECT count(*) FROM hxc_dashboard_rows),
		(SELECT count(*) FROM hxc_dashboard_rows WHERE projection_id=(SELECT projection_id FROM hxc_dashboard_refresh_runs ORDER BY id LIMIT 1))
	`).Scan(&versions, &receipts, &retainedRows, &oldestVersionRows); err != nil {
		t.Fatal(err)
	}
	if versions != 9 || receipts != 9 || retainedRows != 8 || oldestVersionRows != 0 {
		t.Fatalf("versions=%d receipts=%d rows=%d oldest_rows=%d", versions, receipts, retainedRows, oldestVersionRows)
	}
}

func hxcRetentionProjection(index int) domain.Projection {
	sourceDigest, projectionDigest, subjectDigest := [32]byte{}, [32]byte{}, [32]byte{}
	sourceDigest[31], projectionDigest[31], subjectDigest[31] = byte(index), byte(index), byte(index)
	asOf := time.Date(2026, 9, 5, 0, 0, index, 0, time.UTC)
	return domain.Projection{
		AsOf: asOf, SourceDigest: sourceDigest, ProjectionDigest: projectionDigest,
		Counts: domain.Counts{Total: 1, RegisteredNoActiveMembership: 1, Unmatched: 1, PendingObservation: 1},
		Rows: []domain.ProjectionRow{{
			SubjectDigest: subjectDigest, UserRef: "HXC-000000000001", Stage: domain.RegisteredNoActiveMembership,
			SourceRow: domain.SourceRow{
				MembershipAttribution: "none", CapabilityUsage: []byte(`{}`), FocusTopics: []byte(`[]`), SourceUpdatedAt: asOf,
			},
			IdentityState: domain.Unmatched, MatchedBy: "none", IdentityReasonCode: "no_match",
		}},
	}
}

func hxcIntegrationPool(t *testing.T) (*pgxpool.Pool, *platformpostgres.UnitOfWork, func()) {
	t.Helper()
	databaseURL, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured; skipping PostgreSQL integration test")
	}
	ctx := context.Background()
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.NewWithConfig(ctx, config.Copy())
	if err != nil {
		t.Fatal(err)
	}
	var random [8]byte
	if _, err = rand.Read(random[:]); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	schema := "aicrm_hxc_test_" + hex.EncodeToString(random[:])
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	testConfig := config.Copy()
	testConfig.ConnConfig.RuntimeParams["search_path"] = schema
	native, err := pgxpool.NewWithConfig(ctx, testConfig)
	if err != nil {
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close()
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, `CREATE TABLE customers(id BIGINT PRIMARY KEY)`); err != nil {
		native.Close()
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close()
		t.Fatal(err)
	}
	for _, migration := range []string{"0028_hxc_dashboard.sql", "0064_hxc_dashboard_identity_v2.sql", "0084_hxc_shared_facts.sql", "0138_hxc_registration_coverage.sql"} {
		contents, readErr := os.ReadFile(hxcMigrationPath(t, migration))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, err = native.Exec(ctx, string(contents)); err != nil {
			t.Fatalf("apply %s: %v", migration, err)
		}
	}
	wrapped, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	return native, uow, func() {
		wrapped.Close()
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close()
	}
}

func hxcMigrationPath(t *testing.T, name string) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate HXC store integration test")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations", name))
}

func TestSharedFactsPublishedGenerationAndLegacyAvailability(t *testing.T) {
	native, uow, cleanup := hxcIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	store := NewPostgreSQL(native)
	if _, err := store.SharedFactsAtVersion(ctx, 99999, []domainCustomerID{77}); !errors.Is(err, hxcport.ErrSharedFactsVersionUnavailable) {
		t.Fatalf("unknown projection must return the stable Port error, got %v", err)
	}
	if _, err := native.Exec(ctx, `INSERT INTO customers(id) VALUES(77),(78)`); err != nil {
		t.Fatal(err)
	}
	publish := func(key string, projection domain.Projection) {
		var runID int64
		digest := make([]byte, 32)
		digest[0] = byte(len(key))
		if err := native.QueryRow(ctx, `INSERT INTO hxc_dashboard_refresh_runs(run_key,request_digest,trigger,identity_mode,status,source_count,processed_count,identity_replay_verified_count) VALUES($1,$2,'initial','apply','publishing',1,1,1) RETURNING id`, key, digest).Scan(&runID); err != nil {
			t.Fatal(err)
		}
		if err := uow.Within(ctx, func(tx context.Context) error { _, e := store.Publish(tx, runID, projection); return e }); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)
	future := now.Add(time.Hour)
	pastMembership := now.Add(-time.Second)
	login := now.Add(-48 * time.Hour)
	opened := now.Add(-time.Hour)
	used := now.Add(-2 * time.Hour)
	base := func(customer int64, available bool) domain.Projection {
		current, total := int64(3), int64(4)
		return domain.Projection{AsOf: now, SharedFactsAvailable: available, Counts: domain.Counts{Total: 1, ActiveUsed: 1, Matched: 1, MatchedByUnionID: 1}, Rows: []domain.ProjectionRow{{SubjectDigest: [32]byte{byte(customer)}, UserRef: "HXC-000000000077", Stage: domain.ActiveUsed, SourceRow: domain.SourceRow{SubscriptionTier: "standard", SubscriptionExpiresAt: &future, MembershipAttribution: "none", FormallyLoggedIn: true, FormalLoginAt: &login, HasTokenUsage: true, LearningPlanFound: true, LearningPlanStatus: "active", LearningPlanCurrent: &current, LearningPlanTotal: &total, CardOpenCount7D: 0, CardLastOpenedAt: &opened, MembershipRecordFound: true, IsMember: true, MembershipSource: "user_id", MembershipStatus: "active", MembershipExpiresAt: &pastMembership, LastUsedAt: &used, SourceUpdatedAt: now, CapabilityUsage: []byte(`{}`), FocusTopics: []byte(`[]`)}, CustomerID: domainCustomer(customer), IdentityState: domain.Matched, MatchedBy: "unionid", IdentityReasonCode: "matched_unionid"}}}
	}
	publish("shared-facts-available", base(77, true))
	pinnedVersion, err := store.CurrentSharedFactsVersion(ctx)
	if err != nil || pinnedVersion <= 0 {
		t.Fatalf("pin current shared facts generation: version=%d err=%v", pinnedVersion, err)
	}
	facts, err := store.SharedFacts(ctx, []domainCustomerID{77, 78})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := facts[77]
	if !ok || got.Availability != "available" || !got.FormallyLoggedIn || !got.HasTokenUsage || !got.LearningPlanFound || got.LearningPlanCurrent == nil || *got.LearningPlanCurrent != 3 || got.LearningPlanTotal == nil || *got.LearningPlanTotal != 4 || got.CardOpenCount7D != 0 || !got.IsMember || got.MembershipSource != "user_id" || got.MembershipStatus != "active" || !got.Registered || !got.HasRealUsage {
		t.Fatalf("available facts=%+v", got)
	}
	if got.ExpiresAt == nil || !got.ExpiresAt.Equal(pastMembership) || got.ActiveAt(now) || !got.ExpiredAt(now) {
		t.Fatalf("membership expiry must come from membership source, not subscription: %+v", got)
	}
	publish("shared-facts-legacy", base(78, false))
	currentVersion, err := store.CurrentSharedFactsVersion(ctx)
	if err != nil || currentVersion == pinnedVersion {
		t.Fatalf("new publication must advance the current generation: current=%d pinned=%d err=%v", currentVersion, pinnedVersion, err)
	}
	pinnedFacts, err := store.SharedFactsAtVersion(ctx, pinnedVersion, []domainCustomerID{77})
	if err != nil || pinnedFacts[77].Availability != "available" || pinnedFacts[77].MembershipSource != "user_id" {
		t.Fatalf("pinned generation must remain readable through bounded batches: facts=%+v err=%v", pinnedFacts[77], err)
	}
	facts, err = store.SharedFacts(ctx, []domainCustomerID{78})
	if err != nil {
		t.Fatal(err)
	}
	if got = facts[78]; got.Availability != "unavailable" {
		t.Fatalf("old generation must not claim zero/false facts: %+v", got)
	}

	// Publishing and retention run concurrently with a pinned consumer. Every
	// result must be the complete first generation or the explicit Port error;
	// a cleanup race must never surface as an empty successful map.
	readResults := make(chan error, 32)
	stopPinnedReads := make(chan struct{})
	pinnedReadsDone := make(chan struct{})
	go func() {
		defer close(pinnedReadsDone)
		for {
			select {
			case <-stopPinnedReads:
				return
			default:
			}
			facts, readErr := store.SharedFactsAtVersion(ctx, pinnedVersion, []domainCustomerID{77})
			if readErr == nil && (facts[77].Availability != hxcport.SharedFactsAvailable || facts[77].MembershipSource != "user_id") {
				readErr = fmt.Errorf("pinned read became incomplete: %+v", facts[77])
			}
			select {
			case readResults <- readErr:
			case <-stopPinnedReads:
				return
			}
		}
	}()
	for index := 0; index < 7; index++ {
		publish(fmt.Sprintf("shared-facts-rollover-%d", index), base(78, true))
	}
	close(stopPinnedReads)
	<-pinnedReadsDone
	close(readResults)
	for readErr := range readResults {
		if readErr != nil && !errors.Is(readErr, hxcport.ErrSharedFactsVersionUnavailable) {
			t.Fatal(readErr)
		}
	}
	if _, readErr := store.SharedFactsAtVersion(ctx, pinnedVersion, []domainCustomerID{77}); !errors.Is(readErr, hxcport.ErrSharedFactsVersionUnavailable) {
		t.Fatalf("pruned pinned generation must be unavailable, got %v", readErr)
	}
}

type domainCustomerID = customerdomain.CustomerID

func domainCustomer(value int64) customerdomain.CustomerID { return customerdomain.CustomerID(value) }
