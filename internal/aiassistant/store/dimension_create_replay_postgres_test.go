package store

import (
	"context"
	"errors"
	"testing"
	"time"

	aiapp "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/app"
	ai "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/port"
	customer "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	effect "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

type dimensionCustomers struct{ active bool }

func (r *dimensionCustomers) CustomerSnapshot(_ context.Context, id customer.CustomerID) (aiapp.CustomerSnapshot, error) {
	canonical := id
	if !r.active {
		canonical++
	}
	return aiapp.CustomerSnapshot{CanonicalID: canonical, Status: customer.StatusActive}, nil
}

type dimensionStaff struct {
	integrationStaff
	active bool
}

func (r *dimensionStaff) StaffSnapshot(_ context.Context, id int64) (aiapp.StaffSnapshot, error) {
	return aiapp.StaffSnapshot{ID: id, Active: r.active}, nil
}

// The committed receipt describes the original accepted request. Replay must
// return that plan even after mutable eligibility changes, while a new request
// and a changed payload remain subject to the existing checks.
func TestDimensionPostgreSQLCreatePlanReplaySurvivesEligibilityChanges(t *testing.T) {
	for _, kind := range []string{"human", "human_within", "machine", "machine_within"} {
		t.Run(kind, func(t *testing.T) {
			pool, cleanup := integrationPool(t)
			defer cleanup()
			wrapped, err := platformpostgres.Wrap(pool, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer wrapped.Close()
			uow, err := platformpostgres.NewUnitOfWork(wrapped)
			if err != nil {
				t.Fatal(err)
			}
			repository, err := NewPostgreSQL(pool, uow)
			if err != nil {
				t.Fatal(err)
			}
			customers, staff := &dimensionCustomers{true}, &dimensionStaff{active: true}
			service, err := aiapp.NewService(uow, repository, customers, staff, integrationMaterials{}, integrationIdentities{}, integrationIdentities{})
			if err != nil {
				t.Fatal(err)
			}
			actor, _ := ai.MachineActorFromClientID("dimension-replay")
			at := time.Date(2026, 10, 7, 1, 2, 3, 0, time.UTC)
			recipients := []ai.RecipientCandidate{{CustomerID: 11, StaffID: 21, Content: []ai.ContentBlock{{Kind: ai.ContentText, Text: "审阅建议 😀"}}}}
			create := func(key, name string) (ai.PlanID, bool, error) {
				ctx := context.Background()
				if kind == "human" || kind == "human_within" {
					command := ai.CreatePlanCommand{Actor: ai.Actor{Kind: ai.ActorAdmin, ID: 7}, IdempotencyKey: key, Name: name, SourceKind: "dimension.review", SourceDigest: effect.Hash("dimension-source"), Recipients: recipients, OccurredAt: at}
					var result ai.CreatePlanResult
					var err error
					if kind == "human_within" {
						err = uow.Within(ctx, func(tx context.Context) error { result, err = service.CreatePlanWithin(tx, command); return err })
					} else {
						result, err = service.CreatePlan(ctx, command)
					}
					return result.Plan.ID, result.Replayed, err
				}
				command := ai.MachineCreatePlanCommand{Actor: actor, IdempotencyKey: key, Name: name, SourceKind: "dimension.review", SourceDigest: effect.Hash("dimension-source"), Recipients: recipients, OccurredAt: at}
				var result ai.MachineCreatePlanResult
				var err error
				if kind == "machine_within" {
					err = uow.Within(ctx, func(tx context.Context) error { result, err = service.CreateMachinePlanWithin(tx, command); return err })
				} else {
					result, err = service.CreateMachinePlan(ctx, command)
				}
				return result.Plan.ID, result.Replayed, err
			}
			const key, name = "dimension-replay-key", "原始计划"
			first, replayed, err := create(key, name)
			if err != nil || replayed || first < 1 {
				t.Fatalf("first id=%d replay=%t err=%v", first, replayed, err)
			}
			for _, changed := range []string{"customer_merged", "staff_inactive"} {
				customers.active, staff.active = changed != "customer_merged", changed != "staff_inactive"
				id, replayed, err := create(key, name)
				if err != nil || !replayed || id != first {
					t.Errorf("%s must replay committed plan: id=%d replay=%t err=%v", changed, id, replayed, err)
				}
				if _, _, err = create(key, "改变的计划"); !errors.Is(err, aiapp.ErrIdempotencyConflict) {
					t.Errorf("%s payload drift err=%v", changed, err)
				}
				if _, _, err = create(key+"-"+changed, name); !errors.Is(err, aiapp.ErrConflict) {
					t.Errorf("%s new request skipped eligibility: %v", changed, err)
				}
			}
			var plans int
			if err = pool.QueryRow(context.Background(), `SELECT count(*) FROM ai_assistant_plans`).Scan(&plans); err != nil {
				t.Fatal(err)
			}
			if plans != 1 {
				t.Fatalf("replay or rejected request created extra plans: %d", plans)
			}
		})
	}
}
