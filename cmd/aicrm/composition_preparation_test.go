package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	testconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config/tests"
)

// A runner supplies one private preparation directory per attempt. Direct Go
// invocations get the same reuse within this package and clean up at exit.
func TestMain(m *testing.M) {
	owned := testconfig.PreparationDirectory() == ""
	directory := testconfig.PreparationDirectory()
	if owned {
		var err error
		directory, err = os.MkdirTemp("", "aicrm-test-preparation-")
		if err != nil {
			fmt.Fprintln(os.Stderr, "create test preparation:", err)
			os.Exit(2)
		}
		_ = os.Setenv("AICRM_TEST_PREP_DIR", directory)
	}
	code := m.Run()
	raw, _ := platformconfig.DatabaseURL()
	if owned {
		if err := cleanupCompositionDatabases(directory, raw); err != nil {
			fmt.Fprintln(os.Stderr, "test database cleanup failed:", err)
			code = 1
		} else {
			_ = os.RemoveAll(directory)
		}
	}
	os.Exit(code)
}

type compositionDatabasePreparation struct {
	Database    string `json:"database"`
	Fingerprint string `json:"fingerprint"`
	Purpose     string `json:"purpose,omitempty"`
}

type compositionCleanupContext struct {
	raw         string
	fingerprint string
}

// Connection credentials stay in this process, never in preparation records.
// The installed smoke supplies its connection through a private input file,
// so the process environment is not the owner of that connection.
var compositionCleanupContexts = struct {
	sync.Mutex
	byDirectory map[string]compositionCleanupContext
}{byDirectory: make(map[string]compositionCleanupContext)}

func rememberCompositionCleanupContext(directory, raw, fingerprint string) error {
	compositionCleanupContexts.Lock()
	defer compositionCleanupContexts.Unlock()
	next := compositionCleanupContext{raw: raw, fingerprint: fingerprint}
	if previous, ok := compositionCleanupContexts.byDirectory[directory]; ok && previous != next {
		return fmt.Errorf("composition preparation connection identity changed")
	}
	compositionCleanupContexts.byDirectory[directory] = next
	return nil
}

func compositionPreparationDirectory() (string, error) {
	directory := testconfig.PreparationDirectory()
	return directory, validateCompositionPreparationDirectory(directory)
}

func validateCompositionPreparationDirectory(directory string) error {
	info, err := os.Lstat(directory)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !filepath.IsAbs(directory) || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 || !ok || int(stat.Uid) != os.Getuid() {
		return fmt.Errorf("composition preparation requires a private check-owned directory")
	}
	return nil
}

func compositionSyntheticURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid synthetic database URL")
	}
	database := strings.TrimPrefix(parsed.Path, "/")
	if (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") || (parsed.Hostname() != "localhost" && parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "::1") || (database != "aicrm_ci" && !strings.HasPrefix(database, "aicrm_test_")) {
		return nil, fmt.Errorf("composition template requires an explicit local synthetic database")
	}
	return parsed, nil
}

