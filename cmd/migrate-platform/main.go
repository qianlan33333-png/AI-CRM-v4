// Command migrate-platform applies the independent v3 platform migration set.
// Runtime packages do not import this command or its migration runner.
package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"regexp"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

var migrationName = regexp.MustCompile(`^([0-9]{4,})_[a-z0-9_]+\.sql$`)

type migration struct {
	version  string
	name     string
	checksum [sha256.Size]byte
	sql      string
}

func main() {
	if err := run(); err != nil {
		slog.Error("platform migration failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	directory := flag.String("dir", "migrations", "migration directory")
	timeout := flag.Duration("timeout", 2*time.Minute, "overall migration timeout")
	flag.Parse()
	if *timeout <= 0 {
		return errors.New("timeout must be positive")
	}

	databaseURL, err := platformconfig.DatabaseURL()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	pool, err := platformpostgres.Open(ctx, platformpostgres.Config{URL: databaseURL})
	if err != nil {
		return err
	}
	defer pool.Close()

	if err = applyMigrations(ctx, pool.Native(), os.DirFS(*directory)); err != nil {
		return err
	}
	slog.Info("platform migrations complete")
	return nil
}

func applyMigrations(ctx context.Context, pool *pgxpool.Pool, filesystem fs.FS) error {
	migrations, err := loadMigrations(filesystem)
	if err != nil {
		return err
	}
	connection, err := pool.Acquire(ctx)
	if err != nil {
		return errors.New("acquire migration connection")
	}
	defer connection.Release()

	if _, err = connection.Exec(ctx, `SELECT pg_advisory_lock(hashtext('aicrm_v3_platform_migrations'))`); err != nil {
		return errors.New("acquire migration lock")
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, _ = connection.Exec(unlockCtx, `SELECT pg_advisory_unlock(hashtext('aicrm_v3_platform_migrations'))`)
	}()

	if _, err = connection.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS platform_schema_migrations (
			version text PRIMARY KEY,
			name text NOT NULL UNIQUE,
			checksum bytea NOT NULL,
			applied_at timestamptz NOT NULL DEFAULT clock_timestamp(),
			CONSTRAINT platform_schema_migrations_checksum_sha256 CHECK (octet_length(checksum) = 32)
		)`); err != nil {
		return errors.New("create migration ledger")
	}

	for _, item := range migrations {
		var existingName string
		var existing []byte
		err = connection.QueryRow(ctx,
			`SELECT name, checksum FROM platform_schema_migrations WHERE version = $1`, item.version,
		).Scan(&existingName, &existing)
		switch {
		case err == nil:
			if existingName != item.name {
				return fmt.Errorf("migration version %s name mismatch", item.version)
			}
			if !equalChecksum(existing, item.checksum) {
				return fmt.Errorf("migration %s checksum mismatch", item.name)
			}
			continue
		case !errors.Is(err, pgx.ErrNoRows):
			return fmt.Errorf("read migration %s ledger: %w", item.name, err)
		}

		tx, beginErr := connection.BeginTx(ctx, pgx.TxOptions{})
		if beginErr != nil {
			return fmt.Errorf("begin migration %s: %w", item.name, beginErr)
		}
		if _, err = tx.Exec(ctx, item.sql); err == nil {
			_, err = tx.Exec(ctx,
				`INSERT INTO platform_schema_migrations (version, name, checksum) VALUES ($1, $2, $3)`,
				item.version, item.name, item.checksum[:],
			)
		}
		if err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("apply migration %s: %w", item.name, err)
		}
		if err = tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit migration %s: %w", item.name, err)
		}
	}
	return nil
}

func loadMigrations(filesystem fs.FS) ([]migration, error) {
	entries, err := fs.ReadDir(filesystem, ".")
	if err != nil {
		return nil, errors.New("read migration directory")
	}
	items := make([]migration, 0, len(entries))
	versions := make(map[string]string)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		matches := migrationName.FindStringSubmatch(entry.Name())
		if matches == nil {
			continue
		}
		if previous, exists := versions[matches[1]]; exists {
			return nil, fmt.Errorf("duplicate migration version %s in %s and %s", matches[1], previous, entry.Name())
		}
		contents, readErr := fs.ReadFile(filesystem, entry.Name())
		if readErr != nil {
			return nil, fmt.Errorf("read migration %s: %w", entry.Name(), readErr)
		}
		if len(contents) == 0 {
			return nil, fmt.Errorf("migration %s is empty", entry.Name())
		}
		versions[matches[1]] = entry.Name()
		items = append(items, migration{
			version:  matches[1],
			name:     entry.Name(),
			checksum: sha256.Sum256(contents),
			sql:      string(contents),
		})
	}
	if len(items) == 0 {
		return nil, errors.New("no platform migrations found")
	}
	sort.Slice(items, func(left, right int) bool { return items[left].version < items[right].version })
	return items, nil
}

func equalChecksum(existing []byte, expected [sha256.Size]byte) bool {
	if len(existing) != len(expected) {
		return false
	}
	var difference byte
	for index := range existing {
		difference |= existing[index] ^ expected[index]
	}
	return difference == 0
}
