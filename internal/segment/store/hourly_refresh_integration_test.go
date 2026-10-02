package store

import (
	"context"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	segmentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/domain"
	"testing"
	"time"
)

func TestPostgreSQLHourlyConfigurationClaimsOneIncrementalCursor(t *testing.T) {
	ctx := context.Background()
	native, cleanup := segmentDatabase(t, ctx)
	defer cleanup()
	wrapped, e := platformpostgres.Wrap(native, time.Second)
	if e != nil {
		t.Fatal(e)
	}
	uow, e := platformpostgres.NewUnitOfWork(wrapped)
	if e != nil {
		t.Fatal(e)
	}
	repo, e := NewPostgreSQL(native, uow)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Date(2026, 10, 2, 4, 1, 0, 0, time.UTC)
	e = uow.Within(ctx, func(tx context.Context) error {
		group, e := repo.CreateGroup(tx, mustGroup(t, "hourly", now))
		if e != nil {
			return e
		}
		pkg, e := repo.CreatePackage(tx, mustPackage(t, "hourly", group.ID, now))
		if e != nil {
			return e
		}
		cfg, e := segmentdomain.NewConfigurationVersion(pkg.ID, 1, []byte(`{"schema_version":1,"template_key":"paid_order","parameters":{}}`), "", "every_1h", 7, now)
		if e != nil {
			return e
		}
		cfg, e = repo.CreateConfigurationVersion(tx, cfg)
		if e != nil {
			return e
		}
		pkg, e = repo.SetCurrentConfiguration(tx, pkg.ID, cfg.ID, pkg.Version, 7, now)
		if e != nil {
			return e
		}
		locked, e := repo.LockPackage(tx, pkg.ID)
		if e != nil {
			return e
		}
		if e = locked.Transition(segmentdomain.Active, pkg.Version, 7, now); e != nil {
			return e
		}
		_, e = repo.UpdatePackage(tx, locked, pkg.Version)
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
	var items []segmentdomain.ScheduledConfiguration
	e = uow.Within(ctx, func(tx context.Context) error { var e error; items, e = repo.ScheduledConfigurations(tx, 10); return e })
	if e != nil {
		t.Fatal(e)
	}
	if len(items) != 1 || items[0].Kind != "incremental" || items[0].CronUTC != "0 * * * *" {
		t.Fatalf("hourly schedules=%+v", items)
	}
	occurrence := now.Truncate(time.Hour).Add(time.Hour)
	for index := 0; index < 2; index++ {
		var claimed bool
		e = uow.Within(ctx, func(tx context.Context) error {
			var e error
			claimed, e = repo.ClaimScheduledOccurrence(tx, items[0], occurrence, occurrence.Add(time.Hour), occurrence)
			return e
		})
		if e != nil || claimed != (index == 0) {
			t.Fatalf("claim=%d owned=%v err=%v", index, claimed, e)
		}
	}
}
