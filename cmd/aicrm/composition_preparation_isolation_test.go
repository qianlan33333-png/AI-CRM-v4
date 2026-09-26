package main

import (
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	testconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config/tests"
)

func TestCompositionPreparationDatabaseIsolation(t *testing.T) {
	raw, _ := platformconfig.DatabaseURL()
	if raw == "" {
		t.Skip("requires local synthetic PostgreSQL")
	}
	directory := t.TempDir()
	if err := os.Chmod(directory, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AICRM_TEST_PREP_DIR", directory)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	first, cleanupFirst := preparedCompositionDatabase(t, ctx, raw)
	defer cleanupFirst()
	second, cleanupSecond := preparedCompositionDatabase(t, ctx, raw)
	defer cleanupSecond()
	if first == second {
		t.Fatal("composition clones must have distinct database identities")
	}
	one, err := pgxpool.New(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	defer one.Close()
	two, err := pgxpool.New(ctx, second)
	if err != nil {
		t.Fatal(err)
	}
	defer two.Close()
	// Each clone carries the canonical ledger, sequences and triggers. Only
	// migration-owned seed data is present; bootstrap/business seeds run later.
	var firstID, secondID int64
	for _, pool := range []*pgxpool.Pool{one, two} {
		var ledger, triggers, adminUsers int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM platform_schema_migrations").Scan(&ledger); err != nil {
			t.Fatal(err)
		}
		_, source, _, _ := runtime.Caller(0)
		files, _ := filepath.Glob(filepath.Join(filepath.Dir(source), "../../migrations/*.sql"))
		if ledger != len(files) {
			t.Fatalf("migration ledger count=%d expected=%d", ledger, len(files))
		}
		data, err := os.ReadFile(files[0])
		if err != nil {
			t.Fatal(err)
		}
		expected := sha256.Sum256(data)
		var checksum []byte
		if err = pool.QueryRow(ctx, "SELECT checksum FROM platform_schema_migrations WHERE name=$1", filepath.Base(files[0])).Scan(&checksum); err != nil {
			t.Fatal(err)
		}
		if string(checksum) != string(expected[:]) {
			t.Fatal("clone changed original migration checksum")
		}
		if err = pool.QueryRow(ctx, "SELECT count(*) FROM pg_trigger WHERE NOT tgisinternal").Scan(&triggers); err != nil || triggers == 0 {
			t.Fatalf("clone lost migration triggers: %v", err)
		}
		if err = pool.QueryRow(ctx, "SELECT count(*) FROM admin_users").Scan(&adminUsers); err != nil || adminUsers != 0 {
			t.Fatalf("template leaked bootstrap data: %d %v", adminUsers, err)
		}
		if _, err = pool.Exec(ctx, "CREATE TABLE clone_isolation(id bigserial PRIMARY KEY, value text)"); err != nil {
			t.Fatal(err)
		}
	}
	if err = one.QueryRow(ctx, "INSERT INTO clone_isolation(value) VALUES('first') RETURNING id").Scan(&firstID); err != nil {
		t.Fatal(err)
	}
	if err = two.QueryRow(ctx, "INSERT INTO clone_isolation(value) VALUES('second') RETURNING id").Scan(&secondID); err != nil {
		t.Fatal(err)
	}
	if firstID != 1 || secondID != 1 {
		t.Fatalf("clone sequences leaked: %d %d", firstID, secondID)
	}
	if _, err = one.Exec(ctx, "CREATE FUNCTION only_in_first() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'isolated'; END $$; CREATE TRIGGER only_in_first BEFORE INSERT ON clone_isolation FOR EACH ROW EXECUTE FUNCTION only_in_first()"); err != nil {
		t.Fatal(err)
	}
	if _, err = one.Exec(ctx, "INSERT INTO clone_isolation(value) VALUES('blocked')"); err == nil {
		t.Fatal("clone trigger did not execute")
	}
	if _, err = two.Exec(ctx, "INSERT INTO clone_isolation(value) VALUES('unaffected')"); err != nil {
		t.Fatal("trigger leaked between databases")
	}
	// Template cleanup must occur after clone cleanups and closed application pools.
	directory = testconfig.PreparationDirectory()
	t.Cleanup(func() {
		if err := cleanupCompositionDatabases(directory, raw); err != nil {
			t.Fatal(err)
		}
	})
}

func TestCompositionPreparationRejectsNonSyntheticDatabase(t *testing.T) {
	for _, raw := range []string{"postgres://role@10.0.4.6/aicrm_test_x", "postgres://role@localhost/aicrm", "postgres://role@localhost/production"} {
		if _, err := compositionSyntheticURL(raw); err == nil {
			t.Fatal("unsafe template URL accepted")
		}
	}
}
