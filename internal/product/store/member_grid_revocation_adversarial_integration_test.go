package store_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformoutbox "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/outbox"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	productapp "github.com/qianlan33333-png/AI-CRM-v3/internal/product/app"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
	productstore "github.com/qianlan33333-png/AI-CRM-v3/internal/product/store"
)

func TestPostgreSQLMemberGridLegacyShareRemainsRevocableAfterArchive(t *testing.T) {
	f := newMemberGridAdversarialFixture(t)
	actor := productport.MemberGridActor{AdminUserID: 1, IsAdmin: true}
	share, issued, err := f.workspace.SetShare(f.ctx, productport.SetMemberGridShareCommand{ProductID: f.productID, Enabled: true, Actor: actor, IdempotencyKey: "legacy-grid-share-issue-0001"})
	if err != nil || !issued || !share.Enabled {
		t.Fatalf("issue=%+v issued=%v err=%v", share, issued, err)
	}
	archived, err := f.periods.ArchiveServicePeriodProduct(f.ctx, productport.ArchiveServicePeriodProductCommand{ID: f.productID, ExpectedVersion: 1, Actor: 1, IdempotencyKey: "legacy-grid-product-archive-0001"})
	if err != nil || !archived.Archived {
		t.Fatalf("archive=%+v err=%v", archived, err)
	}
	if _, err = f.workspace.ResolveShare(f.ctx, share.PublicID); err != nil {
		t.Fatalf("historical share unexpectedly lost: %v", err)
	}
	revoke := productport.SetMemberGridShareCommand{ProductID: f.productID, Enabled: false, ExpectedVersion: share.Version, Actor: actor, IdempotencyKey: "legacy-grid-share-revoke-0001"}
	revoked, issued, err := f.workspace.SetShare(f.ctx, revoke)
	if err != nil || issued || revoked.Enabled || revoked.PublicID != "" || revoked.Version != share.Version+1 {
		t.Fatalf("archived capability cannot be revoked: value=%+v issued=%v err=%v", revoked, issued, err)
	}
	if _, err = f.workspace.ResolveShare(f.ctx, share.PublicID); !errors.Is(err, productapp.ErrNotFound) {
		t.Fatalf("revoked token resolves: %v", err)
	}
	replay, replayIssued, err := f.workspace.SetShare(f.ctx, revoke)
	if err != nil || replayIssued || replay.Version != revoked.Version || replay.Enabled {
		t.Fatalf("revoke replay=%+v issued=%v err=%v", replay, replayIssued, err)
	}
	if _, _, err = f.workspace.SetShare(f.ctx, productport.SetMemberGridShareCommand{ProductID: f.productID, Enabled: true, ExpectedVersion: revoked.Version, Actor: actor, IdempotencyKey: "legacy-grid-share-reenable-0001"}); !errors.Is(err, productapp.ErrNotFound) {
		t.Fatalf("archived product reopened share: %v", err)
	}
	var enabled bool
	var version, receipts, audit, outbox int64
	if err = f.native.QueryRow(f.ctx, `SELECT enabled,version,(SELECT count(*) FROM product_operation_receipts),(SELECT count(*) FROM audit_events),(SELECT count(*) FROM outbox_events) FROM product_service_period_member_shares WHERE product_id=$1`, f.productID).Scan(&enabled, &version, &receipts, &audit, &outbox); err != nil || enabled || version != revoked.Version || receipts != 3 || audit != 3 || outbox != 3 {
		t.Fatalf("durable state enabled=%v version=%d receipts=%d audit=%d outbox=%d err=%v", enabled, version, receipts, audit, outbox, err)
	}
}

