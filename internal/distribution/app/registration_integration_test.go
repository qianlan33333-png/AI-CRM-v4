package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
	distributionstore "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/store"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func TestPostgreSQLRegistrationReceiptReplaysAndRejectsAgreementDrift(t *testing.T) {
	pool, cleanup := registrationIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	service := registrationIntegrationService(t, pool)
	now := time.Date(2026, 9, 14, 16, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	command := registrationIntegrationCommand(now, "registration-pg-replay-key")

	first, err := service.Register(ctx, command)
	if err != nil || first.Distributor.ID < 1 {
		t.Fatalf("first registration profile=%+v err=%v", first, err)
	}
	second, err := service.Register(ctx, command)
	if err != nil || second.Distributor.ID != first.Distributor.ID || second.Distributor.PublicNo != first.Distributor.PublicNo {
		t.Fatalf("replayed registration profile=%+v first=%+v err=%v", second, first, err)
	}
	assertRegistrationFacts(t, ctx, pool, command.Actor.CustomerID, command.IdempotencyKey, 1, 1, 1, 1)

	if _, err = pool.Exec(ctx, `UPDATE distribution_agreements SET active=FALSE WHERE active=TRUE`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO distribution_agreements(version,content,active,created_at) VALUES('v2','更新后的推广协议',TRUE,$1)`, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	drift := command
	drift.AgreementVersion = "v2"
	if _, err = service.Register(ctx, drift); !errors.Is(err, distributionport.ErrConflict) {
		t.Fatalf("agreement payload drift error=%v", err)
	}
	assertRegistrationFacts(t, ctx, pool, command.Actor.CustomerID, command.IdempotencyKey, 1, 1, 1, 1)
}

func TestPostgreSQLRegistrationConcurrentSameKeyCreatesOneFactSet(t *testing.T) {
	pool, cleanup := registrationIntegrationPool(t)
	defer cleanup()
	service := registrationIntegrationService(t, pool)
	now := time.Date(2026, 9, 14, 16, 5, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	command := registrationIntegrationCommand(now, "registration-pg-concurrent-key")

	start := make(chan struct{})
	profiles := make(chan distributionport.DistributorProfile, 2)
	errs := make(chan error, 2)
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			profile, err := service.Register(context.Background(), command)
			profiles <- profile
			errs <- err
		}()
	}
	close(start)
	wait.Wait()
	close(profiles)
	close(errs)
	var ids []int64
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent registration error=%v", err)
		}
	}
	for profile := range profiles {
		ids = append(ids, profile.Distributor.ID)
	}
	if len(ids) != 2 || ids[0] < 1 || ids[0] != ids[1] {
		t.Fatalf("concurrent registrations did not replay one distributor: ids=%v", ids)
	}
	assertRegistrationFacts(t, context.Background(), pool, command.Actor.CustomerID, command.IdempotencyKey, 1, 1, 1, 1)
}

func TestPostgreSQLRegistrationSameBrowserKeyDifferentCustomersKeepsBothOutboxFacts(t *testing.T) {
	pool, cleanup := registrationIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	service := registrationIntegrationService(t, pool)
	now := time.Date(2026, 9, 14, 16, 10, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	first := registrationIntegrationCommand(now, "registration-shared-browser-key")
	second := first
	second.Actor.CustomerID = 1702
	second.Actor.IdentityID = 2702
	if _, err := service.Register(ctx, first); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Register(ctx, second); err != nil {
		t.Fatal(err)
	}
	assertRegistrationFacts(t, ctx, pool, first.Actor.CustomerID, first.IdempotencyKey, 1, 1, 1, 1)
	assertRegistrationFacts(t, ctx, pool, second.Actor.CustomerID, second.IdempotencyKey, 1, 1, 1, 1)
	var outbox int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM distribution_outbox WHERE event_type='distribution.distributor_registered.v1'`).Scan(&outbox); err != nil || outbox != 2 {
		t.Fatalf("same browser key lost a customer outbox fact count=%d err=%v", outbox, err)
	}
}

func registrationIntegrationService(t *testing.T, pool *pgxpool.Pool) *RegistrationService {
	t.Helper()
	wrapped, err := platformpostgres.Wrap(pool, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(wrapped.Close)
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := distributionstore.NewPostgreSQL(pool, uow)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewRegistrationService(uow, repository, &settlementPaymentStub{})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func registrationIntegrationCommand(now time.Time, idempotencyKey string) distributionport.RegisterCommand {
	return distributionport.RegisterCommand{
		Actor:            distributionport.TrustedSessionActor{CustomerID: 1701, IdentityID: 2701, AppID: "wx-registration", AppScope: "wechat-app:wx-registration", Channel: "mini_program", OccurredAt: now},
		AgreementVersion: "v1",
		IdempotencyKey:   idempotencyKey,
	}
}

func assertRegistrationFacts(t *testing.T, ctx context.Context, pool *pgxpool.Pool, customerID int64, idempotencyKey string, distributors, receipts, audits, outbox int) {
	t.Helper()
	actorScope := "customer:" + decimal(customerID)
	assertRegistrationCount(t, ctx, pool, `SELECT count(*) FROM distribution_distributors WHERE customer_id=$1`, distributors, customerID)
	assertRegistrationCount(t, ctx, pool, `SELECT count(*) FROM distribution_operation_receipts WHERE operation='register' AND actor_scope=$1 AND key_digest=$2`, receipts, actorScope, registrationIdempotencyDigest(idempotencyKey))
	assertRegistrationCount(t, ctx, pool, `SELECT count(*) FROM distribution_audit_events WHERE event_type='distribution.distributor_registered.v1' AND actor_scope=$1`, audits, actorScope)
	assertRegistrationCount(t, ctx, pool, `SELECT count(*) FROM distribution_outbox WHERE event_type='distribution.distributor_registered.v1' AND idempotency_key=$1`, outbox, registrationOutboxKey(actorScope, idempotencyKey))
}

func assertRegistrationCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, query string, want int, args ...any) {
	t.Helper()
	var got int
	if err := pool.QueryRow(ctx, query, args...).Scan(&got); err != nil || got != want {
		t.Fatalf("registration facts query=%q got=%d want=%d err=%v", query, got, want, err)
	}
}

func registrationIdempotencyDigest(key string) []byte {
	digest := sha256.Sum256([]byte(key))
	return digest[:]
}

func registrationIntegrationPool(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	databaseURL, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured; skipping registration PostgreSQL integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	adminConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.NewWithConfig(ctx, adminConfig)
	if err != nil {
		t.Fatal(err)
	}
	var random [8]byte
	if _, err = rand.Read(random[:]); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	schema := "aicrm_distribution_registration_" + hex.EncodeToString(random[:])
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	config := adminConfig.Copy()
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close()
		t.Fatal(err)
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		pool.Close()
		admin.Close()
		t.Fatal("locate distribution migrations")
	}
	root := filepath.Join(filepath.Dir(file), "..", "..", "..")
	for _, name := range []string{"0003_access.sql", "0157_distribution_core.sql", "0159_distribution_admin_receipts.sql"} {
		body, readErr := os.ReadFile(filepath.Join(root, "migrations", name))
		if readErr != nil {
			pool.Close()
			admin.Close()
			t.Fatal(readErr)
		}
		if _, execErr := pool.Exec(ctx, string(body)); execErr != nil {
			pool.Close()
			admin.Close()
			t.Fatalf("apply %s: %v", name, execErr)
		}
	}
	return pool, func() {
		pool.Close()
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_, _ = admin.Exec(cleanup, "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close()
	}
}
