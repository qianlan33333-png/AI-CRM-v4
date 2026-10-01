// migrate-wecom-account-identities corrects a reviewed hxc UnionID binding
// using fresh WeCom Provider reads. It has no Provider write or business-table
// write path. Run on the approved source tree via the V4 release desk.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	identityquery "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/query"
	identitysecure "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/secure"
	identitystore "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/store"
	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/idempotency"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	wecomadapter "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/adapter"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
	wecomprovider "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/provider"
)

var errDryRun = errors.New("account correction dry-run rollback")

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "WeCom account identity correction failed:", err)
		os.Exit(1)
	}
}

func loadPlan(raw []byte) (identityport.AccountCorrectionPlan, error) {
	var plan identityport.AccountCorrectionPlan
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&plan); err != nil {
		return plan, errors.New("invalid correction plan")
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return plan, errors.New("invalid correction plan trailing content")
	}
	if plan.RunKey == "" || plan.Operator == "" || plan.LeftCustomerID < 1 || plan.RightCustomerID < 1 || plan.LeftCustomerID == plan.RightCustomerID || plan.LeftVersion < 1 || plan.RightVersion < 1 || plan.WrongIdentityID < 1 || plan.WrongIdentityVersion < 1 || plan.CandidateID < 1 || plan.CandidateVersion < 1 || plan.HXCSubjectID < 1 || plan.HXCSubjectVersion < 1 || len(plan.ConflictIDs) == 0 {
		return plan, errors.New("incomplete correction plan")
	}
	for _, id := range plan.ConflictIDs {
		if id < 1 {
			return plan, errors.New("invalid correction conflict ID")
		}
	}
	return plan, nil
}

type contactReader interface {
	ReadExternalContact(context.Context, string) (wecomport.ExternalContact, error)
}

func verifyAccounts(ctx context.Context, reader contactReader, plan identityport.AccountCorrectionPlan, corp, scope string, externals [2]string) (identityport.AccountCorrectionCommand, error) {
	cmd := identityport.AccountCorrectionCommand{Plan: plan}
	if externals[0] == "" || externals[1] == "" || externals[0] == externals[1] {
		return cmd, errors.New("two separate verified WeCom accounts required")
	}
	facts := make([]identitydomain.VerifiedFact, 0, 4)
	for _, external := range externals {
		contact, err := reader.ReadExternalContact(ctx, external)
		if err != nil {
			return cmd, err
		}
		union, err := wecomprovider.VerifiedContactUnionID(external, scope, "wecom.directory_sync", contact)
		if err != nil {
			return cmd, err
		}
		fact, err := wecomprovider.VerifiedExternalContact(corp, external, "wecom.directory_sync")
		if err != nil {
			return cmd, err
		}
		facts = append(facts, fact, union)
	}
	if facts[1].Reference().NormalizedValue == facts[3].Reference().NormalizedValue {
		return cmd, errors.New("Provider returned same UnionID; separate-account correction refused")
	}
	cmd.LeftExternal = facts[0]
	cmd.LeftUnion = facts[1]
	cmd.RightExternal = facts[2]
	cmd.RightUnion = facts[3]
	return cmd, nil
}