func TestPostgreSQLMemberGridReceiptNamespaceMatchesAuditAndOutbox(t *testing.T) {
	for _, variant := range []string{"cross_actor", "cross_operation"} {
		t.Run(variant, func(t *testing.T) {
			f := newMemberGridAdversarialFixture(t)
			key := "member-grid-shared-browser-key-0001"
			actor := productport.MemberGridActor{AdminUserID: 1, IsAdmin: true}
			first := productport.CreateMemberGridViewCommand{ProductID: f.productID, Name: "First view", Config: json.RawMessage(`{"schema_version":1}`), Actor: actor, IdempotencyKey: key}
			view, err := f.workspace.CreateView(f.ctx, first)
			if err != nil {
				t.Fatalf("first view: %v", err)
			}
			if variant == "cross_actor" {
				second := first
				second.Actor.AdminUserID, second.Name = 2, "Second view"
				other, err := f.workspace.CreateView(f.ctx, second)
				if err != nil || other.ID == view.ID {
					t.Fatalf("independent actor collided with audit/outbox: view=%+v err=%v", other, err)
				}
				if replay, err := f.workspace.CreateView(f.ctx, second); err != nil || replay.ID != other.ID {
					t.Fatalf("second actor replay=%+v err=%v", replay, err)
				}
			} else {
				command := productport.CreateMemberGridCollaboratorCommand{ProductID: f.productID, AdminUserID: 2, Permission: "read", Actor: actor, IdempotencyKey: key}
				collaborator, err := f.workspace.CreateCollaborator(f.ctx, command)
				if err != nil {
					t.Fatalf("independent operation collided with audit/outbox: %v", err)
				}
				if replay, err := f.workspace.CreateCollaborator(f.ctx, command); err != nil || replay.ID != collaborator.ID {
					t.Fatalf("operation replay=%+v err=%v", replay, err)
				}
			}
			if replay, err := f.workspace.CreateView(f.ctx, first); err != nil || replay.ID != view.ID {
				t.Fatalf("first replay=%+v err=%v", replay, err)
			}
			var receipts, audit, outbox int64
			if err = f.native.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM product_operation_receipts),(SELECT count(*) FROM audit_events),(SELECT count(*) FROM outbox_events)`).Scan(&receipts, &audit, &outbox); err != nil || receipts != 2 || audit != 2 || outbox != 2 {
				t.Fatalf("receipts=%d audit=%d outbox=%d err=%v", receipts, audit, outbox, err)
			}
		})
	}
}

func TestPostgreSQLMemberGridRejectsObjectSwapsAndStaleCollaboratorAccess(t *testing.T) {
	f := newMemberGridAdversarialFixture(t)
	admin := productport.MemberGridActor{AdminUserID: 1, IsAdmin: true}
	var otherProductID productport.ID
	if err := f.native.QueryRow(f.ctx, `INSERT INTO products(product_code,name,price_minor,currency,stock_quantity,created_by,legacy_admin_projection) VALUES('grid-other','Other grid',100,'CNY',0,1,'{"schema_version":1,"status":"service_period_enabled","enabled":true}') RETURNING id`).Scan(&otherProductID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.native.Exec(f.ctx, `INSERT INTO product_imported_service_period_definitions(product_id,duration_days) VALUES($1,30)`, otherProductID); err != nil {
		t.Fatal(err)
	}
	config := json.RawMessage(`{"schema_version":1}`)
	view, err := f.workspace.CreateView(f.ctx, productport.CreateMemberGridViewCommand{ProductID: f.productID, Name: "Retained view", Config: config, Actor: admin, IdempotencyKey: "grid-object-swap-view-create-0001"})
	if err != nil {
		t.Fatal(err)
	}
	collaborator, err := f.workspace.CreateCollaborator(f.ctx, productport.CreateMemberGridCollaboratorCommand{ProductID: f.productID, AdminUserID: 2, Permission: "edit", Actor: admin, IdempotencyKey: "grid-object-swap-collaborator-create-0001"})
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []struct {
		name string
		call func() error
	}{
		{"view.update", func() error {
			_, e := f.workspace.UpdateView(f.ctx, productport.UpdateMemberGridViewCommand{ProductID: otherProductID, ViewID: view.ID, Name: "Swapped", Config: config, ExpectedVersion: view.Version, Actor: admin, IdempotencyKey: "grid-object-swap-view-update-0001"})
			return e
		}},
		{"view.delete", func() error {
			_, e := f.workspace.DeleteView(f.ctx, productport.DeleteMemberGridViewCommand{ProductID: otherProductID, ViewID: view.ID, ExpectedVersion: view.Version, Actor: admin, IdempotencyKey: "grid-object-swap-view-delete-0001"})
			return e
		}},
		{"collaborator.update", func() error {
			_, e := f.workspace.UpdateCollaborator(f.ctx, productport.UpdateMemberGridCollaboratorCommand{ProductID: otherProductID, CollaboratorID: collaborator.ID, Permission: "read", ExpectedVersion: collaborator.Version, Actor: admin, IdempotencyKey: "grid-object-swap-collaborator-update-0001"})
			return e
		}},
		{"collaborator.delete", func() error {
			_, e := f.workspace.DeleteCollaborator(f.ctx, productport.DeleteMemberGridCollaboratorCommand{ProductID: otherProductID, CollaboratorID: collaborator.ID, ExpectedVersion: collaborator.Version, Actor: admin, IdempotencyKey: "grid-object-swap-collaborator-delete-0001"})
			return e
		}},
	} {
		t.Run(action.name, func(t *testing.T) {
			if e := action.call(); !errors.Is(e, productapp.ErrNotFound) {
				t.Fatalf("cross-product object accepted: %v", e)
			}
		})
	}
	actor := productport.MemberGridActor{AdminUserID: 2}
	access, err := f.workspace.Access(f.ctx, f.productID, actor)
	if err != nil || !access.CanEdit {
		t.Fatalf("pre-read access=%+v err=%v", access, err)
	}
	if _, err = f.workspace.UpdateCollaborator(f.ctx, productport.UpdateMemberGridCollaboratorCommand{ProductID: f.productID, CollaboratorID: collaborator.ID, Permission: "read", ExpectedVersion: collaborator.Version, Actor: admin, IdempotencyKey: "grid-access-downgrade-collaborator-0001"}); err != nil {
		t.Fatal(err)
	}
	if _, err = f.workspace.UpdateView(f.ctx, productport.UpdateMemberGridViewCommand{ProductID: f.productID, ViewID: view.ID, Name: "Stale access", Config: config, ExpectedVersion: view.Version, Actor: actor, IdempotencyKey: "grid-stale-collaborator-write-0001"}); !errors.Is(err, productapp.ErrNotFound) {
		t.Fatalf("stale capability reached write: %v", err)
	}
	var name, permission string
	var version, receipts, audit, outbox int64
	if err = f.native.QueryRow(f.ctx, `SELECT name,version,(SELECT permission FROM product_service_period_member_collaborators WHERE id=$2),(SELECT count(*) FROM product_operation_receipts),(SELECT count(*) FROM audit_events),(SELECT count(*) FROM outbox_events) FROM product_service_period_member_views WHERE id=$1`, view.ID, collaborator.ID).Scan(&name, &version, &permission, &receipts, &audit, &outbox); err != nil || name != view.Name || version != view.Version || permission != "read" || receipts != 3 || audit != 3 || outbox != 3 {
		t.Fatalf("denied mutation persisted: name=%s version=%d permission=%s receipts=%d audit=%d outbox=%d err=%v", name, version, permission, receipts, audit, outbox, err)
	}
}

type memberGridAdversarialFixture struct {
	ctx       context.Context
	native    *pgxpool.Pool
	productID productport.ID
	workspace *productapp.MemberGridWorkspaceService
	periods   *productapp.ServicePeriodService
}

func newMemberGridAdversarialFixture(t *testing.T) memberGridAdversarialFixture {
	t.Helper()
	databaseURL, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	adminConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.NewWithConfig(ctx, adminConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	var random [8]byte
	if _, err = rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	schema := "aicrm_grid_adversary_" + hex.EncodeToString(random[:])
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := admin.Exec(cleanupCtx, "DROP SCHEMA "+identifier+" CASCADE"); err != nil {
			t.Errorf("drop test schema: %v", err)
		}
	})
	config := adminConfig.Copy()
	config.ConnConfig.RuntimeParams["search_path"] = schema
	native, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(native.Close)
	for _, name := range []string{"0001_platform.sql", "0003_access.sql", "0010_product.sql", "0235_wechat_shop_products.sql", "0241_product_variants.sql", "0079_service_period_member_grid.sql"} {
		migration, err := os.ReadFile(memberGridMigration(t, name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = native.Exec(ctx, string(migration)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
	}
	for _, sql := range []string{
		`CREATE TABLE outbox_events(id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,aggregate_type TEXT NOT NULL,aggregate_id TEXT NOT NULL,event_type TEXT NOT NULL,event_version SMALLINT NOT NULL,idempotency_key TEXT NOT NULL UNIQUE,payload_json JSONB NOT NULL,occurred_at TIMESTAMPTZ NOT NULL,processed_at TIMESTAMPTZ)`,
		`CREATE TABLE product_imported_service_period_definitions(product_id BIGINT PRIMARY KEY REFERENCES products(id),duration_days INTEGER NOT NULL CHECK(duration_days>0))`,
		`INSERT INTO admin_users(username,password_hash,display_name) VALUES('grid-adversary-one','$argon2id$test','One'),('grid-adversary-two','$argon2id$test','Two')`,
	} {
		if _, err = native.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	var id productport.ID
	if err = native.QueryRow(ctx, `INSERT INTO products(product_code,name,price_minor,currency,stock_quantity,created_by,legacy_admin_projection) VALUES('grid-adversary','Grid',100,'CNY',19,1,'{"schema_version":1,"status":"service_period_enabled","enabled":true}') RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, `INSERT INTO product_imported_service_period_definitions(product_id,duration_days) VALUES($1,30)`, id); err != nil {
		t.Fatal(err)
	}
	wrapped, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := productstore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	audit, err := platformaudit.NewService(platformaudit.NewPostgreSQLStore())
	if err != nil {
		t.Fatal(err)
	}
	events, err := productstore.NewTransactionalEventAppender(audit, platformoutbox.NewPostgreSQL())
	if err != nil {
		t.Fatal(err)
	}
	return memberGridAdversarialFixture{ctx: ctx, native: native, productID: id, workspace: productapp.NewMemberGridWorkspaceService(uow, repository, archiveMemberGridDirectory{}, events), periods: productapp.NewServicePeriodService(uow, repository, events)}
}
