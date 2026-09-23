// Command migrate-river applies River's pinned PostgreSQL schema before the
// v3 application starts a durable worker. Runtime packages never import it.
package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

func main() {
	if err := run(); err != nil {
		slog.Error("River migration failed", "error", err)
		os.Exit(1)
	}
}
func run() error {
	url, err := platformconfig.DatabaseURL()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool, err := platformpostgres.Open(ctx, platformpostgres.Config{URL: url})
	if err != nil {
		return err
	}
	defer pool.Close()
	migrator, err := rivermigrate.New(riverpgxv5.New(pool.Native()), nil)
	if err != nil {
		return err
	}
	_, err = migrator.Migrate(ctx, rivermigrate.DirectionUp, nil)
	return err
}