func run(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("migrate-wecom-account-identities", flag.ContinueOnError)
	mode := flags.String("mode", "inspect", "inspect|dry-run|apply")
	path := flags.String("plan-file", "", "protected reviewed plan path (IDs and versions only)")
	confirmedDigest := flags.String("plan-sha256", "", "exact plan sha256 for apply")
	confirm := flags.Bool("confirm-apply", false, "apply the already authorized exact plan")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *path == "" || flags.NArg() != 0 {
		return errors.New("plan-file required; positional arguments refused")
	}
	if *mode != "inspect" && *mode != "dry-run" && *mode != "apply" {
		return errors.New("unsupported mode")
	}
	raw, err := os.ReadFile(*path)
	if err != nil {
		return errors.New("protected plan unavailable")
	}
	plan, err := loadPlan(raw)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(raw)
	digestHex := hex.EncodeToString(digest[:])
	if *mode == "apply" && (!*confirm || *confirmedDigest != digestHex) {
		return errors.New("apply requires confirmation of exact plan digest")
	}
	if *mode == "inspect" {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"mode": *mode, "plan_sha256": digestHex, "plan": plan, "writes_committed": 0})
	}
	settings, err := platformconfig.Load()
	if err != nil {
		return err
	}
	pool, err := platformpostgres.Open(ctx, platformpostgres.Config{URL: settings.DatabaseURL, MaxConnections: 2, MinConnections: 1})
	if err != nil {
		return err
	}
	defer pool.Close()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		return err
	}
	corp := settings.WeCom.CorpID
	scope := "wechat-open-platform:" + settings.WeCom.UnionIDOpenPlatformID
	if identitydomain.ValidateNamespace(identitydomain.KindUnionID, scope) != nil {
		return errors.New("verified Open Platform scope required")
	}
	query := identityquery.NewPostgreSQL()
	var externals [2]string
	err = uow.Within(ctx, func(tx context.Context) error {
		for i, customer := range []int64{plan.LeftCustomerID, plan.RightCustomerID} {
			value, found, e := query.VerifiedWeComIdentityForCustomer(tx, customerdomain.CustomerID(customer), corp)
			if e != nil {
				return e
			}
			if !found {
				return errors.New("reviewed customer has no unique verified WeCom identity")
			}
			externals[i] = value
		}
		return nil
	})
	if err != nil {
		return err
	}
	client, err := wecomadapter.NewDirectory(wecomadapter.Config{Enabled: true, CorpID: corp, ContactSecret: settings.WeCom.ContactSecret})
	if err != nil {
		return err
	}
	// Provider network reads happen after the read transaction is closed.
	cmd, err := verifyAccounts(ctx, client, plan, corp, scope, externals)
	if err != nil {
		return err
	}
	phoneKey, err := platformconfig.IdentityPhoneDataKey()
	if err != nil {
		return err
	}
	phoneVault, err := identitysecure.NewPhoneVault(phoneKey)
	if err != nil {
		return err
	}
	observationVault, err := identitysecure.NewObservationVault(settings.HXCDashboard.IdentityObservationVaultKey)
	if err != nil {
		return err
	}
	repository := identitystore.NewPostgresStoreWithObservation(phoneVault, observationVault)
	audit, err := platformaudit.NewService(platformaudit.NewPostgreSQLStore())
	if err != nil {
		return err
	}
	var result identityport.AccountCorrectionResult
	err = uow.Within(ctx, func(tx context.Context) error {
		var e error
		result, e = repository.CorrectDistinctWeComAccounts(tx, cmd)
		if e != nil {
			return e
		}
		if !result.Replayed {
			payload, _ := json.Marshal(map[string]any{"plan_sha256": digestHex, "left_customer_id": plan.LeftCustomerID, "right_customer_id": plan.RightCustomerID, "result": result})
			_, e = audit.Append(tx, platformaudit.Event{IdempotencyKey: idempotency.Key("identity:account-correction:" + strconv.FormatInt(result.EvidenceID, 10)), Action: "identity.wecom_accounts_corrected", ActorType: "maintenance", ActorID: plan.Operator, ResourceType: "identity_account_correction", ResourceID: strconv.FormatInt(result.EvidenceID, 10), Payload: payload})
			if e != nil {
				return e
			}
		}
		if *mode == "dry-run" {
			return errDryRun
		}
		return nil
	})
	if *mode == "dry-run" {
		if !errors.Is(err, errDryRun) {
			return err
		}
	} else if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"mode": *mode, "plan_sha256": digestHex, "provider_verified": true, "distinct_unionids": true, "writes_committed": *mode == "apply" && !result.Replayed, "result": result})
}