func compositionPreparationFingerprint(raw string) (string, error) {
	parsed, err := compositionSyntheticURL(raw)
	if err != nil {
		return "", err
	}
	digest := sha256.New()
	// Credentials never enter the manifest; connection identity prevents reuse
	// against another server, database or role in a shared attempt directory.
	fmt.Fprintf(digest, "%s\x00%s\x00%s\x00%s", parsed.Host, parsed.Path, parsed.User.Username(), parsed.Query().Get("sslmode"))
	_, source, _, _ := runtime.Caller(0)
	repository := filepath.Join(filepath.Dir(source), "..", "..")
	names, err := filepath.Glob(filepath.Join(repository, "migrations", "*.sql"))
	if err != nil || len(names) == 0 {
		return "", fmt.Errorf("composition migrations are missing")
	}
	names = append(names, filepath.Join(repository, "go.sum"), source,
		filepath.Join(repository, "cmd/aicrm/admin_access_journey_integration_test.go"))
	sort.Strings(names)
	for _, name := range names {
		data, err := os.ReadFile(name)
		if err != nil {
			return "", err
		}
		relative, _ := filepath.Rel(repository, name)
		digest.Write([]byte(relative + "\x00"))
		digest.Write(data)
		digest.Write([]byte{0})
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func newCompositionDatabaseName(kind string) (string, error) {
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return "aicrm_test_" + kind + "_" + hex.EncodeToString(random[:]) + "_acceptance_test", nil
}

func writeCompositionPreparation(directory string, record compositionDatabasePreparation) (string, error) {
	data, err := json.Marshal(record)
	if err != nil {
		return "", err
	}
	path := filepath.Join(directory, "database-"+record.Database+".json")
	return path, os.WriteFile(path, data, 0600)
}

func preparedCompositionDatabase(t *testing.T, ctx context.Context, raw string) (string, func()) {
	t.Helper()
	directory, err := compositionPreparationDirectory()
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, err := compositionPreparationFingerprint(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err = rememberCompositionCleanupContext(directory, raw, fingerprint); err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	fail := func(err error) { admin.Close(); t.Fatal(err) }
	lock, err := os.OpenFile(filepath.Join(directory, "database.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		fail(err)
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		fail(err)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	template := ""
	records, err := filepath.Glob(filepath.Join(directory, "database-aicrm_test_tpl_*.json"))
	if err != nil {
		fail(err)
	}
	for _, file := range records {
		data, readErr := os.ReadFile(file)
		if readErr != nil {
			fail(readErr)
		}
		var record compositionDatabasePreparation
		if err = json.Unmarshal(data, &record); err != nil {
			fail(err)
		}
		if record.Fingerprint == fingerprint {
			if !validCompositionDatabaseName(record.Database) {
				fail(fmt.Errorf("invalid template database inventory"))
			}
			var ready bool
			if err = admin.QueryRow(ctx, "SELECT NOT datallowconn AND pg_get_userbyid(datdba)=current_user FROM pg_database WHERE datname=$1", record.Database).Scan(&ready); err != nil || !ready {
				fail(fmt.Errorf("composition template is absent, connected or has a different owner"))
			}
			template = record.Database
			break
		}
	}
	const schema = "admin_access_composition_template"
	if template == "" {
		template, err = newCompositionDatabaseName("tpl")
		if err != nil {
			fail(err)
		}
		if _, err = writeCompositionPreparation(directory, compositionDatabasePreparation{Database: template, Fingerprint: fingerprint}); err != nil {
			fail(err)
		}
		if _, err = admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{template}.Sanitize()+" TEMPLATE template0"); err != nil {
			fail(err)
		}
		parsed, _ := compositionSyntheticURL(raw)
		parsed.Path = "/" + template
		migration, err := pgxpool.New(ctx, parsed.String())
		if err != nil {
			fail(err)
		}
		if _, err = migration.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
			migration.Close()
			fail(err)
		}
		migration.Close()
		query := parsed.Query()
		query.Set("search_path", schema)
		parsed.RawQuery = query.Encode()
		migration, err = pgxpool.New(ctx, parsed.String())
		if err != nil {
			fail(err)
		}
		err = adminAccessMigrateCompositionSchema(ctx, migration)
		migration.Close()
		if err != nil {
			fail(err)
		}
		if _, err = admin.Exec(ctx, "ALTER DATABASE "+pgx.Identifier{template}.Sanitize()+" ALLOW_CONNECTIONS false"); err != nil {
			fail(err)
		}
		t.Log("composition preparation: migration template created")
	}
	clone, err := newCompositionDatabaseName("clone")
	if err != nil {
		fail(err)
	}
	recordPath, err := writeCompositionPreparation(directory, compositionDatabasePreparation{Database: clone, Fingerprint: fingerprint})
	if err != nil {
		fail(err)
	}
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{clone}.Sanitize()+" TEMPLATE "+pgx.Identifier{template}.Sanitize()); err != nil {
		fail(err)
	}
	parsed, _ := compositionSyntheticURL(raw)
	parsed.Path = "/" + clone
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	t.Log("composition preparation: isolated database cloned")
	return parsed.String(), func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		defer admin.Close()
		if _, err := admin.Exec(cleanup, "DROP DATABASE "+pgx.Identifier{clone}.Sanitize()); err != nil {
			t.Errorf("clean composition clone: %v", err)
			return
		}
		if err := os.Remove(recordPath); err != nil {
			t.Errorf("clean composition clone inventory: %v", err)
		}
	}
}

func validCompositionDatabaseName(name string) bool {
	for _, kind := range []string{"tpl", "clone"} {
		prefix := "aicrm_test_" + kind + "_"
		if strings.HasPrefix(name, prefix) && strings.HasSuffix(name, "_acceptance_test") {
			random := strings.TrimSuffix(strings.TrimPrefix(name, prefix), "_acceptance_test")
			if len(random) != 16 {
				return false
			}
			_, err := hex.DecodeString(random)
			return err == nil
		}
	}
	return false
}

func cleanupCompositionDatabases(directory, raw string) error {
	records, err := filepath.Glob(filepath.Join(directory, "database-*.json"))
	if err != nil || len(records) == 0 {
		return err
	}
	if err = validateCompositionPreparationDirectory(directory); err != nil {
		return err
	}
	compositionCleanupContexts.Lock()
	connection, captured := compositionCleanupContexts.byDirectory[directory]
	compositionCleanupContexts.Unlock()
	if captured {
		raw = connection.raw
	}
	if _, err := compositionSyntheticURL(raw); err != nil {
		return err
	}
	if !captured {
		connection.fingerprint, err = compositionPreparationFingerprint(raw)
		if err != nil {
			return err
		}
	}
	// Validate the complete inventory before deleting anything. General fixture
	// registrations share this directory but intentionally have no fingerprint.
	inventory := make([]compositionDatabasePreparation, 0, len(records))
	for _, file := range records {
		info, err := os.Lstat(file)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
			return fmt.Errorf("unsafe composition cleanup inventory")
		}
		data, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		var record compositionDatabasePreparation
		if err = json.Unmarshal(data, &record); err != nil {
			return err
		}
		if !validCompositionDatabaseName(record.Database) || filepath.Base(file) != "database-"+record.Database+".json" ||
			(record.Fingerprint != "" && record.Fingerprint != connection.fingerprint) ||
			(record.Fingerprint == "" && (record.Purpose != "fixture" || !strings.HasPrefix(record.Database, "aicrm_test_clone_"))) {
			return fmt.Errorf("unsafe composition cleanup inventory")
		}
		inventory = append(inventory, record)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	pool, err := pgxpool.New(ctx, raw)
	if err != nil {
		return err
	}
	defer pool.Close()
	for index, record := range inventory {
		if _, err = pool.Exec(ctx, "DROP DATABASE IF EXISTS "+pgx.Identifier{record.Database}.Sanitize()); err != nil {
			return err
		}
		if err = os.Remove(records[index]); err != nil {
			return err
		}
	}
	compositionCleanupContexts.Lock()
	delete(compositionCleanupContexts.byDirectory, directory)
	compositionCleanupContexts.Unlock()
	return nil
}
