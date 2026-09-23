package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	automationstore "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/store"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/configmigration/source"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/configmigration/target"
	couponport "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/port"
	couponstore "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/store"
	groupopsstore "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/store"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	productstore "github.com/qianlan33333-png/AI-CRM-v3/internal/product/store"
)

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("migrate-v2-config-definitions", flag.ContinueOnError)
	mode := fs.String("mode", "inspect", "inspect|extract|dry-run|apply|verify|history-inspect|history-extract|history-dry-run|history-apply|history-verify")
	snapshot := fs.String("snapshot", "", "encrypted snapshot")
	key := fs.String("snapshot-key-file", "", "0600 AES key")
	revision := fs.String("source-revision", "", "40-char source revision")
	actor := fs.Int64("actor-admin-user-id", 0, "explicit target administrator")
	want := fs.String("manifest-sha256", "", "snapshot digest confirmation")
	reviewID := fs.Int64("review-coupon-source-id", 0, "one explicitly reviewed source coupon")
	reviewBefore := fs.String("review-coupon-before-sha256", "", "exact Owner before state")
	confirm := fs.Bool("confirm-apply", false, "confirm target write")
	commerceOnly := fs.Bool("commerce-only", false, "capture, inspect, preflight or apply current commerce definitions only")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *commerceOnly && *mode != "extract" && *mode != "inspect" && *mode != "dry-run" && *mode != "apply" && *mode != "review-coupon" {
		return errors.New("commerce-only supports extract, inspect, dry-run and apply only")
	}
	if *commerceOnly && *mode == "apply" && !*confirm {
		return errors.New("commerce-only apply requires --confirm-apply")
	}
	if *mode == "history-extract" {
		if *snapshot == "" || *key == "" || *revision == "" {
			return errors.New("history-extract requires AICRM_SOURCE_DATABASE_URL, snapshot, snapshot-key-file, source-revision")
		}
		sourceDatabaseURL, e := platformconfig.SourceDatabaseURL()
		if e != nil {
			return e
		}
		p, e := pgxpool.New(ctx, sourceDatabaseURL)
		if e != nil {
			return e
		}
		defer p.Close()
		s, e := source.ExtractHistory(ctx, p, *revision)
		if e != nil {
			return e
		}
		d, e := source.SealHistoryToFile(s, *snapshot, *key)
		if e != nil {
			return e
		}
		return print(historySummary("history-extract", s, d))
	}
	if *mode == "extract" {
		if *snapshot == "" || *key == "" || *revision == "" {
			return errors.New("extract requires AICRM_SOURCE_DATABASE_URL, snapshot, snapshot-key-file, source-revision")
		}
		sourceDatabaseURL, e := platformconfig.SourceDatabaseURL()
		if e != nil {
			return e
		}
		p, e := pgxpool.New(ctx, sourceDatabaseURL)
		if e != nil {
			return e
		}
		defer p.Close()
		var s source.Snapshot
		if *commerceOnly {
			s, e = source.ExtractCommerceFrom(ctx, p, *revision)
		} else {
			s, e = source.Extract(ctx, p, *revision)
		}
		if e != nil {
			return e
		}
		if !*commerceOnly {
			if e = source.ValidateExpectedBaseline(s); e != nil {
				return e
			}
		}
		d, e := source.SealToFile(s, *snapshot, *key)
		if e != nil {
			return e
		}
		return print(summary("extract", s, d))
	}
	if *snapshot == "" || *key == "" {
		return errors.New("snapshot and snapshot-key-file are required")
	}
	if *mode == "history-inspect" || *mode == "history-dry-run" || *mode == "history-apply" || *mode == "history-verify" {
		s, d, e := source.LoadHistoryFile(*snapshot, *key)
		if e != nil {
			return e
		}
		if *mode == "history-inspect" {
			return print(historySummary(*mode, s, d))
		}
		url, e := platformconfig.DatabaseURL()
		if e != nil {
			return e
		}
		pool, e := platformpostgres.Open(ctx, platformpostgres.Config{URL: url})
		if e != nil {
			return e
		}
		defer pool.Close()
		uow, e := platformpostgres.NewUnitOfWork(pool)
		if e != nil {
			return e
		}
		groupOps, e := groupopsstore.NewPostgreSQL(pool.Native(), uow)
		if e != nil {
			return e
		}
		runner := target.HistoryRunner{UOW: uow, GroupOps: groupOps}
		if *mode == "history-dry-run" {
			if e = runner.Preflight(ctx, s, d); e != nil {
				return e
			}
			return print(map[string]any{"mode": *mode, "eligible": true, "manifest_sha256": target.DigestHex(d), "counts": s.Summary()})
		}
		if *mode == "history-apply" {
			if !*confirm {
				return errors.New("history-apply requires --confirm-apply")
			}
			out, e := runner.Apply(ctx, s, d)
			if e != nil {
				return e
			}
			return print(map[string]any{"mode": *mode, "manifest_sha256": target.DigestHex(d), "result": out})
		}
		out, e := runner.Verify(ctx, s, d)
		if e != nil {
			return e
		}
		return print(map[string]any{"mode": *mode, "manifest_sha256": target.DigestHex(d), "result": out})
	}
	s, d, e := source.LoadFile(*snapshot, *key)
	if e != nil {
		return e
	}
	if e = s.Validate(); e != nil {
		return e
	}
	if *commerceOnly != (s.Manifest.Scope == "commerce-only") {
		return errors.New("snapshot scope does not match explicit commerce-only selection")
	}
	if !*commerceOnly {
		if e = source.ValidateExpectedBaseline(s); e != nil {
			return e
		}
	}
	if *mode == "inspect" {
		return print(summary("inspect", s, d))
	}
	if *actor < 1 {
		return errors.New("explicit actor-admin-user-id is required")
	}
	if *want != target.DigestHex(d) {
		return errors.New("manifest-sha256 confirmation mismatch")
	}
	url, e := platformconfig.DatabaseURL()
	if e != nil {
		return e
	}
	pool, e := platformpostgres.Open(ctx, platformpostgres.Config{URL: url})
	if e != nil {
		return e
	}
	defer pool.Close()
	if *commerceOnly && *mode == "dry-run" {
		report, err := target.InspectCommerceTarget(ctx, pool.Native(), s, *actor)
		if err != nil {
			return err
		}
		return print(map[string]any{"mode": "dry-run", "scope": "commerce-only", "manifest_sha256": target.DigestHex(d), "result": report})
	}
	if *mode != "apply" && *mode != "dry-run" && *mode != "verify" && *mode != "review-coupon" {
		return errors.New("unknown mode")
	}
	if *mode == "apply" && !*confirm {
		return errors.New("apply requires --confirm-apply")
	}
	uow, e := platformpostgres.NewUnitOfWork(pool)
	if e != nil {
		return e
	}
	p, e := productstore.NewPostgreSQL(pool.Native(), uow)
	if e != nil {
		return e
	}
	c, e := couponstore.NewPostgreSQL(pool.Native(), uow)
	if e != nil {
		return e
	}
	if *commerceOnly {
		if *mode == "review-coupon" {
			if *reviewID < 1 {
				return errors.New("explicit reviewed coupon source required")
			}
			var before [32]byte
			err := uow.Within(ctx, func(bound context.Context) error {
				tx, err := platformpostgres.RequireTransaction(bound)
				if err != nil {
					return err
				}
				var id int64
				err = tx.QueryRow(bound, `SELECT target_id FROM config_definition_import_source_maps WHERE source_system=$1 AND source_kind='commerce_coupons' AND source_key=$2`, s.Manifest.SourceSystem, fmt.Sprint(*reviewID)).Scan(&id)
				if err != nil {
					return err
				}
				before, err = c.CutoverReviewDigest(bound, couponport.ID(id))
				return err
			})
			if err != nil {
				return err
			}
			return print(map[string]any{"mode": "review-coupon", "source_id": *reviewID, "before_sha256": target.DigestHex(before), "manifest_sha256": target.DigestHex(d)})
		}
		var before [32]byte
		if *reviewID != 0 || *reviewBefore != "" {
			raw, err := hex.DecodeString(*reviewBefore)
			if err != nil || len(raw) != 32 || *reviewID < 1 {
				return errors.New("explicit reviewed coupon and full before digest required")
			}
			copy(before[:], raw)
		}
		runner := target.Runner{UOW: uow, Products: p, Coupons: c, ReviewCouponSourceID: *reviewID, ReviewCouponBefore: before}
		out, err := runner.Apply(ctx, s, d, *actor)
		if err != nil {
			return err
		}
		return print(map[string]any{"mode": "apply", "scope": "commerce-only", "manifest_sha256": target.DigestHex(d), "result": out})
	}
	g, e := groupopsstore.NewPostgreSQL(pool.Native(), uow)
	if e != nil {
		return e
	}
	a, e := automationstore.NewPostgreSQL(pool.Native(), uow)
	if e != nil {
		return e
	}
	runner := target.Runner{UOW: uow, Products: p, Coupons: c, GroupOps: g, Automation: a}
	if *mode == "dry-run" {
		if e = runner.Preflight(ctx, s, d, *actor); e != nil {
			return e
		}
		return print(map[string]any{"mode": "dry-run", "eligible": true, "manifest_sha256": target.DigestHex(d), "counts": s.Summary()})
	}
	if *mode == "verify" {
		out, e := runner.Verify(ctx, s, d)
		if e != nil {
			return e
		}
		return print(map[string]any{"mode": "verify", "manifest_sha256": target.DigestHex(d), "result": out})
	}
	out, e := runner.Apply(ctx, s, d, *actor)
	if e != nil {
		return e
	}
	return print(map[string]any{"mode": "apply", "manifest_sha256": target.DigestHex(d), "result": out})
}
func print(v any) error { return json.NewEncoder(os.Stdout).Encode(v) }

func summary(mode string, snapshot source.Snapshot, digest [32]byte) map[string]any {
	return map[string]any{
		"scope":           snapshot.Manifest.Scope,
		"mode":            mode,
		"manifest_sha256": target.DigestHex(digest),
		"source_system":   snapshot.Manifest.SourceSystem,
		"source_revision": snapshot.Manifest.SourceRevision,
		"snapshot_at":     snapshot.Manifest.SnapshotAt,
		"counts":          snapshot.Summary(),
	}
}

func historySummary(mode string, snapshot source.HistorySnapshot, digest [32]byte) map[string]any {
	return map[string]any{
		"mode":            mode,
		"manifest_sha256": target.DigestHex(digest),
		"source_system":   snapshot.Manifest.SourceSystem,
		"source_revision": snapshot.Manifest.SourceRevision,
		"snapshot_at":     snapshot.Manifest.SnapshotAt,
		"counts":          snapshot.Summary(),
	}
}
