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

type archiveMemberGridDirectory struct{}

func (archiveMemberGridDirectory) ActiveMemberGridStaff(context.Context, int64) (bool, error) {
	return true, nil
}
func (archiveMemberGridDirectory) MemberGridStaffByWeComUserID(context.Context, string) (productport.MemberGridStaff, bool, error) {
	return productport.MemberGridStaff{}, false, nil
}
func (archiveMemberGridDirectory) MemberGridStaffByID(_ context.Context, id int64) (productport.MemberGridStaff, bool, error) {
	return productport.MemberGridStaff{AdminUserID: id, Active: true}, true, nil
}
func (archiveMemberGridDirectory) ListActiveMemberGridStaff(context.Context) ([]productport.MemberGridStaff, error) {
	return []productport.MemberGridStaff{}, nil
}

// TestLocalProductArchivePostgreSQLRetainsHistoryAndClosesNewEntryPoints
// exercises the owner Archive command against its PostgreSQL implementation.
// It keeps historical Product IDs readable while default discovery, current
// selection, checkout and public service-period presentation all close.
func TestLocalProductArchivePostgreSQLRetainsHistoryAndClosesNewEntryPoints(t *testing.T) {
	databaseURL, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured; skipping Product lifecycle PostgreSQL integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	adminConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.NewWithConfig(ctx, adminConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	if err = admin.Ping(ctx); err != nil {
		t.Fatalf("postgres readiness: %v", err)
	}
	var random [8]byte
	if _, err = rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	schema := "aicrm_product_archive_" + hex.EncodeToString(random[:])
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(context.Background(), "DROP SCHEMA "+identifier+" CASCADE")
	config := adminConfig.Copy()
	config.ConnConfig.RuntimeParams["search_path"] = schema
	native, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer native.Close()
	for _, name := range []string{"0001_platform.sql", "0003_access.sql", "0010_product.sql", "0079_service_period_member_grid.sql"} {
		migration, readErr := os.ReadFile(memberGridMigration(t, name))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, execErr := native.Exec(ctx, string(migration)); execErr != nil {
			t.Fatalf("apply %s: %v", name, execErr)
		}
	}
	if _, err = native.Exec(ctx, `CREATE TABLE outbox_events (
		id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
		aggregate_type TEXT NOT NULL, aggregate_id TEXT NOT NULL, event_type TEXT NOT NULL,
		event_version SMALLINT NOT NULL, idempotency_key TEXT NOT NULL UNIQUE,
		payload_json JSONB NOT NULL, occurred_at TIMESTAMPTZ NOT NULL, processed_at TIMESTAMPTZ
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, `CREATE TABLE product_archive_history (id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY, product_id BIGINT NOT NULL REFERENCES products(id) ON DELETE RESTRICT)`); err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, `CREATE TABLE product_imported_service_period_definitions(product_id BIGINT PRIMARY KEY REFERENCES products(id),duration_days INTEGER NOT NULL CHECK(duration_days>0))`); err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, `INSERT INTO admin_users(username,password_hash,display_name) VALUES('archive-admin','$argon2id$test','Archive Admin'),('archive-collaborator','$argon2id$test','Archive Collaborator'),('archive-pending','$argon2id$test','Archive Pending')`); err != nil {
		t.Fatal(err)
	}
	var ordinaryID, periodID int64
	ordinaryProjection := json.RawMessage(`{"schema_version":1,"status":"active","enabled":true}`)
	periodProjection := json.RawMessage(`{"schema_version":1,"status":"service_period_enabled","enabled":true}`)
	if err = native.QueryRow(ctx, `INSERT INTO products(product_code,name,price_minor,currency,stock_quantity,created_by,legacy_admin_projection) VALUES('archive-standard','Archive Standard',100,'CNY',1,1,$1) RETURNING id`, ordinaryProjection).Scan(&ordinaryID); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `INSERT INTO products(product_code,name,price_minor,currency,stock_quantity,created_by,legacy_admin_projection) VALUES('archive-period','Archive Period',200,'CNY',1,1,$1) RETURNING id`, periodProjection).Scan(&periodID); err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, `INSERT INTO product_imported_service_period_definitions(product_id,duration_days) VALUES($1,90)`, periodID); err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, `INSERT INTO product_archive_history(product_id) VALUES($1),($2)`, ordinaryID, periodID); err != nil {
		t.Fatal(err)
	}

	wrapped, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := productstore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	auditService, err := platformaudit.NewService(platformaudit.NewPostgreSQLStore())
	if err != nil {
		t.Fatal(err)
	}
	events, err := productstore.NewTransactionalEventAppender(auditService, platformoutbox.NewPostgreSQL())
	if err != nil {
		t.Fatal(err)
	}
	lifecycle := productapp.NewLocalProductLifecycleService(uow, repository, events)
	ordinary := productapp.NewService(uow, repository, events)
	periods := productapp.NewServicePeriodService(uow, repository, events)
	targets, err := productapp.NewTargetReader(ordinary, periods)
	if err != nil {
		t.Fatal(err)
	}
	workspace := productapp.NewMemberGridWorkspaceService(uow, repository, archiveMemberGridDirectory{}, events)
	if preArchivePeriod, readErr := periods.GetServicePeriodProduct(ctx, productport.ID(periodID)); readErr != nil || preArchivePeriod.Lifecycle != productport.ServicePeriodEnabled {
		t.Fatalf("pre-archive service-period read=%+v err=%v", preArchivePeriod, readErr)
	}
	gridActor := productport.MemberGridActor{AdminUserID: 1, IsAdmin: true, IsSuperAdmin: true}
	createView := productport.CreateMemberGridViewCommand{ProductID: productport.ID(periodID), Name: "Retained view", Config: json.RawMessage(`{"columns":["name"]}`), Actor: gridActor, IdempotencyKey: "product-grid-before-archive-view-0001"}
	view, err := workspace.CreateView(ctx, createView)
	if err != nil || view.Version != 1 {
		t.Fatalf("create pre-archive view=%+v err=%v", view, err)
	}
	if replay, replayErr := workspace.CreateView(ctx, createView); replayErr != nil || replay.ID != view.ID || replay.Version != view.Version {
		t.Fatalf("create view replay=%+v err=%v", replay, replayErr)
	}
	changedView := createView
	changedView.Name = "same key different payload"
	if _, conflictErr := workspace.CreateView(ctx, changedView); !errors.Is(conflictErr, productapp.ErrConflict) {
		t.Fatalf("create view same-key changed-payload error=%v", conflictErr)
	}
	collaborator, err := workspace.CreateCollaborator(ctx, productport.CreateMemberGridCollaboratorCommand{ProductID: productport.ID(periodID), AdminUserID: 2, Permission: "edit", Actor: gridActor, IdempotencyKey: "product-grid-before-archive-collaborator-0002"})
	if err != nil || collaborator.Version != 1 {
		t.Fatalf("create pre-archive collaborator=%+v err=%v", collaborator, err)
	}
	share, issued, err := workspace.SetShare(ctx, productport.SetMemberGridShareCommand{ProductID: productport.ID(periodID), Enabled: true, ExpectedVersion: 0, Actor: gridActor, IdempotencyKey: "product-grid-before-archive-share-0003"})
	if err != nil || !issued || !share.Enabled || share.Version != 1 {
		t.Fatalf("set pre-archive share=%+v issued=%v err=%v", share, issued, err)
	}

	archive := func(id productport.ID, key string) productport.LocalProduct {
		value, archiveErr := lifecycle.ArchiveLocalProduct(ctx, productport.ArchiveLocalProductCommand{ID: id, ExpectedVersion: 1, Actor: 1, IdempotencyKey: key})
		if archiveErr != nil || value.ID != id || value.Version != 2 || value.Enabled || value.Lifecycle != productport.LocalProductArchived {
			t.Fatalf("archive %d=%+v err=%v", id, value, archiveErr)
		}
		return value
	}
	archivedOrdinary := archive(productport.ID(ordinaryID), "product-archive-postgres-standard-0001")
	archivedPeriod, archivePeriodErr := periods.ArchiveServicePeriodProduct(ctx, productport.ArchiveServicePeriodProductCommand{ID: productport.ID(periodID), ExpectedVersion: 1, Actor: 1, IdempotencyKey: "product-archive-postgres-period-0002"})
	if archivePeriodErr != nil || archivedPeriod.ServiceProductID != productport.ID(periodID) || archivedPeriod.Version != 2 || !archivedPeriod.Archived || archivedPeriod.Enabled || archivedPeriod.Lifecycle != productport.ServicePeriodArchived {
		t.Fatalf("archive service-period=%+v err=%v", archivedPeriod, archivePeriodErr)
	}
	if replay, replayErr := lifecycle.ArchiveLocalProduct(ctx, productport.ArchiveLocalProductCommand{ID: archivedOrdinary.ID, ExpectedVersion: 1, Actor: 1, IdempotencyKey: "product-archive-postgres-standard-0001"}); replayErr != nil || replay.ID != archivedOrdinary.ID || replay.Version != archivedOrdinary.Version {
		t.Fatalf("archive replay=%+v err=%v", replay, replayErr)
	}
	if _, staleErr := periods.ArchiveServicePeriodProduct(ctx, productport.ArchiveServicePeriodProductCommand{ID: archivedPeriod.ServiceProductID, ExpectedVersion: 1, Actor: 1, IdempotencyKey: "product-archive-postgres-period-stale-0003"}); !errors.Is(staleErr, productapp.ErrConflict) {
		t.Fatalf("stale archive error=%v", staleErr)
	}

	if err = uow.Within(ctx, func(tx context.Context) error {
		rows, listErr := repository.List(tx, nil, 20)
		if listErr != nil || len(rows) != 0 {
			t.Fatalf("ordinary owner list=%+v err=%v", rows, listErr)
		}
		count, countErr := repository.Count(tx)
		if countErr != nil || count != 0 {
			t.Fatalf("ordinary owner count=%d err=%v", count, countErr)
		}
		periodRows, periodTotal, periodErr := repository.ListServicePeriodProducts(tx, 20, 0)
		if periodErr != nil || len(periodRows) != 0 || periodTotal != 0 {
			t.Fatalf("period owner list=%+v total=%d err=%v", periodRows, periodTotal, periodErr)
		}
		options, optionsErr := repository.ListProductOptions(tx, productport.ProductOptionQuery{ProductType: productport.ProductOptionAll, Limit: 20})
		if optionsErr != nil || options.Total != 0 || len(options.Items) != 0 {
			t.Fatalf("new selection options=%+v err=%v", options, optionsErr)
		}
		historical, historyErr := repository.ReadProductTargets(tx, []productport.ProductTargetReference{{ProductType: productport.ProductOptionStandard, ID: archivedOrdinary.ID}, {ProductType: productport.ProductOptionServicePeriod, ID: archivedPeriod.ServiceProductID}})
		if historyErr != nil || len(historical) != 2 || !historical[0].Found || !historical[1].Found {
			t.Fatalf("historical targets=%+v err=%v", historical, historyErr)
		}
		for _, input := range []struct {
			id   productport.ID
			kind productport.ExternalPushProductKind
		}{{archivedOrdinary.ID, productport.ExternalPushWeChatPay}, {archivedPeriod.ServiceProductID, productport.ExternalPushServicePeriod}} {
			if _, lockErr := repository.LockCommerceExternalPushConfiguration(tx, input.id, input.kind); lockErr == nil {
				t.Fatalf("archived product %d admitted a new external-push write", input.id)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, input := range []struct {
		id   productport.ID
		kind productport.ProductOptionType
	}{{archivedOrdinary.ID, productport.ProductOptionStandard}, {archivedPeriod.ServiceProductID, productport.ProductOptionServicePeriod}} {
		if _, readErr := targets.ReadProductTarget(ctx, input.kind, input.id); readErr == nil {
			t.Fatalf("archived product %d remained available to a new target", input.id)
		}
		if checkoutErr := uow.Within(ctx, func(tx context.Context) error {
			_, readErr := targets.ReadCheckoutProductWithin(tx, input.kind, input.id)
			return readErr
		}); !errors.Is(checkoutErr, productapp.ErrNotFound) {
			t.Fatalf("archived product %d checkout error=%v", input.id, checkoutErr)
		}
	}
	if _, public, publicErr := periods.ReadServicePeriodPublicPresentationByCode(ctx, "archive-period"); publicErr != nil || public {
		t.Fatalf("archived period public presentation available=%v err=%v", public, publicErr)
	}
	if views, readErr := workspace.ListViews(ctx, archivedPeriod.ServiceProductID); readErr != nil || len(views) != 1 || views[0].ID != view.ID {
		t.Fatalf("archived member-grid views=%+v err=%v", views, readErr)
	}
	if collaborators, readErr := workspace.ListCollaborators(ctx, archivedPeriod.ServiceProductID); readErr != nil || len(collaborators) != 1 || collaborators[0].ID != collaborator.ID {
		t.Fatalf("archived member-grid collaborators=%+v err=%v", collaborators, readErr)
	}
	if retainedShare, readErr := workspace.Share(ctx, archivedPeriod.ServiceProductID); readErr != nil || !retainedShare.Enabled || retainedShare.PublicID != share.PublicID || retainedShare.Version != share.Version {
		t.Fatalf("archived member-grid share=%+v err=%v", retainedShare, readErr)
	}
	if _, writeErr := workspace.CreateView(ctx, productport.CreateMemberGridViewCommand{ProductID: archivedPeriod.ServiceProductID, Name: "must not create", Config: json.RawMessage(`{"columns":[]}`), Actor: gridActor, IdempotencyKey: "product-grid-after-archive-view-create-0004"}); !errors.Is(writeErr, productapp.ErrNotFound) {
		t.Fatalf("archived member-grid create view error=%v", writeErr)
	}
	if _, writeErr := workspace.UpdateView(ctx, productport.UpdateMemberGridViewCommand{ProductID: archivedPeriod.ServiceProductID, ViewID: view.ID, ExpectedVersion: view.Version, Name: "must not update", Config: json.RawMessage(`{"columns":[]}`), Actor: gridActor, IdempotencyKey: "product-grid-after-archive-view-update-0005"}); !errors.Is(writeErr, productapp.ErrNotFound) {
		t.Fatalf("archived member-grid update view error=%v", writeErr)
	}
	if _, writeErr := workspace.DeleteView(ctx, productport.DeleteMemberGridViewCommand{ProductID: archivedPeriod.ServiceProductID, ViewID: view.ID, ExpectedVersion: view.Version, Actor: gridActor, IdempotencyKey: "product-grid-after-archive-view-delete-0006"}); !errors.Is(writeErr, productapp.ErrNotFound) {
		t.Fatalf("archived member-grid delete view error=%v", writeErr)
	}
	if _, writeErr := workspace.CreateCollaborator(ctx, productport.CreateMemberGridCollaboratorCommand{ProductID: archivedPeriod.ServiceProductID, AdminUserID: 3, Permission: "read", Actor: gridActor, IdempotencyKey: "product-grid-after-archive-collaborator-create-0007"}); !errors.Is(writeErr, productapp.ErrNotFound) {
		t.Fatalf("archived member-grid create collaborator error=%v", writeErr)
	}
	if _, writeErr := workspace.UpdateCollaborator(ctx, productport.UpdateMemberGridCollaboratorCommand{ProductID: archivedPeriod.ServiceProductID, CollaboratorID: collaborator.ID, ExpectedVersion: collaborator.Version, Permission: "read", Actor: gridActor, IdempotencyKey: "product-grid-after-archive-collaborator-update-0008"}); !errors.Is(writeErr, productapp.ErrNotFound) {
		t.Fatalf("archived member-grid update collaborator error=%v", writeErr)
	}
	if _, writeErr := workspace.DeleteCollaborator(ctx, productport.DeleteMemberGridCollaboratorCommand{ProductID: archivedPeriod.ServiceProductID, CollaboratorID: collaborator.ID, ExpectedVersion: collaborator.Version, Actor: gridActor, IdempotencyKey: "product-grid-after-archive-collaborator-delete-0009"}); !errors.Is(writeErr, productapp.ErrNotFound) {
		t.Fatalf("archived member-grid delete collaborator error=%v", writeErr)
	}
	if _, _, writeErr := workspace.SetShare(ctx, productport.SetMemberGridShareCommand{ProductID: archivedPeriod.ServiceProductID, Enabled: true, ExpectedVersion: share.Version, Actor: gridActor, IdempotencyKey: "product-grid-after-archive-share-set-0010"}); !errors.Is(writeErr, productapp.ErrNotFound) {
		t.Fatalf("archived member-grid set share error=%v", writeErr)
	}

	for table, want := range map[string]int64{"product_operation_receipts": 5, "audit_events": 5, "outbox_events": 5, "product_archive_history": 2, "product_service_period_member_views": 1, "product_service_period_member_collaborators": 1, "product_service_period_member_shares": 1} {
		var got int64
		if err = native.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("%s count=%d want=%d", table, got, want)
		}
	}
	var ordinaryArchiveReceipts, periodUpdateReceipts, archiveAudit, archiveOutbox int64
	if err = native.QueryRow(ctx, `SELECT count(*) FROM product_operation_receipts WHERE operation='archive' AND state='completed'`).Scan(&ordinaryArchiveReceipts); err != nil || ordinaryArchiveReceipts != 1 {
		t.Fatalf("ordinary archive receipts=%d err=%v", ordinaryArchiveReceipts, err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM product_operation_receipts WHERE operation='update' AND state='completed'`).Scan(&periodUpdateReceipts); err != nil || periodUpdateReceipts != 1 {
		t.Fatalf("legacy service-period archive receipt scope=%d err=%v", periodUpdateReceipts, err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE payload->>'action'='archive'`).Scan(&archiveAudit); err != nil || archiveAudit != 2 {
		t.Fatalf("archive audit events=%d err=%v", archiveAudit, err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE payload_json->>'action'='archive'`).Scan(&archiveOutbox); err != nil || archiveOutbox != 2 {
		t.Fatalf("archive outbox events=%d err=%v", archiveOutbox, err)
	}
	var ordinaryVersion, periodVersion int64
	if err = native.QueryRow(ctx, `SELECT version FROM products WHERE id=$1`, ordinaryID).Scan(&ordinaryVersion); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT version FROM products WHERE id=$1`, periodID).Scan(&periodVersion); err != nil {
		t.Fatal(err)
	}
	if ordinaryVersion != archivedOrdinary.Version || periodVersion != archivedPeriod.Version {
		t.Fatalf("archive/stale changed versions ordinary=%d period=%d", ordinaryVersion, periodVersion)
	}
}
