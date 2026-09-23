// Explicit migration: live read verification precedes a separate protected
// WeCom-only provisioning transaction. No Union fact, linking, or merge exists.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	ia "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/adapter"
	app "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	proof "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/migration/cutoverproof"
	ip "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	store "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/store"
	config "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	pg "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	wa "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/adapter"
	wp "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
	"os"
	"time"
)

func main() {
	if e := run(context.Background(), os.Args[1:]); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
func run(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("wecom-provision", flag.ContinueOnError)
	mode := fs.String("mode", "dry-run", "verify|dry-run|apply")
	path := fs.String("proof", "", "encrypted input proof")
	key := fs.String("key-file", "", "protected encryption key")
	want := fs.String("proof-sha256", "", "exact input digest")
	candidates := fs.String("candidates", "", "protected JSON source reference array")
	candidateSHA := fs.String("candidate-sha256", "", "exact candidate digest")
	out := fs.String("output", "", "exclusive encrypted verified plan")
	confirmed := fs.Bool("confirm-matched-corp", false, "independently verified source and target Corp")
	apply := fs.Bool("confirm-provision", false, "explicitly provision verified WeCom-only customers")
	if e := fs.Parse(args); e != nil {
		return e
	}
	if !*confirmed {
		return errors.New("matched Corp confirmation required")
	}
	s, d, e := proof.Load(*path, *key)
	if e != nil {
		return e
	}
	corp := config.CutoverWecomProvisionEnvironment("AICRM_WECOM_CORP_ID")
	if *want != hex.EncodeToString(d[:]) || corp == "" || corp != s.Scopes.CorpID {
		return errors.New("proof/Corp mismatch")
	}
	pool, e := pg.Open(ctx, pg.Config{URL: config.CutoverWecomProvisionEnvironment("AICRM_DATABASE_URL"), MaxConnections: 2, MinConnections: 1})
	if e != nil {
		return errors.New("open isolated target")
	}
	defer pool.Close()
	uow, e := pg.NewUnitOfWork(pool)
	if e != nil {
		return e
	}
	owner := app.OneIDService{Store: store.NewPostgresStore()}
	if *mode == "verify" || *mode == "probe" {
		refs, _, e := proof.ExistingWecomReferences(s, ia.ProviderHistory{})
		if e != nil {
			return e
		}
		raw, e := os.ReadFile(*candidates)
		if e != nil {
			return errors.New("read protected candidates")
		}
		h := sha256.Sum256(raw)
		if *candidateSHA != hex.EncodeToString(h[:]) {
			return errors.New("candidate digest mismatch")
		}
		var selection struct {
			Refs []string `json:"opaque_source_refs"`
		}
		if json.Unmarshal(raw, &selection) != nil {
			return errors.New("invalid candidates")
		}
		chosen := selection.Refs
		if len(chosen) == 0 {
			return errors.New("invalid candidates")
		}
		seen := map[string]bool{}
		rows := map[string]proof.Row{}
		for _, r := range s.Rows {
			rows[r.UnionID] = r
		}
		for _, v := range chosen {
			if seen[v] || refs[v].Value == "" {
				return errors.New("candidate missing strict evidence or duplicated")
			}
			seen[v] = true
		}
		client, e := wa.NewDirectory(wa.Config{Enabled: true, CorpID: corp, ContactSecret: config.CutoverWecomProvisionEnvironment("AICRM_WECOM_CONTACT_SECRET")})
		if e != nil {
			return errors.New("directory reader not configured")
		}
		fresh := proof.Snapshot{Version: 3, ResolutionMode: proof.VerifiedWecomProvision, Scopes: proof.Scopes{CorpID: corp}, CapturedAt: time.Now().UTC(), Rows: []proof.Row{}, ParentProofSHA: *want, CandidateSHA: *candidateSHA, Verification: map[string]string{}}
		counts := map[string]int{}
		for _, v := range chosen {
			r := rows[v]
			state := "verified"
			var found ip.ResolveResult
			if e = uow.Within(ctx, func(tx context.Context) error { var err error; found, err = owner.Resolve(tx, refs[v]); return err }); e != nil {
				return errors.New("target resolve failed")
			}
			if found.Status == ip.ResolveFound {
				state = "already_found"
			} else if found.Status != ip.ResolveNotFound {
				state = "target_conflict"
			} else {
				contact, err := client.ReadExternalContact(ctx, r.PrimaryExternalID)
				state = verificationState(r, contact, err)
				if *mode == "probe" {
					code := "none"
					var failure wp.DirectoryFailure
					if errors.As(err, &failure) {
						code = failure.DirectoryFailureCode()
					}
					httpStatus := 0
					var providerCode int64
					var numeric interface{ DirectoryFailureNumbers() (int, int64) }
					if errors.As(err, &numeric) {
						httpStatus, providerCode = numeric.DirectoryFailureNumbers()
					}
					return json.NewEncoder(os.Stdout).Encode(map[string]any{"mode": "probe", "provider_reads": 1, "state": state, "failure_code": code, "http_status": httpStatus, "provider_code": providerCode, "identity_writes": 0})
				}
			}
			fresh.Rows = append(fresh.Rows, r)
			fresh.Verification[v] = state
			counts[state]++
		}
		digest, e := proof.Seal(fresh, *out, *key)
		if e != nil {
			return e
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"mode": "verify", "counts": counts, "proof_sha256": hex.EncodeToString(digest[:]), "identity_writes": 0})
	}
	if *mode != "dry-run" && *mode != "apply" {
		return errors.New("unsupported mode")
	}
	if *mode == "apply" && !*apply {
		return errors.New("explicit provision confirmation required")
	}
	refs, e := proof.ProvisionReferences(s, ia.ProviderHistory{})
	if e != nil {
		return e
	}
	counts := map[string]int{"not_verified": len(s.Rows) - len(refs)}
	e = uow.Within(ctx, func(tx context.Context) error {
		for _, row := range s.Rows {
			ref, ok := refs[row.UnionID]
			if !ok {
				continue
			}
			match, err := owner.Resolve(tx, ref)
			if err != nil {
				return errors.New("target resolve failed")
			}
			if match.Status == ip.ResolveFound {
				counts["already_found"]++
				continue
			}
			if match.Status != ip.ResolveNotFound {
				return errors.New("target identity conflict; rolled back")
			}
			if *mode == "dry-run" {
				counts["ready"]++
				continue
			}
			fact, err := (ia.ProviderHistory{}).VerifiedHistoricalFact(ip.HistoricalVerifiedInput{Kind: string(ref.Kind), Scope: ref.Scope, Value: ref.Value, Source: "provider-history:cutover-live:" + *want})
			if err != nil {
				return err
			}
			result, err := owner.ProvisionVerifiedIdentity(tx, ip.ProvisionCommand{Fact: fact, IdempotencyKey: "cutover-live:" + *want})
			if err != nil {
				return errors.New("verified provisioning failed; rolled back")
			}
			if result.Created {
				counts["created"]++
			} else {
				counts["already_found"]++
			}
		}
		return nil
	})
	if e != nil {
		return e
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"mode": *mode, "counts": counts, "union_facts": 0, "merge_calls": 0})
}

func verificationState(r proof.Row, c wp.ExternalContact, err error) string {
	if err != nil {
		return "provider_failed"
	}
	if c.ExternalUserID != r.PrimaryExternalID || c.UnionID != r.UnionID || len(c.FollowInfo) == 0 {
		return "provider_mismatch"
	}
	return "verified"
}
