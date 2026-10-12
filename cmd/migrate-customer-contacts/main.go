// migrate-customer-contacts fills empty reusable contact groups from explicit
// historical facts. It never binds identities or replays business actions.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	customerapp "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/app"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	customerstore "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/store"
	identityquery "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/query"
	orderapp "github.com/qianlan33333-png/AI-CRM-v3/internal/order/app"
	ordersecure "github.com/qianlan33333-png/AI-CRM-v3/internal/order/secure"
	orderstore "github.com/qianlan33333-png/AI-CRM-v3/internal/order/store"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	surveyapp "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/app"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
	surveysecure "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/secure"
	surveystore "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/store"
	"os"
	"sort"
	"time"
)

type source func(context.Context, int64) ([]customerport.ContactHistory, error)
type candidate struct{ phone, address *customerport.ContactUpdate }
type summary struct {
	Mode       string         `json:"mode"`
	Candidates int            `json:"candidate_customers"`
	Accepted   int            `json:"fill_empty_transactions"`
	Skipped    map[string]int `json:"skipped"`
}

func newer(a, b *customerport.ContactUpdate) bool {
	return b == nil || a.SourceAt.After(b.SourceAt) || (a.SourceAt.Equal(b.SourceAt) && a.Source > b.Source)
}
func collect(ctx context.Context, sources []source) (map[customerdomain.CustomerID]candidate, map[string]int, error) {
	result := map[customerdomain.CustomerID]candidate{}
	skipped := map[string]int{}
	for _, read := range sources {
		var after int64
		for {
			facts, err := read(ctx, after)
			if err != nil {
				return nil, skipped, errors.New("history source unavailable")
			}
			if len(facts) == 0 {
				break
			}
			for _, f := range facts {
				if f.Cursor <= after {
					return nil, skipped, errors.New("invalid history cursor")
				}
				after = f.Cursor
				if f.SkipReason != "" {
					skipped[f.SkipReason]++
				}
				// A damaged address must not discard a separately valid phone.
				v := f.Update
				if v.CustomerID < 1 || v.SourceAt.IsZero() {
					continue
				}
				c := result[v.CustomerID]
				if v.MobileE164 != "" && newer(&v, c.phone) {
					p := v
					p.Shipping = nil
					c.phone = &p
				}
				if v.Shipping != nil && newer(&v, c.address) {
					a := v
					a.MobileE164 = ""
					c.address = &a
				}
				if c.phone != nil || c.address != nil {
					result[v.CustomerID] = c
				}
			}
		}
	}
	return result, skipped, nil
}
func apply(ctx context.Context, uow platformport.UnitOfWork, writer customerport.ContactWriter, values map[customerdomain.CustomerID]candidate) (int, error) {
	ids := make([]int, 0, len(values))
	for id := range values {
		ids = append(ids, int(id))
	}
	sort.Ints(ids)
	done := 0
	for _, id := range ids {
		c := values[customerdomain.CustomerID(id)]
		err := uow.Within(ctx, func(tx context.Context) error {
			for _, v := range []*customerport.ContactUpdate{c.phone, c.address} {
				if v != nil {
					copy := *v
					copy.OnlyIfEmpty = true
					if err := writer.UpdateContactWithin(tx, copy); err != nil {
						return err
					}
				}
			}
			return nil
		})
		if err != nil {
			return done, errors.New("contact initialization failed; original groups retained")
		}
		done++
	}
	return done, nil
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	mode := flag.String("mode", "inspect", "inspect|apply")
	mapping := flag.String("survey-mapping", "", "JSON array of exact definition_version_id, question_id, target for legacy questions")
	flag.Parse()
	if *mode != "inspect" && *mode != "apply" {
		return errors.New("invalid mode")
	}
	var allow []surveyport.LegacyContactMapping
	if *mapping != "" {
		raw, e := os.ReadFile(*mapping)
		if e != nil {
			return errors.New("mapping file unavailable")
		}
		if json.Unmarshal(raw, &allow) != nil {
			return errors.New("invalid mapping file")
		}
	}
	seen := map[surveyport.ID]bool{}
	for _, m := range allow {
		if m.DefinitionVersionID < 1 || m.QuestionID < 1 || (m.Target != "contact_phone" && m.Target != "shipping_contact") || seen[m.DefinitionVersionID] {
			return errors.New("mapping requires one explicit contact source per definition version")
		}
		seen[m.DefinitionVersionID] = true
	}
	url, err := platformconfig.DatabaseURL()
	if err != nil {
		return err
	}
	keys := platformconfig.CollectedPhoneRepairKeys()
	ctx := context.Background()
	pool, err := platformpostgres.Open(ctx, platformpostgres.Config{URL: url, MaxConnections: 2, ConnectTimeout: 5 * time.Second, HealthTimeout: 3 * time.Second})
	if err != nil {
		return errors.New("database unavailable")
	}
	defer pool.Close()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		return err
	}
	readUow, err := platformpostgres.NewReadOnlyRepeatableReadUnitOfWork(pool)
	if err != nil {
		return err
	}
	cipher, err := surveysecure.NewCipher(keys.Survey)
	if err != nil {
		return err
	}
	contacts, err := customerapp.NewContactService(customerstore.NewPostgreSQL(), cipher, identityquery.PostgreSQL{})
	if err != nil {
		return err
	}
	orders, err := orderstore.NewPostgreSQL(pool.Native(), uow)
	if err != nil {
		return err
	}
	orderService := orderapp.NewService(uow, orders)
	orderCipher, err := ordersecure.NewContactCipher(keys.OrderContact)
	if err != nil {
		return err
	}
	if err = orderService.SetContactCipher(orderCipher); err != nil {
		return err
	}
	surveys, err := surveystore.NewPostgreSQL(pool.Native(), uow, cipher)
	if err != nil {
		return err
	}
	surveyService := surveyapp.NewSubmissionService(uow, surveys, cipher)
	var values map[customerdomain.CustomerID]candidate
	report := summary{Mode: *mode}
	// Select both domains under one stable snapshot before filling any group.
	err = readUow.Within(ctx, func(tx context.Context) error {
		var e error
		values, report.Skipped, e = collect(tx, []source{orderService.ContactHistoryWithin, func(c context.Context, a int64) ([]customerport.ContactHistory, error) {
			return surveyService.ContactHistoryWithin(c, a, allow)
		}})
		return e
	})
	if err != nil {
		return err
	}
	report.Candidates = len(values)
	if *mode == "apply" {
		report.Accepted, err = apply(ctx, uow, contacts, values)
	}
	if e := json.NewEncoder(os.Stdout).Encode(report); e != nil {
		return e
	}
	return err
}
