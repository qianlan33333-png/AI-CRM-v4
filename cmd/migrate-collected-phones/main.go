// Command migrate-collected-phones restores submitted Order/Survey phone facts
// to OneID. It uses domain applications, not cross-domain table access, and
// never executes purchase/completion actions or Provider calls.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	customerapp "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/app"
	customerstore "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/store"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	identityquery "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/query"
	identitysecure "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/secure"
	identitystore "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/store"
	orderapp "github.com/qianlan33333-png/AI-CRM-v3/internal/order/app"
	ordersecure "github.com/qianlan33333-png/AI-CRM-v3/internal/order/secure"
	orderstore "github.com/qianlan33333-png/AI-CRM-v3/internal/order/store"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	surveyapp "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/app"
	surveysecure "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/secure"
	surveystore "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/store"
)

type replaySource interface {
	List(context.Context, int64, int64) ([]int64, error)
	Replay(context.Context, int64) (identityport.DeclaredAttachResult, error)
}

type recoverySource interface {
	InspectRecovery(context.Context, int64) (identityport.PhoneRecoveryResult, error)
	Recover(context.Context, int64) (identityport.PhoneRecoveryResult, error)
}

type domainSource struct {
	inspect func(context.Context, int64) (identityport.PhoneRecoveryResult, error)
	recover func(context.Context, int64) (identityport.PhoneRecoveryResult, error)
	list    func(context.Context, int64, int64) ([]int64, error)
	replay  func(context.Context, int64) (identityport.DeclaredAttachResult, error)
}

func (s domainSource) List(ctx context.Context, after, through int64) ([]int64, error) {
	return s.list(ctx, after, through)
}
func (s domainSource) Replay(ctx context.Context, id int64) (identityport.DeclaredAttachResult, error) {
	return s.replay(ctx, id)
}

func (s domainSource) InspectRecovery(ctx context.Context, id int64) (identityport.PhoneRecoveryResult, error) {
	return s.inspect(ctx, id)
}
func (s domainSource) Recover(ctx context.Context, id int64) (identityport.PhoneRecoveryResult, error) {
	return s.recover(ctx, id)
}

type summary struct {
	Mode            string           `json:"mode"`
	Source          string           `json:"source"`
	Candidates      int64            `json:"candidates"`
	Outcomes        map[string]int64 `json:"outcomes"`
	LastCompletedID int64            `json:"last_completed_id"`
	FailedSourceID  int64            `json:"failed_source_id,omitempty"`
}

