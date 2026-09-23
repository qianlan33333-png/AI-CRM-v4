package store

import (
	"context"
	"errors"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	"testing"
)

func TestRegistrationCoveragePublicationIsAtomicAndMissingUnknown(t *testing.T) {
	native, uow, cleanup := hxcIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	s := NewPostgreSQL(native)
	var id customerdomain.CustomerID
	if e := native.QueryRow(ctx, `INSERT INTO customers(id)VALUES(919)RETURNING id`).Scan(&id); e != nil {
		t.Fatal(e)
	}
	var run int64
	if e := native.QueryRow(ctx, `INSERT INTO hxc_dashboard_refresh_runs(run_key,request_digest,trigger,identity_mode,status)VALUES('registration-test',decode(repeat('11',32),'hex'),'initial','inspect','publishing') RETURNING id`).Scan(&run); e != nil {
		t.Fatal(e)
	}
	p := hxcRetentionProjection(1)
	p.RegistrationCoverage = map[int64]string{int64(id): "unregistered"}
	sentinel := errors.New("rollback")
	if e := uow.Within(ctx, func(tx context.Context) error {
		if _, e := s.Publish(tx, run, p); e != nil {
			return e
		}
		return sentinel
	}); !errors.Is(e, sentinel) {
		t.Fatal(e)
	}
	var count int
	if e := native.QueryRow(ctx, `SELECT count(*) FROM hxc_registration_coverage`).Scan(&count); e != nil || count != 0 {
		t.Fatalf("rollback count=%d error=%v", count, e)
	}
	if e := uow.Within(ctx, func(tx context.Context) error { _, e := s.Publish(tx, run, p); return e }); e != nil {
		t.Fatal(e)
	}
	if e := uow.Within(ctx, func(tx context.Context) error {
		facts, e := s.RegistrationFacts(tx, []customerdomain.CustomerID{id, 99999}, p.AsOf)
		if e != nil {
			return e
		}
		if facts[id].State != "unregistered" || facts[id].Version <= 0 || facts[99999].State != "unknown" {
			t.Fatal("generation fact lost or absent treated as negative")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}
