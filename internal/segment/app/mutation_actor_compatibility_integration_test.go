package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
	segmentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/store"
)

func TestPostgreSQLLegacyHumanReceiptReplaysAfterMutationActorMigration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	native, cleanup := scheduleRuntimeDatabaseWithMutationActor(t, ctx, false)
	defer cleanup()

	now := time.Date(2026, 9, 6, 14, 0, 0, 0, time.UTC)
	command := GroupCommand{Name: "legacy receipt", SortOrder: 1, Actor: 7, IdempotencyKey: "legacy-human-group-replay-001"}
	legacyCommand := struct {
		ID, ExpectedVersion int64
		Name                string
		SortOrder           int
		Actor               int64
		IdempotencyKey      string
	}{Name: command.Name, SortOrder: command.SortOrder, Actor: command.Actor, IdempotencyKey: command.IdempotencyKey}
	legacyPayload, err := json.Marshal(struct {
		Operation string `json:"operation"`
		Command   any    `json:"command"`
	}{Operation: "create_group", Command: legacyCommand})
	if err != nil {
		t.Fatal(err)
	}
	actor, err := segmentport.AdminMutationActor(command.Actor)
	if err != nil {
		t.Fatal(err)
	}
	if got := mutationPayload("create_group", actor, command); !bytes.Equal(got, legacyPayload) {
		t.Fatalf("administrator payload changed across 0097\n got: %s\nwant: %s", got, legacyPayload)
	}

	var groupID int64
	if err = native.QueryRow(ctx, `INSERT INTO segment_audience_groups(name,sort_order,created_by,updated_by,created_at,updated_at)
		VALUES($1,$2,$3,$3,$4,$4) RETURNING id`, command.Name, command.SortOrder, command.Actor, now).Scan(&groupID); err != nil {
		t.Fatal(err)
	}
	legacyResult := []byte(`{"id":` + jsonNumber(groupID) + `,"name":"legacy receipt","sort_order":1,"version":1,"created_by":7,"updated_by":7}`)
	keyDigest := sha256.Sum256([]byte(command.IdempotencyKey))
	payloadDigest := sha256.Sum256(legacyPayload)
	if _, err = native.Exec(ctx, `INSERT INTO segment_audience_operation_receipts(operation,actor_scope,key_digest,payload_digest,state,result_snapshot,created_at,completed_at)
		VALUES('create_group','admin:7',$1,$2,'completed',$3::jsonb,$4,$4)`, keyDigest[:], payloadDigest[:], legacyResult, now); err != nil {
		t.Fatal(err)
	}

	applySegmentRuntimeMigration(t, native, "0097_segment_audience_mutation_actor.sql")
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
	service.now = func() time.Time { return now.Add(time.Minute) }

	replayed, err := service.CreateGroup(ctx, command)
	if err != nil || replayed.ID != groupID || replayed.Name != command.Name {
		t.Fatalf("legacy human replay group=%+v err=%v", replayed, err)
	}
	var groups, receipts, audits, outbox int64
	if err = native.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM segment_audience_groups),
		(SELECT count(*) FROM segment_audience_operation_receipts),
		(SELECT count(*) FROM segment_audience_audit_events),
		(SELECT count(*) FROM segment_audience_outbox)`).Scan(&groups, &receipts, &audits, &outbox); err != nil {
		t.Fatal(err)
	}
	if groups != 1 || receipts != 1 || audits != 0 || outbox != 0 {
		t.Fatalf("legacy replay changed persisted facts groups=%d receipts=%d audits=%d outbox=%d", groups, receipts, audits, outbox)
	}
	var kind, reference string
	if err = native.QueryRow(ctx, `SELECT actor_kind,actor_ref FROM segment_audience_operation_receipts WHERE id=1`).Scan(&kind, &reference); err != nil {
		t.Fatal(err)
	}
	if kind != "admin" || reference != "admin:7" {
		t.Fatalf("migration did not canonically retain human receipt kind=%q ref=%q", kind, reference)
	}
}

func TestPostgreSQLMachineActorsScopeSharedKeysAndKeepCAS(t *testing.T) {
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
	service.now = func() time.Time { return time.Date(2026, 9, 6, 14, 5, 0, 0, time.UTC) }
	machineA, err := segmentport.MachineMutationActor("open-audience-a")
	if err != nil {
		t.Fatal(err)
	}
	machineB, err := segmentport.MachineMutationActor("open-audience-b")
	if err != nil {
		t.Fatal(err)
	}

	type createdGroup struct {
		valueID int64
		name    string
		actor   segmentport.MutationActor
		err     error
	}
	results := make(chan createdGroup, 2)
	start := make(chan struct{})
	var workers sync.WaitGroup
	for _, item := range []struct {
		name  string
		actor segmentport.MutationActor
	}{{"machine A group", machineA}, {"machine B group", machineB}} {
		workers.Add(1)
		go func(item struct {
			name  string
			actor segmentport.MutationActor
		}) {
			defer workers.Done()
			<-start
			group, createErr := service.CreateGroup(ctx, GroupCommand{Name: item.name, SortOrder: 1, MutationActor: item.actor, IdempotencyKey: "shared-machine-group-key-001"})
			results <- createdGroup{valueID: group.ID, name: item.name, actor: item.actor, err: createErr}
		}(item)
	}
	close(start)
	workers.Wait()
	close(results)
	groups := make(map[string]createdGroup, 2)
	for result := range results {
		if result.err != nil || result.valueID < 1 {
			t.Fatalf("parallel machine group actor=%s id=%d err=%v", result.actor.Reference, result.valueID, result.err)
		}
		groups[result.actor.Reference] = result
	}
	if len(groups) != 2 || groups[machineA.Reference].valueID == groups[machineB.Reference].valueID {
		t.Fatalf("shared key collapsed machine groups: %+v", groups)
	}

	type createdPackage struct {
		id    int64
		code  string
		actor segmentport.MutationActor
		err   error
	}
	packageResults := make(chan createdPackage, 2)
	start = make(chan struct{})
	for _, item := range []struct {
		name  string
		actor segmentport.MutationActor
	}{{"machine A package", machineA}, {"machine B package", machineB}} {
		workers.Add(1)
		go func(item struct {
			name  string
			actor segmentport.MutationActor
		}) {
			defer workers.Done()
			<-start
			group := groups[item.actor.Reference]
			pkg, createErr := service.CreatePackage(ctx, PackageCreateCommand{Name: item.name, TemplateKey: "active_contacts", GroupID: &group.valueID, MutationActor: item.actor, IdempotencyKey: "shared-machine-package-key-001"})
			packageResults <- createdPackage{id: pkg.ID, code: pkg.Code, actor: item.actor, err: createErr}
		}(item)
	}
	close(start)
	workers.Wait()
	close(packageResults)
	packages := make(map[string]createdPackage, 2)
	for result := range packageResults {
		if result.err != nil || result.id < 1 || result.code == "" {
			t.Fatalf("parallel machine package actor=%s package=%+v", result.actor.Reference, result)
		}
		packages[result.actor.Reference] = result
	}
	first, second := packages[machineA.Reference], packages[machineB.Reference]
	if len(packages) != 2 || first.id == second.id || first.code == second.code {
		t.Fatalf("shared key collapsed machine packages first=%+v second=%+v", first, second)
	}
	firstGroupID := groups[machineA.Reference].valueID

	replayed, err := service.CreatePackage(ctx, PackageCreateCommand{Name: "machine A package", TemplateKey: "active_contacts", GroupID: &firstGroupID, MutationActor: machineA, IdempotencyKey: "shared-machine-package-key-001"})
	if err != nil || replayed.ID != first.id {
		t.Fatalf("same machine replay package=%+v err=%v", replayed, err)
	}
	_, err = service.CreatePackage(ctx, PackageCreateCommand{Name: "machine A changed package", TemplateKey: "active_contacts", GroupID: &firstGroupID, MutationActor: machineA, IdempotencyKey: "shared-machine-package-key-001"})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("same machine changed replay error=%v", err)
	}
	_, err = service.UpdateGroup(ctx, GroupCommand{ID: groups[machineA.Reference].valueID, ExpectedVersion: 2, Name: "stale CAS", SortOrder: 2, MutationActor: machineA, IdempotencyKey: "machine-group-cas-conflict-001"})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("machine stale group CAS error=%v", err)
	}
}

func jsonNumber(value int64) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
