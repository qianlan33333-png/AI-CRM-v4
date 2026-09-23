// migrate-cutover-identities captures narrowly proven provider relationships
// for membership/coupon subjects. It never imports business history or phones.
package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	identityadapter "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/adapter"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identitymigration "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/migration"
	proof "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/migration/cutoverproof"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	identitystore "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/store"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func main() {
	if e := run(context.Background(), os.Args[1:]); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
func run(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("migrate-cutover-identities", flag.ContinueOnError)
	mode := fs.String("mode", "inspect", "capture, inspect, dry-run or apply")
	path := fs.String("snapshot", "", "protected encrypted proof path")
	key := fs.String("snapshot-key-file", "", "0600 32-byte base64 key")
	want := fs.String("manifest-sha256", "", "explicit proof digest")
	confirm := fs.Bool("confirm-apply", false, "write accepted identity candidates and quarantine receipts")
	existingOnly := fs.Bool("existing-wecom-only", false, "resolve existing WeCom roots only; no Union scope or identity writes")
	matchedCorp := fs.Bool("confirm-matched-corp", false, "source and target Corp independently verified equal")
	scopesConfirmed := fs.Bool("confirm-matched-provider-scopes", false, "source and target Provider Corp/OpenPlatform configuration independently verified equal")
	corp := fs.String("corp-id", platformconfig.CutoverIdentityEnvironment("AICRM_WECOM_CORP_ID"), "verified shared WeCom Corp ID")
	union := fs.String("union-scope", platformconfig.CutoverIdentityEnvironment("AICRM_SURVEY_OAUTH_OPEN_PLATFORM_ID"), "explicit wechat-open-platform namespace")
	if e := fs.Parse(args); e != nil {
		return e
	}
	if *mode != "capture" && *mode != "inspect" && *mode != "dry-run" && *mode != "apply" {
		return errors.New("unsupported proof mode")
	}
	if *path == "" || *key == "" {
		return errors.New("snapshot and key file required")
	}
	if *mode == "apply" && !*confirm {
		return errors.New("apply requires confirm-apply")
	}
	scopes := proof.Scopes{CorpID: *corp, UnionScope: *union}
	if *mode == "capture" {
		if (!*existingOnly && (!*scopesConfirmed || scopes.Validate() != nil)) || (*existingOnly && (!*matchedCorp || *corp == "")) {
			return errors.New("capture requires independently matched explicit provider scopes")
		}
		db, e := open(ctx, "AICRM_SOURCE_DATABASE_URL")
		if e != nil {
			return e
		}
		defer db.Close()
		var s proof.Snapshot
		var captureErr error
		if *existingOnly {
			s, captureErr = proof.CaptureExistingWecom(ctx, db, *corp)
		} else {
			s, captureErr = proof.Capture(ctx, db, scopes)
		}
		e = captureErr
		if e != nil {
			return e
		}
		d, e := proof.Seal(s, *path, *key)
		if e != nil {
			return e
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"mode": "capture", "subjects": len(s.Rows), "manifest_sha256": hex.EncodeToString(d[:])})
	}
	s, d, e := proof.Load(*path, *key)
	if e != nil {
		return e
	}
	if *mode == "inspect" {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"mode": "inspect", "subjects": len(s.Rows), "manifest_sha256": hex.EncodeToString(d[:])})
	}
	if s.Version == 2 {
		if !*existingOnly || !*matchedCorp || s.Scopes.CorpID != *corp || *mode == "apply" {
			return errors.New("resolve-only proof requires matched Corp and forbids identity apply")
		}
	} else if !*scopesConfirmed || scopes.Validate() != nil || scopes != s.Scopes {
		return errors.New("target explicit provider scopes must equal independently verified capture scopes")
	}
	if *want != hex.EncodeToString(d[:]) {
		return errors.New("proof digest confirmation mismatch")
	}
	db, e := open(ctx, "AICRM_DATABASE_URL")
	if e != nil {
		return e
	}
	defer db.Close()
	opts := pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}
	if *mode == "apply" {
		opts.AccessMode = pgx.ReadWrite
	}
	tx, e := db.BeginTx(ctx, opts)
	if e != nil {
		return errors.New("begin identity proof transaction")
	}
	defer tx.Rollback(ctx)
	bound := platformpostgres.BindTransaction(ctx, tx)
	owner := identityapp.OneIDService{Store: identitystore.NewPostgresStore()}
	if s.Version == 2 {
		refs, counts, err := proof.ExistingWecomReferences(s, identityadapter.ProviderHistory{})
		if err != nil {
			return err
		}
		for _, ref := range refs {
			r, err := owner.Resolve(bound, ref)
			if err != nil {
				return errors.New("resolve existing WeCom failed")
			}
			counts["target_"+string(r.Status)]++
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"mode": *mode, "manifest_sha256": hex.EncodeToString(d[:]), "counts": counts, "identity_writes": 0})
	}
	plan, e := proof.BuildPlan(bound, s, owner, identityadapter.ProviderHistory{})
	if e != nil {
		return e
	}
	if *mode == "apply" {
		receipts := identitymigration.PostgreSQLReceipts{}
		runKey := "cutover-proof:" + hex.EncodeToString(d[:])
		for i, row := range plan.Rows {
			digest := proof.RowDigest(s.Rows[i], s.Scopes)
			if strings.HasPrefix(row.State, "quarantine_") {
				if e = receipts.RecordQuarantine(bound, runKey, row.SourceKey, digest, row.State, hex.EncodeToString(digest[:])); e != nil {
					return errors.New("identity quarantine receipt conflict")
				}
				continue
			}
			result, err := owner.ProvisionHistoricalSubject(bound, identityport.HistoricalSubjectCommand{SubjectKey: row.SourceKey, Facts: row.Facts, SourceDigest: digest})
			if err != nil {
				return errors.New("identity provisioning conflict; batch rolled back")
			}
			if e = receipts.RecordSubject(bound, runKey, row.SourceKey, digest, int64(result.CustomerID), len(result.IdentityIDs)); e != nil {
				return errors.New("identity receipt conflict; batch rolled back")
			}
		}
	}
	if e = tx.Commit(ctx); e != nil {
		return errors.New("identity proof transaction failed")
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"mode": *mode, "manifest_sha256": hex.EncodeToString(d[:]), "plan": plan})
}
func open(ctx context.Context, name string) (*pgxpool.Pool, error) {
	dsn := platformconfig.CutoverIdentityEnvironment(name)
	if dsn == "" {
		return nil, errors.New("protected database environment unavailable")
	}
	config, e := pgxpool.ParseConfig(dsn)
	if e != nil {
		return nil, errors.New("invalid protected database configuration")
	}
	config.MaxConns = 2
	db, e := pgxpool.NewWithConfig(ctx, config)
	if e != nil {
		return nil, errors.New("connect protected database")
	}
	return db, nil
}
