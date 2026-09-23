// migrate-order-distribution-qualification imports only reviewed historical
// purchase-to-product mappings. It cannot create Orders, Payments, customers,
// commissions, or payment effects; it merely makes already-proven history
// eligible for the same locked qualification checks as native purchases.
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

	identityquery "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/query"
	orderapp "github.com/qianlan33333-png/AI-CRM-v3/internal/order/app"
	ordermigration "github.com/qianlan33333-png/AI-CRM-v3/internal/order/migration"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	orderstore "github.com/qianlan33333-png/AI-CRM-v3/internal/order/store"
	paymentapp "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/app"
	paymentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/store"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

var errDryRunRollback = errors.New("qualification dry-run rollback")

type options struct {
	mode, snapshot, digest string
	confirm                bool
}

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "order distribution qualification migration failed:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("migrate-order-distribution-qualification", flag.ContinueOnError)
	var cfg options
	flags.StringVar(&cfg.mode, "mode", "inspect", "inspect|dry-run|apply")
	flags.StringVar(&cfg.snapshot, "snapshot", "", "path to protected qualification mapping snapshot")
	flags.StringVar(&cfg.digest, "manifest-sha256", "", "exact protected snapshot sha256 required for apply")
	flags.BoolVar(&cfg.confirm, "confirm-apply", false, "confirm the exact apply snapshot")
	if err := flags.Parse(args); err != nil || cfg.snapshot == "" {
		return errors.New("snapshot is required")
	}
	manifest, err := ordermigration.LoadQualificationManifest(cfg.snapshot)
	if err != nil {
		return err
	}
	if cfg.mode == "inspect" {
		return printJSON(map[string]any{"mode": "inspect", "manifest_sha256": manifest.DigestHex(), "summary": manifest.Summary()})
	}
	if cfg.mode != "dry-run" && cfg.mode != "apply" {
		return errors.New("unsupported mode")
	}
	if cfg.mode == "apply" {
		provided, decodeErr := hex.DecodeString(cfg.digest)
		if decodeErr != nil || len(provided) != len(manifest.Digest) || subtle.ConstantTimeCompare(provided, manifest.Digest[:]) != 1 {
			return errors.New("manifest digest confirmation mismatch")
		}
		if !cfg.confirm {
			return errors.New("apply requires --confirm-apply")
		}
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
	payments := paymentapp.NewService(uow, paymentstore.NewPostgreSQL(), nil, nil, nil)
	if err = orders.SetHistoricalQualificationEvidenceVerifier(orderapp.NewHistoricalQualificationEvidenceVerifier(identityquery.NewPostgreSQL(), payments)); err != nil {
		return err
	}

	rows := make([]orderport.HistoricalQualificationEvidence, 0, len(manifest.Rows))
	for _, row := range manifest.Rows {
		evidence, evidenceErr := manifest.Evidence(row)
		if evidenceErr != nil {
			return evidenceErr
		}
		rows = append(rows, evidence)
	}
	if cfg.mode == "dry-run" {
		for _, evidence := range rows {
			err = uow.Within(ctx, func(tx context.Context) error {
				if verifyErr := orders.ImportHistoricalQualificationEvidenceWithin(tx, evidence); verifyErr != nil {
					return verifyErr
				}
				return errDryRunRollback
			})
			if !errors.Is(err, errDryRunRollback) {
				return err
			}
		}
		return printJSON(map[string]any{"mode": "dry-run", "run_key": manifest.RunKey, "manifest_sha256": manifest.DigestHex(), "verified_rows": len(rows), "writes_committed": 0})
	}

	for _, evidence := range rows {
		if err = uow.Within(ctx, func(tx context.Context) error {
			return orders.ImportHistoricalQualificationEvidenceWithin(tx, evidence)
		}); err != nil {
			return err
		}
	}
	return printJSON(map[string]any{"mode": "apply", "run_key": manifest.RunKey, "manifest_sha256": manifest.DigestHex(), "processed_rows": len(rows), "order_or_payment_rows_changed": 0, "commissions_changed": 0})
}

func printJSON(value any) error { return json.NewEncoder(os.Stdout).Encode(value) }
