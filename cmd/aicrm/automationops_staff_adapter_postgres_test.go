package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	accessstore "github.com/qianlan33333-png/AI-CRM-v3/internal/access/store"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func TestPostgreSQLAutomationSenderProjectionSharesCallerTransaction(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	dsn, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()
	native, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer native.Close()
	wrapped, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	adapter := automationOpsStaffAdapter{uow: uow, users: accessstore.NewPostgreSQL()}
	rollback := errors.New("synthetic rollback")
	err = uow.Within(ctx, func(tx context.Context) error {
		db, e := platformpostgres.RequireTransaction(tx)
		if e != nil {
			return e
		}
		var id int64
		if e = db.QueryRow(tx, `INSERT INTO admin_users(username,password_hash,display_name,wecom_userid,is_active,login_enabled) VALUES('sender-uow-fixture','$argon2id$test','Sender transaction','synthetic-sender-uow',FALSE,FALSE) RETURNING id`).Scan(&id); e != nil {
			return e
		}
		item, found, e := adapter.ResolveAutomationSender(tx, "synthetic-sender-uow")
		if e != nil || !found || int64(item.StaffID) != id || item.Active || item.Eligible {
			t.Fatalf("local sender read did not see its caller's uncommitted, inactive employee: found=%t id_matches=%t inactive=%t err=%v", found, int64(item.StaffID) == id, !item.Active && !item.Eligible, e)
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	if _, found, e := adapter.ResolveAutomationSender(ctx, "synthetic-sender-uow"); e != nil || found {
		t.Fatalf("standalone read observed a rolled-back employee: found=%t err=%v", found, e)
	}
}
