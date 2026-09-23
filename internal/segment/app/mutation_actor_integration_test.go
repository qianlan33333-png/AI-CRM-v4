package app

import (
	"context"
	"errors"
	"testing"
	"time"

	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
	segmentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/store"
)

func TestPostgreSQLMachineMutationActorPersistsFactsReceiptsAndAuditAtomically(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	native, cleanup := scheduleRuntimeDatabase(t, ctx)
	defer cleanup()
	wrapped, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := segmentstore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(uow, repository)
	service.now = func() time.Time { return time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC) }

	machine, err := segmentport.MachineMutationActor("open-audience")
	if err != nil {
		t.Fatal(err)
	}
	group, err := service.CreateGroup(ctx, GroupCommand{
		Name: "Open audience", SortOrder: 3, MutationActor: machine,
		IdempotencyKey: "segment-machine-create-group-001",
	})
	if err != nil {
		t.Fatalf("create machine group: %v", err)
	}
	if group.CreatedBy != 0 || group.CreatedActorKind != "machine" || group.CreatedActorRef != "machine:open-audience" || group.UpdatedBy != 0 || group.UpdatedActorKind != "machine" || group.UpdatedActorRef != "machine:open-audience" {
		t.Fatalf("machine group attribution=%+v", group)
	}

	created, err := service.CreatePackage(ctx, PackageCreateCommand{
		Name: "Open active contacts", TemplateKey: "active_contacts", GroupID: &group.ID, MutationActor: machine,
		IdempotencyKey: "segment-machine-create-package-001",
	})
	if err != nil {
		t.Fatalf("create machine package: %v", err)
	}
	if created.CreatedBy != 0 || created.CreatedActorKind != "machine" || created.CreatedActorRef != "machine:open-audience" || created.UpdatedBy != 0 || created.UpdatedActorKind != "machine" || created.UpdatedActorRef != "machine:open-audience" {
		t.Fatalf("machine package attribution=%+v", created)
	}
	replayed, err := service.CreatePackage(ctx, PackageCreateCommand{
		Name: "Open active contacts", TemplateKey: "active_contacts", GroupID: &group.ID, MutationActor: machine,
		IdempotencyKey: "segment-machine-create-package-001",
	})
	if err != nil || replayed.ID != created.ID {
		t.Fatalf("machine replay package=%+v err=%v", replayed, err)
	}

	var configurationActorID *int64
	var configurationKind, configurationRef string
	if err = native.QueryRow(ctx, `SELECT created_by,created_actor_kind,created_actor_ref FROM segment_audience_configuration_versions WHERE package_id=$1`, created.ID).Scan(&configurationActorID, &configurationKind, &configurationRef); err != nil {
		t.Fatal(err)
	}
	if configurationActorID != nil || configurationKind != "machine" || configurationRef != "machine:open-audience" {
		t.Fatalf("machine configuration actor id=%v kind=%q ref=%q", configurationActorID, configurationKind, configurationRef)
	}
	var receipts, receiptStaff, audits, auditStaff int64
	if err = native.QueryRow(ctx, `SELECT count(*),count(CASE WHEN actor_kind='machine' AND actor_ref='machine:open-audience' THEN 1 END) FROM segment_audience_operation_receipts`).Scan(&receipts, &receiptStaff); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*),count(actor_id) FROM segment_audience_audit_events WHERE actor_kind='machine' AND actor_ref='machine:open-audience'`).Scan(&audits, &auditStaff); err != nil {
		t.Fatal(err)
	}
	if receipts != 2 || receiptStaff != 2 || audits != 3 || auditStaff != 0 {
		t.Fatalf("machine receipt/audit counts receipts=%d machine=%d audits=%d nonnull_actor_ids=%d", receipts, receiptStaff, audits, auditStaff)
	}

	var beforeFailedReceipt int64
	if err = native.QueryRow(ctx, `SELECT count(*) FROM segment_audience_operation_receipts WHERE actor_scope='machine:open-audience' AND operation='create_package'`).Scan(&beforeFailedReceipt); err != nil {
		t.Fatal(err)
	}
	missingGroup := int64(999999)
	_, err = service.CreatePackage(ctx, PackageCreateCommand{
		Name: "broken machine package", TemplateKey: "active_contacts", GroupID: &missingGroup, MutationActor: machine,
		IdempotencyKey: "segment-machine-rollback-package-001",
	})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("machine rollback error=%v", err)
	}
	var afterFailedReceipt int64
	if err = native.QueryRow(ctx, `SELECT count(*) FROM segment_audience_operation_receipts WHERE actor_scope='machine:open-audience' AND operation='create_package'`).Scan(&afterFailedReceipt); err != nil {
		t.Fatal(err)
	}
	if afterFailedReceipt != beforeFailedReceipt {
		t.Fatalf("failed machine mutation retained receipt before=%d after=%d", beforeFailedReceipt, afterFailedReceipt)
	}

	human, err := service.CreateGroup(ctx, GroupCommand{Name: "Human audience", SortOrder: 4, Actor: 7, IdempotencyKey: "segment-human-create-group-001"})
	if err != nil || human.CreatedBy != 7 || human.CreatedActorKind != "admin" || human.CreatedActorRef != "admin:7" {
		t.Fatalf("human compatibility group=%+v err=%v", human, err)
	}
	_, err = service.CreateGroup(ctx, GroupCommand{Name: "invalid combined actor", SortOrder: 5, Actor: 7, MutationActor: machine, IdempotencyKey: "segment-machine-invalid-actor-001"})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("machine/admin mixing error=%v", err)
	}
	_, err = service.CreateGroup(ctx, GroupCommand{Name: "invalid subject", SortOrder: 6, MutationActor: segmentport.MutationActor{Kind: segmentport.MutationActorMachine, Reference: "machine:has space"}, IdempotencyKey: "segment-machine-invalid-subject-001"})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid machine subject error=%v", err)
	}
}
