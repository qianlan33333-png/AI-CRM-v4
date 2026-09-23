package main

import (
	"context"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	identityquery "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/query"
	orderapp "github.com/qianlan33333-png/AI-CRM-v3/internal/order/app"
	ordermigration "github.com/qianlan33333-png/AI-CRM-v3/internal/order/migration"
	orderstore "github.com/qianlan33333-png/AI-CRM-v3/internal/order/store"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

type options struct {
	mode, snapshot, digest, corpID string
	confirm                        bool
}

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "order attribution migration failed:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("migrate-order-attribution", flag.ContinueOnError)
	var cfg options
	flags.StringVar(&cfg.mode, "mode", "inspect", "inspect|dry-run|apply|reconcile")
	flags.StringVar(&cfg.snapshot, "snapshot", "", "path to the protected attribution snapshot")
	flags.StringVar(&cfg.digest, "manifest-sha256", "", "required snapshot sha256 for apply/reconcile")
	flags.StringVar(&cfg.corpID, "wecom-corp-id", "", "confirmed WeCom Corp ID used to scope existing verified identities")
	flags.BoolVar(&cfg.confirm, "confirm-apply", false, "confirm the exact apply manifest")
	if err := flags.Parse(args); err != nil || cfg.snapshot == "" {
		return errors.New("snapshot is required")
	}
	manifest, err := ordermigration.LoadAttribution(cfg.snapshot)
	if err != nil {
		return err
	}
	if cfg.mode == "inspect" {
		return printJSON(map[string]any{"mode": cfg.mode, "manifest_sha256": manifest.DigestHex(), "summary": manifest.Summary()})
	}
	if strings.TrimSpace(cfg.corpID) != cfg.corpID || cfg.corpID == "" {
		return errors.New("confirmed WeCom Corp ID is required")
	}
	if cfg.mode == "apply" || cfg.mode == "reconcile" {
		provided, decodeErr := hex.DecodeString(cfg.digest)
		if decodeErr != nil || len(provided) != len(manifest.Digest) || subtle.ConstantTimeCompare(provided, manifest.Digest[:]) != 1 {
			return errors.New("manifest digest confirmation mismatch")
		}
	}
	if cfg.mode == "apply" && !cfg.confirm {
		return errors.New("apply requires --confirm-apply")
	}
	databaseURL, err := platformconfig.DatabaseURL()
	if err != nil {
		return err
	}
	pool, err := platformpostgres.Open(ctx, platformpostgres.Config{URL: databaseURL, MaxConnections: 10, MinConnections: 1})
	if err != nil {
		return err
	}
	defer pool.Close()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		return err
	}
	repository, err := orderstore.NewPostgreSQL(pool.Native(), uow)
	if err != nil {
		return err
	}
	orders := orderapp.NewService(uow, repository)
	runner := ordermigration.AttributionRunner{
		UOW: uow, Resolver: identityquery.NewPostgreSQL(), Query: orders,
		Orders: orderapp.NewHistoricalAttributionService(repository),
		Runs:   ordermigration.PostgreSQLAttributionRuns{Pool: pool.Native()},
		Scope:  "wecom-corp:" + cfg.corpID,
	}
	var result ordermigration.AttributionRunResult
	switch cfg.mode {
	case "dry-run":
		result, err = runner.DryRun(ctx, manifest)
	case "apply":
		result, err = runner.Apply(ctx, manifest)
	case "reconcile":
		result, err = runner.Reconcile(ctx, manifest)
	default:
		return errors.New("unsupported mode")
	}
	if err != nil {
		return err
	}
	return printJSON(map[string]any{"mode": cfg.mode, "run_key": manifest.RunKey, "result": result})
}

func printJSON(value any) error { return json.NewEncoder(os.Stdout).Encode(value) }
