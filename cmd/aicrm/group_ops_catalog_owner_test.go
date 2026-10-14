package main

import (
	"context"
	accessstore "github.com/qianlan33333-png/AI-CRM-v3/internal/access/store"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"testing"
	"time"
)

func TestGroupOpsPostgreSQLCatalogOwnerMapsOnlyExistingActiveStaff(t *testing.T) {
	native, cleanup := groupOpsIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	pool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	var actor int64
	if err = native.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name,wecom_userid,is_active,login_enabled) VALUES('catalog-owner','$argon2id$test','运营者','catalog-owner',true,false) RETURNING id`).Scan(&actor); err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, `INSERT INTO admin_users(username,password_hash,display_name,wecom_userid,is_active,login_enabled) VALUES('catalog-inactive','$argon2id$test','停用员工','catalog-inactive',false,false)`); err != nil {
		t.Fatal(err)
	}
	adapter := groupOpsCatalogOwnerAdapter{uow: uow, access: accessstore.NewPostgreSQL()}
	for id, want := range map[string]int64{"catalog-owner": actor, "catalog-inactive": 0, "catalog-unknown": 0} {
		got, err := adapter.ResolveOwner(ctx, id)
		if err != nil || got != want {
			t.Fatalf("owner lookup got=%d want=%d err=%v", got, want, err)
		}
	}
	var count int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM admin_users`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("owner lookup provisioned staff count=%d err=%v", count, err)
	}
}
