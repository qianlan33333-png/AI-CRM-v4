package main

import (
	"context"
	"strings"
	"testing"
	"time"

	groupopsport "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/port"
	groupopsstore "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/store"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func TestGroupOpsDirectoryPersistsMixedNamedAndUnnamedGroups(t *testing.T) {
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
	store, err := groupopsstore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	var owner int64
	if err = native.QueryRow(ctx, `WITH account AS (
		INSERT INTO admin_users(username,password_hash,display_name,wecom_userid,is_active,login_enabled)
		VALUES('unnamed-groups','$argon2id$test','Directory Test','unnamed-owner',true,false) RETURNING id
	), role AS (
		INSERT INTO admin_user_roles(admin_user_id,role_code) SELECT id,'viewer' FROM account
	) SELECT id FROM account`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	items := []groupopsport.GroupDirectoryItem{
		{ChatReference: "named-chat", OwnerStaffID: owner, DisplayName: "真实群名", MemberCount: 2, RefreshedAt: now},
		{ChatReference: "unnamed-chat", OwnerStaffID: owner, DisplayName: "", MemberCount: 1, RefreshedAt: now},
	}
	if err = uow.Within(ctx, func(tx context.Context) error { return store.ReplaceDirectoryGroups(tx, owner, items, now) }); err != nil {
		t.Fatal(err)
	}
	if err = uow.Within(ctx, func(tx context.Context) error {
		rows, total, readErr := store.ListDirectoryGroups(tx, owner, "", 100, 0)
		if readErr != nil {
			return readErr
		}
		if total != 2 || len(rows) != 2 || rows[0].DisplayName != "真实群名" || rows[1].DisplayName != "" {
			t.Fatalf("directory names were lost or fabricated: %+v", rows)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err = uow.Within(ctx, func(tx context.Context) error {
		rows, total, readErr := store.ListDirectoryGroups(tx, owner, "真实", 1, 0)
		if readErr != nil {
			return readErr
		}
		if total != 1 || len(rows) != 1 || rows[0].ChatReference != "named-chat" {
			t.Fatalf("server query did not return the matching owner page: total=%d rows=%+v", total, rows)
		}
		rows, total, readErr = store.ListDirectoryGroups(tx, owner, "%", 100, 0)
		if readErr != nil {
			return readErr
		}
		if total != 0 || len(rows) != 0 {
			t.Fatalf("literal wildcard query escaped its owner directory scope: total=%d rows=%+v", total, rows)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// The migration relaxes absence only, not the existing length constraint.
	items[1].DisplayName = strings.Repeat("a", 129)
	if err = uow.Within(ctx, func(tx context.Context) error { return store.ReplaceDirectoryGroups(tx, owner, items, now) }); err == nil {
		t.Fatal("invalid long name accepted")
	}
}
