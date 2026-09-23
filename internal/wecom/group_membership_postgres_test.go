package wecom

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	cfgplatform "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	pg "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
	"os"
	"testing"
	"time"
)

func TestGroupMembershipPostgresFreshnessAndCAS(t *testing.T) {
	dsn, dsnErr := cfgplatform.DatabaseURL()
	if dsnErr != nil || dsn == "" {
		t.Skip("AICRM_DATABASE_URL required")
	}
	ctx := context.Background()
	admin, e := pgxpool.New(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer admin.Close()
	schema := fmt.Sprintf("test_group_members_%d", time.Now().UnixNano())
	if _, e = admin.Exec(ctx, "CREATE SCHEMA "+schema); e != nil {
		t.Fatal(e)
	}
	defer admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
	cfg, e := pgxpool.ParseConfig(dsn)
	if e != nil {
		t.Fatal(e)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	native, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer native.Close()
	migration, e := os.ReadFile("../../migrations/0136_wecom_group_membership_facts.sql")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = native.Exec(ctx, string(migration)); e != nil {
		t.Fatal(e)
	}
	historyMigration, e := os.ReadFile("../../migrations/0137_wecom_group_membership_observations.sql")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = native.Exec(ctx, string(historyMigration)); e != nil {
		t.Fatal(e)
	}
	rawMigration, e := os.ReadFile("../../migrations/0139_wecom_group_provider_membership.sql")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = native.Exec(ctx, string(rawMigration)); e != nil {
		t.Fatal(e)
	}
	p, e := pg.Wrap(native, time.Second)
	if e != nil {
		t.Fatal(e)
	}
	u, e := pg.NewUnitOfWork(p)
	if e != nil {
		t.Fatal(e)
	}
	s := PostgreSQLGroupMembershipFacts{}
	at := time.Now().UTC().Truncate(time.Microsecond)
	save := func(f wecomport.AudienceGroupMembership, code string) error {
		return u.Within(ctx, func(c context.Context) error { return s.SaveGroupMembership(c, "wecom-corp:c", "g", f, code) })
	}
	read := func(corp, chat string, at time.Time) error {
		return u.Within(ctx, func(c context.Context) error {
			_, e := s.AudienceGroupMembership(c, corp, chat, at, time.Minute)
			return e
		})
	}
	f := wecomport.AudienceGroupMembership{ObservedAt: at, Complete: true}
	if e = read("wecom-corp:c", "g", at); e == nil {
		t.Fatal("missing treated empty")
	}
	if e = save(f, ""); e != nil {
		t.Fatal(e)
	}
	if e = read("wecom-corp:c", "g", at); e != nil {
		t.Fatal(e)
	}
	for _, item := range []struct {
		corp, chat string
		at         time.Time
	}{{"wecom-corp:other", "g", at}, {"wecom-corp:c", "g", at.Add(2 * time.Minute)}, {"wecom-corp:c", "g", at.Add(-time.Second)}} {
		if e = read(item.corp, item.chat, item.at); e == nil {
			t.Fatal("unsafe snapshot accepted")
		}
	}
	f.ObservedAt = at.Add(time.Second)
	f.Complete = false
	if e = save(f, "refreshing"); e != nil {
		t.Fatal(e)
	}
	if e = read("wecom-corp:c", "g", f.ObservedAt); e == nil {
		t.Fatal("inflight old success usable")
	}
	f.Complete = true
	if e = save(f, ""); e != nil {
		t.Fatal(e)
	}
	if e = read("wecom-corp:c", "g", at); e != nil {
		t.Fatal("later refresh erased frozen reference observation", e)
	}
	old := f
	old.ObservedAt = at
	if e = save(old, ""); e == nil {
		t.Fatal("stale writer replaced latest")
	}
	f.ObservedAt = at.Add(2 * time.Second)
	f.Complete = false
	f.ExternalCount = 1
	f.UnresolvedCount = 1
	if e = save(f, "identity_unresolved"); e != nil {
		t.Fatal(e)
	}
	if e = read("wecom-corp:c", "g", f.ObservedAt); e == nil {
		t.Fatal("partial identity snapshot usable")
	}
	// Provider completeness survives partial CRM coverage; old canonical port stays closed.
	f.ObservedAt = at.Add(3 * time.Second)
	f.ProviderComplete = true
	f.ExternalIdentityHashes = []string{groupExternalHash("wecom-corp:c", "unresolved")}
	if e = save(f, "identity_unresolved"); e != nil {
		t.Fatal(e)
	}
	providerRead := func(reference time.Time) error {
		return u.Within(ctx, func(tx context.Context) error {
			got, err := (GroupProviderFacts{}).AudienceGroupMembership(tx, "wecom-corp:c", "g", reference, time.Minute)
			if err == nil && (!got.ProviderComplete || len(got.ExternalIdentityHashes) != 1) {
				t.Fatal("incomplete provider facts")
			}
			return err
		})
	}
	if e = providerRead(f.ObservedAt); e != nil {
		t.Fatal("raw complete membership unavailable", e)
	}
	if e = read("wecom-corp:c", "g", f.ObservedAt); e == nil {
		t.Fatal("canonical port weakened")
	}
	if e = providerRead(f.ObservedAt.Add(2 * time.Minute)); e == nil {
		t.Fatal("stale raw facts allowed")
	}
	previous := f.ObservedAt
	f.ObservedAt = at.Add(4 * time.Second)
	f.ProviderComplete = false
	f.ExternalIdentityHashes = nil
	if e = save(f, "provider_read_failed"); e != nil {
		t.Fatal(e)
	}
	if e = providerRead(previous); e == nil {
		t.Fatal("latest provider failure left old facts usable")
	}

}
