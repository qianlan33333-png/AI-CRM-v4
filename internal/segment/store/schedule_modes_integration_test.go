package store

import (
	"context"
	"testing"
	"time"

	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	segmentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/domain"
)

func TestPostgreSQLScheduledConfigurationsKeepCombinedKindsIndependent(t *testing.T) {
	ctx := context.Background()
	native, cleanup := segmentDatabase(t, ctx)
	defer cleanup()
	wrapped, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 4, 18, 1, 0, 0, time.UTC)
	var configuration segmentdomain.ConfigurationVersion
	err = uow.Within(ctx, func(tx context.Context) error {
		group, e := repository.CreateGroup(tx, mustGroup(t, "刷新模式", now))
		if e != nil {
			return e
		}
		pkg, e := repository.CreatePackage(tx, mustPackage(t, "refresh-modes", group.ID, now))
		if e != nil {
			return e
		}
		configuration, e = segmentdomain.NewConfigurationVersion(pkg.ID, 1, []byte(`{"schema_version":1,"template_key":"wecom_contact_registration","parameters":{"owner_scope":"all","owner_staff_ids":[],"contact_statuses":["active"],"registration_status":"any"}}`), "", "every_3m_plus_daily_0200", 7, now)
		if e != nil {
			return e
		}
		configuration, e = repository.CreateConfigurationVersion(tx, configuration)
		if e != nil {
			return e
		}
		pkg, e = repository.SetCurrentConfiguration(tx, pkg.ID, configuration.ID, pkg.Version, 7, now)
		if e != nil {
			return e
		}
		locked, e := repository.LockPackage(tx, pkg.ID)
		if e != nil {
			return e
		}
		if e = locked.Transition(segmentdomain.Active, pkg.Version, 7, now); e != nil {
			return e
		}
		_, e = repository.UpdatePackage(tx, locked, pkg.Version)
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	var items []segmentdomain.ScheduledConfiguration
	if err = uow.Within(ctx, func(tx context.Context) error {
		var e error
		items, e = repository.ScheduledConfigurations(tx, 10)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("items=%+v", items)
	}
	byKind := map[string]segmentdomain.ScheduledConfiguration{}
	for _, item := range items {
		byKind[item.Kind] = item
	}
	if byKind["incremental"].CronUTC != "*/3 * * * *" || byKind["daily"].CronUTC != "0 18 * * *" {
		t.Fatalf("items=%+v", items)
	}
	claim := func(item segmentdomain.ScheduledConfiguration) bool {
		owned := false
		if e := uow.Within(ctx, func(tx context.Context) error {
			var claimErr error
			owned, claimErr = repository.ClaimScheduledOccurrence(tx, item, now, now.Add(time.Minute), now)
			return claimErr
		}); e != nil {
			t.Fatal(e)
		}
		return owned
	}
	if !claim(byKind["incremental"]) || !claim(byKind["daily"]) || claim(byKind["incremental"]) {
		t.Fatalf("combined cursors did not claim independently")
	}
	_ = configuration
}

func mustGroup(t *testing.T, name string, now time.Time) segmentdomain.Group {
	t.Helper()
	value, err := segmentdomain.NewGroup(name, 1, 7, now)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
func mustPackage(t *testing.T, code string, groupID int64, now time.Time) segmentdomain.Package {
	t.Helper()
	value, err := segmentdomain.NewPackage(code, code, &groupID, 7, now)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
