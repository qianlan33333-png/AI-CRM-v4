package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	accessport "github.com/qianlan33333-png/AI-CRM-v3/internal/access/port"
	accessstore "github.com/qianlan33333-png/AI-CRM-v3/internal/access/store"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	hxcport "github.com/qianlan33333-png/AI-CRM-v3/internal/hxcdashboard/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	segmentadapter "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/adapter"
	segmentcompiler "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/compiler"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
	"testing"
	"time"
)

type ownerUOWFacts struct{}

func (ownerUOWFacts) AudienceContacts(context.Context, time.Time) ([]wecomport.AudienceContact, error) {
	return []wecomport.AudienceContact{{CustomerID: 101, OwnerUserID: "sender-a", Status: "active"}, {CustomerID: 102, OwnerUserID: "other-owner", Status: "active"}}, nil
}
func (ownerUOWFacts) RegistrationFacts(_ context.Context, ids []customerdomain.CustomerID, at time.Time) (map[customerdomain.CustomerID]hxcport.RegistrationFact, error) {
	out := map[customerdomain.CustomerID]hxcport.RegistrationFact{}
	for _, id := range ids {
		out[id] = hxcport.RegistrationFact{State: "unregistered", AsOf: at, Version: 1}
	}
	return out, nil
}
func TestAudienceSpecifiedOwnerReusesEvaluationPostgreSQLUOW(t *testing.T) {
	native, cleanup := automationAudienceRuntimePool(t)
	defer cleanup()
	ctx := context.Background()
	staff := automationAudienceInsertProviderStaff(t, ctx, native)
	pool, e := platformpostgres.Wrap(native, time.Second)
	if e != nil {
		t.Fatal(e)
	}
	uow, e := platformpostgres.NewUnitOfWork(pool)
	if e != nil {
		t.Fatal(e)
	}
	owner := automationOpsStaffAdapter{uow: uow, users: accessstore.NewPostgreSQL()}
	if value, found, e := owner.AudienceOwnerUserID(ctx, accessport.StaffID(staff)); e != nil || !found || value != "sender-a" {
		t.Fatalf("standalone read failed found=%v err=%v", found, e)
	}
	legacy := &segmentadapter.LegacyTemplateSource{Owners: owner, Contacts: ownerUOWFacts{}, HXCRegistration: ownerUOWFacts{}}
	source := segmentadapter.CustomerSource{UoW: uow, Legacy: legacy}
	definition, e := (segmentcompiler.Compiler{}).Compile(json.RawMessage(fmt.Sprintf(`{"schema_version":1,"template_key":"hxc_registration","parameters":{"owner_scope":"specified","owner_staff_ids":["%d"],"registration_status":"unregistered"}}`, staff)))
	if e != nil {
		t.Fatal(e)
	}
	result, e := source.Evaluate(ctx, definition, time.Now())
	if e != nil || len(result.CustomerIDs) != 1 || result.CustomerIDs[0] != 101 {
		t.Fatalf("specified owner result=%v error=%v", result.CustomerIDs, e)
	}
	// Verify the read observes an uncommitted owner change in exactly the
	// caller transaction and rolls back together; a second connection cannot.
	rollback := errors.New("test rollback")
	if e := uow.Within(ctx, func(txctx context.Context) error {
		tx, e := platformpostgres.RequireTransaction(txctx)
		if e != nil {
			return e
		}
		if _, e = tx.Exec(txctx, `UPDATE admin_users SET wecom_userid='transaction-only' WHERE id=$1`, staff); e != nil {
			return e
		}
		value, found, e := owner.AudienceOwnerUserID(txctx, accessport.StaffID(staff))
		if e != nil {
			return e
		}
		if !found || value != "transaction-only" {
			t.Fatal("owner read escaped caller transaction")
		}
		return rollback
	}); !errors.Is(e, rollback) {
		t.Fatal(e)
	}
	value, _, e := owner.AudienceOwnerUserID(ctx, accessport.StaffID(staff))
	if e != nil || value != "sender-a" {
		t.Fatal("rollback not retained")
	}
}