// inspect is strictly a source inventory: no binding is attempted, even in a
// rollback transaction. Each apply item is atomic and its source key survives
// interruption; a retry can start at LastCompletedID or from the beginning.
func replay(ctx context.Context, uow platformport.UnitOfWork, source replaySource, mode, name string, after, through int64) (summary, error) {
	report := summary{Mode: mode, Source: name, Outcomes: map[string]int64{}, LastCompletedID: after}
	if mode != "inspect" && mode != "apply" && mode != "recover-canonical" || mode == "recover-canonical" && (through < 1 || name == "all") || after < 0 || through < 0 || through > 0 && through < after {
		return report, errors.New("invalid replay options")
	}
	for {
		var ids []int64
		err := uow.Within(ctx, func(tx context.Context) error {
			var e error
			ids, e = source.List(tx, report.LastCompletedID, through)
			return e
		})
		if err != nil {
			return report, errors.New("source inventory unavailable")
		}
		if len(ids) == 0 {
			return report, nil
		}
		for _, id := range ids {
			if id <= report.LastCompletedID || through > 0 && id > through {
				return report, errors.New("invalid source cursor")
			}
			if mode == "apply" {
				var result identityport.DeclaredAttachResult
				err = uow.Within(ctx, func(tx context.Context) error { var e error; result, e = source.Replay(tx, id); return e })
				if err != nil {
					report.FailedSourceID = id
					return report, errors.New("phone replay failed; retry original source key")
				}
				outcome := string(result.Status)
				if result.Status == identityport.DeclaredReplayed {
					outcome = "replayed:" + string(result.ReplayOf)
				}
				report.Outcomes[outcome]++
			}
			if mode == "inspect" || mode == "recover-canonical" {
				if recovery, ok := source.(recoverySource); ok {
					var result identityport.PhoneRecoveryResult
					err = uow.Within(ctx, func(tx context.Context) error {
						var e error
						if mode == "inspect" {
							result, e = recovery.InspectRecovery(tx, id)
						} else {
							result, e = recovery.Recover(tx, id)
						}
						return e
					})
					if err != nil {
						report.FailedSourceID = id
						return report, errors.New("canonical phone classification/recovery failed; preserve original source key")
					}
					report.Outcomes[result.Disposition]++
				} else if mode == "recover-canonical" {
					return report, errors.New("recovery capability unavailable")
				}
			}

			report.Candidates++
			report.LastCompletedID = id
		}
	}
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "collected phone repair failed")
		os.Exit(1)
	}
}
func run() error {
	mode := flag.String("mode", "inspect", "inspect|apply|recover-canonical")
	sourceName := flag.String("source", "all", "order|survey|all")
	after := flag.Int64("after-id", 0, "last completed source ID (single source only)")
	through := flag.Int64("through-id", 0, "inclusive source watermark (single source only)")
	flag.Parse()
	if *mode != "inspect" && *mode != "apply" && *mode != "recover-canonical" || *mode == "recover-canonical" && (*through < 1 || *sourceName == "all") || *sourceName != "order" && *sourceName != "survey" && *sourceName != "all" || *sourceName == "all" && (*after != 0 || *through != 0) {
		return errors.New("invalid arguments")
	}
	keys := platformconfig.CollectedPhoneRepairKeys()
	url, err := platformconfig.DatabaseURL()
	if err != nil {
		return err
	}
	ctx := context.Background()
	pool, err := platformpostgres.Open(ctx, platformpostgres.Config{URL: url, MaxConnections: 2, ConnectTimeout: 5 * time.Second, HealthTimeout: 3 * time.Second})
	if err != nil {
		return err
	}
	defer pool.Close()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		return err
	}
	// Inventory uses a genuinely read-only transaction. Apply uses the existing
	// write UoW shared by the source, Identity and Customer projections.
	var runnerUow platformport.UnitOfWork = uow
	if *mode == "inspect" {
		runnerUow, err = platformpostgres.NewReadOnlyRepeatableReadUnitOfWork(pool)
		if err != nil {
			return err
		}
	}
	var collector *customerapp.CollectedPhoneService
	{
		key, e := platformconfig.IdentityPhoneDataKey()
		if e != nil {
			return e
		}
		vault, e := identitysecure.NewPhoneVault(key)
		if e != nil {
			return e
		}
		collector, e = customerapp.NewCollectedPhoneService(identityapp.OneIDService{Store: identitystore.NewPostgresStore(vault).WithCanonicalPhoneRecovery(identityquery.PostgreSQL{})}, customerstore.NewPostgreSQL())
		if e != nil {
			return e
		}
	}
	var sources = map[string]replaySource{}
	if *sourceName == "order" || *sourceName == "all" {
		store, e := orderstore.NewPostgreSQL(pool.Native(), uow)
		if e != nil {
			return e
		}
		app := orderapp.NewService(uow, store)
		{
			cipher, e := ordersecure.NewContactCipher(keys.OrderContact)
			if e != nil {
				return e
			}
			if e = app.SetContactCipher(cipher); e != nil {
				return e
			}
			if e = app.SetCollectedPhoneRecorder(collector); e != nil {
				return e
			}
		}
		sources["order"] = domainSource{list: app.CollectedPhoneOrderIDsWithin, replay: app.ReplayCollectedPhoneWithin, inspect: app.InspectCollectedPhoneRecoveryWithin, recover: app.RecoverCollectedPhoneWithin}
	}
	if *sourceName == "survey" || *sourceName == "all" {
		// Repository's cipher remains required for its safe default reads. The key
		// is held in memory only and is never written to reports or flags.
		cipher, e := surveysecure.NewCipher(keys.Survey)
		if e != nil {
			return e
		}
		store, e := surveystore.NewPostgreSQL(pool.Native(), uow, cipher)
		if e != nil {
			return e
		}
		app := surveyapp.NewSubmissionService(uow, store, cipher)
		{
			if e = app.BindCollectedPhone(collector); e != nil {
				return e
			}
		}
		sources["survey"] = domainSource{list: app.CollectedPhoneAnswerIDsWithin, replay: app.ReplayCollectedPhoneWithin, inspect: app.InspectCollectedPhoneRecoveryWithin, recover: app.RecoverCollectedPhoneWithin}
	}
	for _, name := range []string{"order", "survey"} {
		source, ok := sources[name]
		if !ok {
			continue
		}
		report, e := replay(ctx, runnerUow, source, *mode, name, *after, *through)
		if err = json.NewEncoder(os.Stdout).Encode(report); err != nil {
			return err
		}
		if e != nil {
			return e
		}
	}
	return nil
}
