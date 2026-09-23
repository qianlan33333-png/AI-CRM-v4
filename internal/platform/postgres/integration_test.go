package postgres_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/idempotency"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/webhook"
)

func TestPlatformPostgreSQLIntegration(t *testing.T) {
	pool, cleanup := integrationPool(t)
	defer cleanup()
	if err := pool.Check(context.Background()); err != nil {
		t.Fatalf("postgres readiness: %v", err)
	}
	unit, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	t.Run("read-only repeatable-read reporting unit of work", func(t *testing.T) {
		readUnit, unitErr := platformpostgres.NewReadOnlyRepeatableReadUnitOfWork(pool)
		if unitErr != nil {
			t.Fatal(unitErr)
		}
		if unitErr = readUnit.Within(ctx, func(txContext context.Context) error {
			var isolation, readOnly string
			tx, queryErr := platformpostgres.RequireTransaction(txContext)
			if queryErr != nil {
				return queryErr
			}
			if queryErr = tx.QueryRow(txContext, `SHOW transaction_isolation`).Scan(&isolation); queryErr != nil {
				return queryErr
			}
			if queryErr = tx.QueryRow(txContext, `SHOW transaction_read_only`).Scan(&readOnly); queryErr != nil {
				return queryErr
			}
			if isolation != "repeatable read" || readOnly != "on" {
				t.Fatalf("reporting transaction isolation=%q read_only=%q", isolation, readOnly)
			}
			return nil
		}); unitErr != nil {
			t.Fatal(unitErr)
		}
	})

	t.Run("unit of work commit rollback and nesting", func(t *testing.T) {
		auditService, serviceErr := audit.NewService(audit.NewPostgreSQLStore())
		if serviceErr != nil {
			t.Fatal(serviceErr)
		}
		committedKey, _ := idempotency.Parse("audit:integration:committed")
		if err := unit.Within(ctx, func(txContext context.Context) error {
			_, appendErr := auditService.Append(txContext, audit.Event{
				IdempotencyKey: committedKey,
				Action:         "platform.integration.committed",
				ActorType:      "test",
				ResourceType:   "platform",
			})
			return appendErr
		}); err != nil {
			t.Fatal(err)
		}

		rollbackKey, _ := idempotency.Parse("audit:integration:rollback")
		expected := errors.New("rollback requested")
		err := unit.Within(ctx, func(txContext context.Context) error {
			if _, appendErr := auditService.Append(txContext, audit.Event{
				IdempotencyKey: rollbackKey,
				Action:         "platform.integration.rolled_back",
				ActorType:      "test",
				ResourceType:   "platform",
			}); appendErr != nil {
				return appendErr
			}
			return expected
		})
		if !errors.Is(err, expected) {
			t.Fatalf("rollback error=%v", err)
		}

		if err = unit.Within(ctx, func(txContext context.Context) error {
			return unit.Within(txContext, func(context.Context) error { return nil })
		}); !errors.Is(err, platformpostgres.ErrNestedTransaction) {
			t.Fatalf("nested transaction error=%v", err)
		}

		var committed, rolledBack int
		if err = pool.Native().QueryRow(ctx, `
			SELECT
				count(*) FILTER (WHERE idempotency_key = $1),
				count(*) FILTER (WHERE idempotency_key = $2)
			FROM audit_events`, committedKey, rollbackKey).Scan(&committed, &rolledBack); err != nil {
			t.Fatal(err)
		}
		if committed != 1 || rolledBack != 0 {
			t.Fatalf("committed=%d rolledBack=%d", committed, rolledBack)
		}
	})

	t.Run("idempotency replay drift and skip locked claim", func(t *testing.T) {
		service, serviceErr := idempotency.NewService(idempotency.NewPostgreSQLStore())
		if serviceErr != nil {
			t.Fatal(serviceErr)
		}
		firstKey, _ := idempotency.Parse("idempotency:integration:first")
		secondKey, _ := idempotency.Parse("idempotency:integration:second")
		for _, key := range []idempotency.Key{firstKey, secondKey} {
			if err := unit.Within(ctx, func(txContext context.Context) error {
				_, beginErr := service.Begin(txContext, idempotency.Begin{
					Key: key, Payload: json.RawMessage(`{"operation":"send"}`),
				})
				return beginErr
			}); err != nil {
				t.Fatal(err)
			}
		}

		if err := unit.Within(ctx, func(txContext context.Context) error {
			result, beginErr := service.Begin(txContext, idempotency.Begin{
				Key: firstKey, Payload: json.RawMessage(`{ "operation": "send" }`),
			})
			if beginErr == nil && !result.Replay {
				return errors.New("expected replay")
			}
			return beginErr
		}); err != nil {
			t.Fatal(err)
		}
		if err := unit.Within(ctx, func(txContext context.Context) error {
			_, beginErr := service.Begin(txContext, idempotency.Begin{
				Key: firstKey, Payload: json.RawMessage(`{"operation":"refund"}`),
			})
			return beginErr
		}); !errors.Is(err, idempotency.ErrPayloadMismatch) {
			t.Fatalf("payload drift error=%v", err)
		}

		firstClaimed := make(chan idempotency.Key, 1)
		releaseFirst := make(chan struct{})
		claimError := make(chan error, 1)
		claimNow := time.Now().UTC().Add(time.Second)
		go func() {
			claimError <- unit.Within(ctx, func(txContext context.Context) error {
				claimed, claimErr := service.Claim(txContext, idempotency.Claim{
					Owner: "worker-one", Limit: 1, LeaseDuration: time.Minute, Now: claimNow,
				})
				if claimErr != nil {
					return claimErr
				}
				if len(claimed) != 1 {
					return errors.New("worker one expected one claim")
				}
				firstClaimed <- claimed[0].Key
				<-releaseFirst
				return nil
			})
		}()
		var lockedKey idempotency.Key
		select {
		case lockedKey = <-firstClaimed:
		case claimErr := <-claimError:
			t.Fatalf("worker one claim: %v", claimErr)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		var secondClaimed idempotency.Receipt
		if err := unit.Within(ctx, func(txContext context.Context) error {
			claimed, claimErr := service.Claim(txContext, idempotency.Claim{
				Owner: "worker-two", Limit: 1, LeaseDuration: time.Minute, Now: claimNow,
			})
			if claimErr != nil {
				return claimErr
			}
			if len(claimed) != 1 {
				return errors.New("worker two expected one claim")
			}
			secondClaimed = claimed[0]
			return nil
		}); err != nil {
			close(releaseFirst)
			t.Fatal(err)
		}
		close(releaseFirst)
		if err := <-claimError; err != nil {
			t.Fatal(err)
		}
		if lockedKey == secondClaimed.Key {
			t.Fatalf("two workers claimed %q", lockedKey)
		}

		if err := unit.Within(ctx, func(txContext context.Context) error {
			_, outcomeErr := service.RecordOutcome(txContext, idempotency.Outcome{
				Key:             secondClaimed.Key,
				Status:          idempotency.StatusExecuted,
				Response:        json.RawMessage(`{"accepted":true}`),
				ExpectedAttempt: secondClaimed.AttemptCount,
			})
			return outcomeErr
		}); err != nil {
			t.Fatal(err)
		}
		if err := unit.Within(ctx, func(txContext context.Context) error {
			claimed, claimErr := service.Claim(txContext, idempotency.Claim{
				Owner: "worker-three", Limit: 1, LeaseDuration: time.Minute,
				Now: claimNow.Add(2 * time.Minute),
			})
			if claimErr != nil {
				return claimErr
			}
			if len(claimed) != 1 || claimed[0].Key != lockedKey || claimed[0].AttemptCount != 2 {
				return errors.New("expired lease was not reclaimed with a new attempt")
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("webhook replay drift and transaction enforcement", func(t *testing.T) {
		service, serviceErr := webhook.NewService(webhook.NewPostgreSQLStore())
		if serviceErr != nil {
			t.Fatal(serviceErr)
		}
		key, _ := idempotency.Parse("webhook:integration:0001")
		input := webhook.Ingest{
			Provider:       "test-provider",
			IdempotencyKey: key,
			Payload:        json.RawMessage(`{"event":"created"}`),
		}
		if _, err := service.Ingest(ctx, input); !errors.Is(err, platformpostgres.ErrTransactionNeeded) {
			t.Fatalf("transaction enforcement error=%v", err)
		}
		if err := unit.Within(ctx, func(txContext context.Context) error {
			_, ingestErr := service.Ingest(txContext, input)
			return ingestErr
		}); err != nil {
			t.Fatal(err)
		}
		if err := unit.Within(ctx, func(txContext context.Context) error {
			result, ingestErr := service.Ingest(txContext, input)
			if ingestErr == nil && !result.Replay {
				return errors.New("expected webhook replay")
			}
			return ingestErr
		}); err != nil {
			t.Fatal(err)
		}
		drifted := input
		drifted.Payload = json.RawMessage(`{"event":"deleted"}`)
		if err := unit.Within(ctx, func(txContext context.Context) error {
			_, ingestErr := service.Ingest(txContext, drifted)
			return ingestErr
		}); !errors.Is(err, webhook.ErrPayloadMismatch) {
			t.Fatalf("webhook payload drift error=%v", err)
		}
		if err := unit.Within(ctx, func(txContext context.Context) error {
			otherKey, _ := idempotency.Parse("webhook:integration:other-provider")
			_, ingestErr := service.Ingest(txContext, webhook.Ingest{
				Provider: "other-provider", IdempotencyKey: otherKey,
				Payload: json.RawMessage(`{"event":"other"}`),
			})
			return ingestErr
		}); err != nil {
			t.Fatal(err)
		}
		if err := unit.Within(ctx, func(txContext context.Context) error {
			claimed, claimErr := service.Claim(txContext, webhook.Claim{
				Provider: "test-provider", Owner: "webhook-worker", Limit: 1, LeaseDuration: time.Minute,
				Now: time.Now().UTC().Add(time.Second),
			})
			if claimErr != nil {
				return claimErr
			}
			if len(claimed) != 1 || claimed[0].Status != webhook.StatusProcessing {
				return errors.New("expected one processing webhook")
			}
			_, completeErr := service.Complete(txContext, webhook.Completion{
				ID: claimed[0].ID, ExpectedAttempt: claimed[0].AttemptCount,
				Status: webhook.StatusProcessed,
			})
			return completeErr
		}); err != nil {
			t.Fatal(err)
		}
		if err := unit.Within(ctx, func(txContext context.Context) error {
			claimed, claimErr := service.Claim(txContext, webhook.Claim{
				Provider: "other-provider", Owner: "other-worker", Limit: 1, LeaseDuration: time.Minute,
				Now: time.Now().UTC().Add(time.Second),
			})
			if claimErr != nil {
				return claimErr
			}
			if len(claimed) != 1 || claimed[0].Provider != "other-provider" {
				return errors.New("provider-specific webhook claim crossed provider boundary")
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	})
}

func integrationPool(t *testing.T) (*platformpostgres.Pool, func()) {
	t.Helper()
	databaseURL, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("DATABASE_URL is not configured; skipping PostgreSQL integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	adminConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse DATABASE_URL: %v", err)
	}
	admin, err := pgxpool.NewWithConfig(ctx, adminConfig.Copy())
	if err != nil {
		t.Fatalf("open PostgreSQL integration database: %v", err)
	}
	if err = admin.Ping(ctx); err != nil {
		admin.Close()
		t.Fatalf("ping PostgreSQL integration database: %v", err)
	}

	random := make([]byte, 8)
	if _, err = rand.Read(random); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	schema := "aicrm_platform_test_" + hex.EncodeToString(random)
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		admin.Close()
		t.Fatalf("create PostgreSQL integration schema: %v", err)
	}

	testConfig := adminConfig.Copy()
	testConfig.ConnConfig.RuntimeParams["search_path"] = schema
	native, err := pgxpool.NewWithConfig(ctx, testConfig)
	if err != nil {
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close()
		t.Fatal(err)
	}
	migrationPath := platformMigrationPath(t)
	migrationSQL, err := os.ReadFile(migrationPath)
	if err != nil {
		native.Close()
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close()
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, string(migrationSQL)); err != nil {
		native.Close()
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close()
		t.Fatalf("apply platform integration migration: %v", err)
	}
	pool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return pool, func() {
		pool.Close()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_, _ = admin.Exec(cleanupCtx, "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close()
	}
}

func platformMigrationPath(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate integration test source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", "..", "..", "migrations", "0001_platform.sql"))
}
