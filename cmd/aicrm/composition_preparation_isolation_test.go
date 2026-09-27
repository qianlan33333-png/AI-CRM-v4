package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
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
	t.Cleanup(func() {
		if err := cleanupCompositionDatabases(directory, raw); err != nil {
			t.Error(err)
		}
	})
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
}

func TestCompositionPreparationCleanupUsesCreatingConnection(t *testing.T) {
	raw, _ := platformconfig.DatabaseURL()
	if raw == "" {
		t.Skip("requires local synthetic PostgreSQL")
	}
	directory := t.TempDir()
	if err := os.Chmod(directory, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AICRM_TEST_PREP_DIR", directory)
	t.Cleanup(func() {
		if err := cleanupCompositionDatabases(directory, raw); err != nil {
			t.Error(err)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	_, cleanupClone := preparedCompositionDatabase(t, ctx, raw)
	cleanupClone()
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	// General fixture inventories share the attempt with composition templates.
	fixture, err := testconfig.NewDatabaseName()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{fixture}.Sanitize()+" TEMPLATE template0"); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(directory, "database-*.json"))
	if err != nil || len(files) != 2 {
		t.Fatalf("expected a template and general fixture registration: %v", err)
	}
	var names []string
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), raw) {
			t.Fatal("database connection escaped into preparation inventory")
		}
		var record compositionDatabasePreparation
		if err = json.Unmarshal(data, &record); err != nil {
			t.Fatal(err)
		}
		names = append(names, record.Database)
	}
	// This is the installed smoke boundary: the actual connection came from a
	// private input, while no global database URL exists at process teardown.
	t.Setenv("AICRM_DATABASE_URL", "")
	if err = cleanupCompositionDatabases(directory, ""); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		var exists bool
		if err = admin.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname=$1)", name).Scan(&exists); err != nil || exists {
			t.Fatalf("registered database remained after cleanup: %v", err)
		}
	}
	files, err = filepath.Glob(filepath.Join(directory, "database-*.json"))
	if err != nil || len(files) != 0 {
		t.Fatal("completed cleanup retained database registrations")
	}
}

func TestCompositionPreparationCleanupRejectsChangedInventory(t *testing.T) {
	directory := t.TempDir()
	if err := os.Chmod(directory, 0700); err != nil {
		t.Fatal(err)
	}
	fingerprint := strings.Repeat("a", 64)
	raw := "postgres://fixture@localhost/aicrm_test_cleanup"
	if err := rememberCompositionCleanupContext(directory, raw, fingerprint); err != nil {
		t.Fatal(err)
	}
	if err := rememberCompositionCleanupContext(directory, "postgres://other@localhost/aicrm_test_cleanup", fingerprint); err == nil {
		t.Fatal("preparation accepted a different connection identity")
	}
	first, err := writeCompositionPreparation(directory, compositionDatabasePreparation{Database: "aicrm_test_tpl_0000000000000000_acceptance_test", Fingerprint: fingerprint})
	if err != nil {
		t.Fatal(err)
	}
	second, err := writeCompositionPreparation(directory, compositionDatabasePreparation{Database: "aicrm_test_tpl_1111111111111111_acceptance_test", Fingerprint: strings.Repeat("b", 64)})
	if err != nil {
		t.Fatal(err)
	}
	if err = cleanupCompositionDatabases(directory, ""); err == nil || !strings.Contains(err.Error(), "unsafe composition cleanup inventory") {
		t.Fatal("mixed inventory was not rejected before connecting")
	}
	for _, file := range []string{first, second} {
		if _, err = os.Stat(file); err != nil {
			t.Fatal("cleanup partially removed invalid inventory")
		}
	}
}

func TestCompositionPreparationCleanupWithoutInventoryNeedsNoConnection(t *testing.T) {
	if err := cleanupCompositionDatabases(t.TempDir(), ""); err != nil {
		t.Fatal(err)
	}
}

func TestCompositionPreparationRejectsNonSyntheticDatabase(t *testing.T) {
	for _, raw := range []string{"postgres://role@10.0.4.6/aicrm_test_x", "postgres://role@localhost/aicrm", "postgres://role@localhost/production"} {
		if _, err := compositionSyntheticURL(raw); err == nil {
			t.Fatal("unsafe template URL accepted")
		}
	}
}
