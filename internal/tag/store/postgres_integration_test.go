package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	tagapp "github.com/qianlan33333-png/AI-CRM-v3/internal/tag/app"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/tag/domain"
	tagport "github.com/qianlan33333-png/AI-CRM-v3/internal/tag/port"
)

func TestPostgreSQLCatalogReorderArchiveReplayAndReferenceProtection(t *testing.T) {
	native, cleanup := tagIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	for _, table := range []string{"tag_groups", "tag_catalog_tags", "tag_references", "tag_operation_receipts", "tag_audit_events", "tag_outbox", "tag_sync_receipts", "tag_provider_observations", "tag_provider_group_bindings", "tag_provider_tag_bindings"} {
		var owned bool
		if err := native.QueryRow(ctx, `SELECT tableowner=current_user FROM pg_tables WHERE schemaname=current_schema() AND tablename=$1`, table).Scan(&owned); err != nil || !owned {
			t.Fatalf("table %s ownership = %t, %v", table, owned, err)
		}
	}
	for _, index := range []string{"tag_groups_active_sort_unique", "tag_catalog_tags_active_sort_unique", "tag_sync_receipts_single_active"} {
		var exists bool
		if err := native.QueryRow(ctx, `SELECT to_regclass(current_schema() || '.' || $1) IS NOT NULL`, index).Scan(&exists); err != nil || !exists {
			t.Fatalf("required index %s exists=%t err=%v", index, exists, err)
		}
	}
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
	service := tagapp.NewService(uow, repository, repository, repository, repository)
	groupOne, tagOne, err := service.CreateGroup(ctx, domain.Command{Actor: 7, IdempotencyKey: "pg-group-one-key-0001", GroupName: "Lifecycle", FirstTagName: "Warm"})
	if err != nil {
		t.Fatal(err)
	}
	groupTwo, groupTwoTag, err := service.CreateGroup(ctx, domain.Command{Actor: 7, IdempotencyKey: "pg-group-two-key-0001", GroupName: "Source", FirstTagName: "Organic"})
	if err != nil {
		t.Fatal(err)
	}
	tagTwo, err := service.CreateTag(ctx, domain.Command{Actor: 7, IdempotencyKey: "pg-tag-two-key-00001", GroupID: groupOne.ID, GroupName: groupOne.Name, TagName: "Hot"})
	if err != nil {
		t.Fatal(err)
	}
	groups, err := service.ReorderGroups(ctx, domain.Command{Actor: 7, IdempotencyKey: "pg-group-swap-key-01", IDs: []int64{groupTwo.ID, groupOne.ID}})
	if err != nil || groups[0].ID != groupTwo.ID || groups[1].ID != groupOne.ID {
		t.Fatalf("group swap = %#v, %v", groups, err)
	}
	tags, err := service.ReorderTags(ctx, domain.Command{Actor: 7, IdempotencyKey: "pg-tag-swap-key-0001", IDs: []int64{groupTwoTag.ID, tagTwo.ID}})
	if err == nil { // full catalog includes groupTwo's first tag; stale subset must fail closed.
		t.Fatalf("partial tag reorder unexpectedly succeeded: %#v", tags)
	}
	if _, err = service.ReorderTags(ctx, domain.Command{Actor: 7, IdempotencyKey: "pg-tag-cross-group-key", IDs: []int64{tagTwo.ID, groupTwoTag.ID, tagOne.ID}}); err == nil {
		t.Fatal("cross-group tag reorder unexpectedly succeeded")
	}
	tags, err = service.ReorderTags(ctx, domain.Command{Actor: 7, IdempotencyKey: "pg-tag-swap-all-key", IDs: []int64{groupTwoTag.ID, tagTwo.ID, tagOne.ID}})
	if err != nil || len(tags) != 3 || tags[0].ID != groupTwoTag.ID || tags[1].ID != tagTwo.ID || tags[2].ID != tagOne.ID {
		t.Fatalf("tag group-preserving swap = %#v, %v", tags, err)
	}
	archive := domain.Command{Actor: 7, TagID: tagOne.ID, IdempotencyKey: "pg-tag-archive-replay"}
	first, err := service.ArchiveTag(ctx, archive)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.ArchiveTag(ctx, archive)
	if err != nil || second != first {
		t.Fatalf("archive replay = %#v, %v; want %#v", second, err, first)
	}
	if _, err = service.GetTag(ctx, tagOne.ID); !errors.Is(err, tagapp.ErrNotFound) {
		t.Fatalf("archived public GetTag = %v", err)
	}
	if _, err = native.Exec(ctx, `INSERT INTO tag_references(resource_kind,resource_id,reference_digest,owner) VALUES('tag',$1,'sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','test')`, tagTwo.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = service.ArchiveGroup(ctx, domain.Command{Actor: 7, GroupID: groupOne.ID, IdempotencyKey: "pg-group-reference-key"}); !errors.Is(err, tagapp.ErrReferenced) {
		t.Fatalf("group archive with child tag ref = %v", err)
	}
	var effectID int64
	if err = native.QueryRow(ctx, `INSERT INTO external_effects(owner,kind,source_ref_digest,target_ref_digest,payload_digest,policy_version_hash,envelope_fingerprint,state) VALUES('outbound','wecom_tag_catalog',$1,$2,$3,$4,$5,'executed') RETURNING id`, Digest("source"), Digest("target"), Digest("payload"), Digest("policy"), Digest("fingerprint")).Scan(&effectID); err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, `INSERT INTO external_effect_generations(effect_id,generation) VALUES($1,1)`, effectID); err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, `INSERT INTO tag_sync_receipts(actor_admin_user_id,idempotency_key_digest,trace_id,sync_kind,state,event_id,queue_job_id,effect_id,effect_ref,effect_state,accept_receipt_id,queue_receipt_id,accepted_at) VALUES(7,$1,'','manual','queued',1,1,$2,$3,'queued','accept','queue',clock_timestamp())`, keyDigest("provider-observation-sync"), effectID, "eer_"+strconv.FormatInt(effectID, 10)); err != nil {
		t.Fatal(err)
	}
	observation := tagport.SyncCompletion{EffectID: effectID, Generation: 1, State: tagport.SyncExecuted, Snapshot: []byte(`{"groups":[{"id":"provider-group","name":"Provider Group","order":0,"tags":[{"id":"provider-tag","name":"Provider Tag","order":0}]}]}`)}
	observation.ArtifactDigest = providerArtifactDigest(observation.Snapshot)
	if err = uow.Within(ctx, func(txCtx context.Context) error { return repository.CompleteProviderSync(txCtx, observation) }); err != nil {
		t.Fatal(err)
	}
	status, err := func() (tagport.SyncStatus, error) {
		var value tagport.SyncStatus
		err := uow.Within(ctx, func(txCtx context.Context) error {
			var readErr error
			value, readErr = repository.LatestSync(txCtx)
			return readErr
		})
		return value, err
	}()
	if err != nil || status.State != tagport.SyncExecuted || status.Active || status.GroupCount != 1 || status.TagCount != 1 {
		t.Fatalf("completed sync status = %#v, %v", status, err)
	}
	groupsAfter, tagsAfter, err := func() ([]domain.Group, []domain.Tag, error) {
		var groups []domain.Group
		var tags []domain.Tag
		err := uow.Within(ctx, func(txCtx context.Context) error {
			var readErr error
			groups, readErr = repository.ListGroups(txCtx)
			if readErr == nil {
				tags, readErr = repository.ListTags(txCtx)
			}
			return readErr
		})
		return groups, tags, err
	}()
	if err != nil || len(groupsAfter) != 3 || groupsAfter[0].Name != "Provider Group" || len(tagsAfter) != 3 || tagsAfter[0].Name != "Provider Tag" {
		t.Fatalf("projected catalog groups=%#v tags=%#v err=%v", groupsAfter, tagsAfter, err)
	}
	if err = uow.Within(ctx, func(txCtx context.Context) error { return repository.CompleteProviderSync(txCtx, observation) }); err != nil {
		t.Fatalf("same observation replay=%v", err)
	}
	driftObservation := observation
	driftObservation.ArtifactDigest = providerArtifactDigest([]byte(`{"groups":[{"id":"g","name":"g","order":0,"tags":[]}]}`))
	driftObservation.Snapshot = []byte(`{"groups":[{"id":"g","name":"g","order":0,"tags":[]}]}`)
	if err = uow.Within(ctx, func(txCtx context.Context) error { return repository.CompleteProviderSync(txCtx, driftObservation) }); !errors.Is(err, ErrConflict) {
		t.Fatalf("observation drift=%v", err)
	}
	if _, err = native.Exec(ctx, `UPDATE tag_provider_observations SET artifact_digest=artifact_digest`); err == nil {
		t.Fatal("observation update unexpectedly succeeded")
	}
	if _, err = native.Exec(ctx, `DELETE FROM tag_provider_observations`); err == nil {
		t.Fatal("observation delete unexpectedly succeeded")
	}
	firstCommand := tagport.SyncCommand{Actor: 7, IdempotencyKey: "single-flight-one", Kind: tagport.SyncManual}
	if err = uow.Within(ctx, func(txCtx context.Context) error {
		_, reserveErr := repository.ReserveSync(txCtx, firstCommand)
		return reserveErr
	}); err != nil {
		t.Fatal(err)
	}
	secondCommand := tagport.SyncCommand{Actor: 8, IdempotencyKey: "single-flight-two", Kind: tagport.SyncManual}
	if err = uow.Within(ctx, func(txCtx context.Context) error {
		_, reserveErr := repository.ReserveSync(txCtx, secondCommand)
		return reserveErr
	}); !errors.Is(err, tagport.ErrSyncInProgress) {
		t.Fatalf("second active sync = %v, want ErrSyncInProgress", err)
	}
}

func Test0019BackfillsLatestValidatedSnapshotIntoEmptyCatalog(t *testing.T) {
	native, cleanup := tagIntegrationPoolWithProjection(t, false)
	defer cleanup()
	ctx := context.Background()
	digest := "sha256:" + strings.Repeat("a", 64)
	if _, err := native.Exec(ctx, `INSERT INTO external_effects(owner,kind,source_ref_digest,target_ref_digest,payload_digest,policy_version_hash,envelope_fingerprint,state) VALUES('outbound','wecom_tag_catalog',$1,$1,$1,$1,$1,'executed')`, digest); err != nil {
		t.Fatal(err)
	}
	if _, err := native.Exec(ctx, `INSERT INTO external_effect_generations(effect_id,generation) VALUES(1,1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := native.Exec(ctx, `INSERT INTO tag_sync_receipts(actor_admin_user_id,idempotency_key_digest,sync_kind,state,effect_id,effect_ref,effect_state) VALUES(7,decode(repeat('aa',32),'hex'),'manual','queued',1,'eer_1','queued')`); err != nil {
		t.Fatal(err)
	}
	if _, err := native.Exec(ctx, `INSERT INTO tag_provider_observations(effect_id,generation,artifact_digest,snapshot) VALUES(1,1,$1,'{"groups":[{"id":"g1","name":"Group 1","order":0,"tags":[{"id":"t1","name":"Tag 1","order":0},{"id":"t2","name":"Tag 2","order":1}]}]}'::jsonb)`, digest); err != nil {
		t.Fatal(err)
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate integration test")
	}
	base := filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations")
	projectionSQL, err := os.ReadFile(filepath.Join(base, "0019_tag_catalog_sync_projection.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, string(projectionSQL)); err != nil {
		t.Fatal(err)
	}
	applyTagCatalogMutationMigrations(t, native, base)
	var state, eventType string
	var groups, tags, groupBindings, tagBindings int
	if err = native.QueryRow(ctx, `SELECT state FROM tag_sync_receipts WHERE id=1`).Scan(&state); err != nil {
		t.Fatal(err)
	}
	for query, target := range map[string]*int{
		`SELECT count(*) FROM tag_groups WHERE archived_at IS NULL`:       &groups,
		`SELECT count(*) FROM tag_catalog_tags WHERE archived_at IS NULL`: &tags,
		`SELECT count(*) FROM tag_provider_group_bindings`:                &groupBindings,
		`SELECT count(*) FROM tag_provider_tag_bindings`:                  &tagBindings,
	} {
		if err = native.QueryRow(ctx, query).Scan(target); err != nil {
			t.Fatal(err)
		}
	}
	if err = native.QueryRow(ctx, `SELECT event_type FROM tag_audit_events ORDER BY id DESC LIMIT 1`).Scan(&eventType); err != nil {
		t.Fatal(err)
	}
	if state != "executed" || groups != 1 || tags != 2 || groupBindings != 1 || tagBindings != 2 || eventType != "tag.catalog_sync_backfilled" {
		t.Fatalf("backfill state=%s groups=%d tags=%d group_bindings=%d tag_bindings=%d event=%s", state, groups, tags, groupBindings, tagBindings, eventType)
	}
}

func TestPostgreSQLCatalogArchiveOutcomeRemainsVisibleAfterLocalArchive(t *testing.T) {
	native, cleanup := tagIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
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
	service := tagapp.NewService(uow, repository, repository, repository, repository)
	_, tag, err := service.CreateGroup(ctx, domain.Command{Actor: 7, IdempotencyKey: "archive-visible-group-key", GroupName: "Lifecycle", FirstTagName: "Warm"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, `INSERT INTO tag_provider_tag_bindings(provider_tag_id,tag_id) VALUES('provider-tag-visible',$1)`, tag.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = service.ArchiveTag(ctx, domain.Command{Actor: 7, TagID: tag.ID, IdempotencyKey: "archive-visible-tag-key-01"}); err != nil {
		t.Fatal(err)
	}
	if _, err = service.GetTag(ctx, tag.ID); !errors.Is(err, tagapp.ErrNotFound) {
		t.Fatalf("archived tag still in active catalog: %v", err)
	}
	var intent tagport.CatalogMutationIntent
	if err = uow.Within(ctx, func(tx context.Context) error {
		var reserveErr error
		intent, reserveErr = repository.ReserveCatalogMutation(tx, tagport.CatalogMutationPlan{Operation: tagport.CatalogTagArchive, Actor: 7, TagID: tag.ID, IdempotencyKey: "archive-visible-tag-key-01"})
		if reserveErr != nil {
			return reserveErr
		}
		return repository.AcceptCatalogMutation(tx, intent.ID, tagport.CatalogMutationEffectReceipt{EffectID: intent.ID, QueueJobID: intent.ID, EffectRef: "eer_1", EffectState: "queued", AcceptReceiptID: "eerop_1", QueueReceiptID: "eerop_1"})
	}); err != nil {
		t.Fatal(err)
	}
	operations, err := service.ArchiveOperations(ctx)
	if err != nil || len(operations) != 1 || operations[0].Operation != tagport.CatalogTagArchive || operations[0].LocalID != tag.ID || operations[0].State != "queued" {
		t.Fatalf("queued archive operations = %#v, %v", operations, err)
	}
	if err = uow.Within(ctx, func(tx context.Context) error {
		return repository.CompleteCatalogMutation(tx, tagport.CatalogMutationCompletion{EffectRef: "eer_1", State: "outcome_unknown", ResultDigest: "sha256:" + strings.Repeat("c", 64), Attempt: 1, Generation: 1, Fence: 1, CompletedAt: time.Now().UTC()})
	}); err != nil {
		t.Fatal(err)
	}
	operations, err = service.ArchiveOperations(ctx)
	if err != nil || len(operations) != 1 || operations[0].LocalID != tag.ID || operations[0].State != "outcome_unknown" {
		t.Fatalf("unknown archive operations = %#v, %v", operations, err)
	}
}

func TestPostgreSQLCatalogMutationReceiptBindsOnlyConfirmedProviderCreate(t *testing.T) {
	native, cleanup := tagIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
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
	service := tagapp.NewService(uow, repository, repository, repository, repository)

	unknownGroup, unknownTag, err := service.CreateGroup(ctx, domain.Command{Actor: 7, IdempotencyKey: "mutation-unknown-create", GroupName: "Unknown", FirstTagName: "Pending"})
	if err != nil {
		t.Fatal(err)
	}
	unknown := reserveCatalogMutation(t, ctx, uow, repository, tagport.CatalogMutationPlan{Operation: tagport.CatalogGroupCreate, Actor: 7, IdempotencyKey: "mutation-unknown-create", GroupID: unknownGroup.ID, TagID: unknownTag.ID, GroupName: unknownGroup.Name, TagName: unknownTag.Name})
	if err = uow.Within(ctx, func(tx context.Context) error {
		return repository.CompleteCatalogMutation(tx, tagport.CatalogMutationCompletion{EffectRef: unknown.EffectRef, State: "outcome_unknown", ResultDigest: "sha256:" + strings.Repeat("a", 64), Attempt: 1, Generation: 1, Fence: 1, CompletedAt: time.Now().UTC()})
	}); err != nil {
		t.Fatal(err)
	}
	var unknownBindings int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM tag_provider_group_bindings WHERE group_id=$1`, unknownGroup.ID).Scan(&unknownBindings); err != nil || unknownBindings != 0 {
		t.Fatalf("unknown create bindings=%d err=%v", unknownBindings, err)
	}

	confirmedGroup, confirmedTag, err := service.CreateGroup(ctx, domain.Command{Actor: 7, IdempotencyKey: "mutation-confirmed-create", GroupName: "Confirmed", FirstTagName: "Bound"})
	if err != nil {
		t.Fatal(err)
	}
	confirmed := reserveCatalogMutation(t, ctx, uow, repository, tagport.CatalogMutationPlan{Operation: tagport.CatalogGroupCreate, Actor: 7, IdempotencyKey: "mutation-confirmed-create", GroupID: confirmedGroup.ID, TagID: confirmedTag.ID, GroupName: confirmedGroup.Name, TagName: confirmedTag.Name})
	readbackAt := time.Date(2026, 9, 8, 9, 0, 0, 0, time.UTC)
	if err = uow.Within(ctx, func(tx context.Context) error {
		return repository.CompleteCatalogMutation(tx, tagport.CatalogMutationCompletion{EffectRef: confirmed.EffectRef, State: "executed", ResultDigest: "sha256:" + strings.Repeat("b", 64), ProviderGroupID: "provider-group-confirmed", ProviderTagID: "provider-tag-confirmed", ReadbackAt: &readbackAt, Attempt: 1, Generation: 1, Fence: 1, CompletedAt: readbackAt})
	}); err != nil {
		t.Fatal(err)
	}
	var boundGroup, boundTag int64
	if err = native.QueryRow(ctx, `SELECT group_id FROM tag_provider_group_bindings WHERE provider_group_id='provider-group-confirmed'`).Scan(&boundGroup); err != nil || boundGroup != confirmedGroup.ID {
		t.Fatalf("bound group=%d err=%v", boundGroup, err)
	}
	if err = native.QueryRow(ctx, `SELECT tag_id FROM tag_provider_tag_bindings WHERE provider_tag_id='provider-tag-confirmed'`).Scan(&boundTag); err != nil || boundTag != confirmedTag.ID {
		t.Fatalf("bound tag=%d err=%v", boundTag, err)
	}
	catalog, err := service.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, tag := range catalog.Tags {
		if tag.ID == confirmedTag.ID && tag.ProviderTagID != "provider-tag-confirmed" {
			t.Fatalf("confirmed Provider tag ID missing: %+v", tag)
		}
		if tag.ID == unknownTag.ID && tag.ProviderTagID != "" {
			t.Fatal("unknown create fabricated Provider tag ID")
		}
	}
	for _, group := range catalog.Groups {
		if group.ID == unknownGroup.ID && (group.ProviderMutationState != "outcome_unknown" || group.ProviderReadbackAt != nil) {
			t.Fatalf("unknown group status=%+v", group)
		}
		if group.ID == confirmedGroup.ID && (group.ProviderMutationState != "executed" || group.ProviderReadbackAt == nil || !group.ProviderReadbackAt.Equal(readbackAt)) {
			t.Fatalf("confirmed group status=%+v", group)
		}
	}
}

type serialCatalogMutationEnqueuer struct{}

func (serialCatalogMutationEnqueuer) EnqueueCatalogMutation(_ context.Context, intent tagport.CatalogMutationIntent, _ string) (tagport.CatalogMutationEffectReceipt, error) {
	value := strconv.FormatInt(intent.ID, 10)
	return tagport.CatalogMutationEffectReceipt{EffectID: intent.ID, QueueJobID: intent.ID, EffectRef: "eer_" + value, EffectState: "queued", AcceptReceiptID: "eerop_" + value, QueueReceiptID: "eerop_" + value}, nil
}

func TestPostgreSQLCatalogMutationSerializesPendingProviderScope(t *testing.T) {
	native, cleanup := tagIntegrationPool(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
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

	// Seed local catalog state before the provider-write seam is bound.
	seed := tagapp.NewService(uow, repository, repository, repository, repository)
	group, tag, err := seed.CreateGroup(ctx, domain.Command{Actor: 7, IdempotencyKey: "scope-seed-group-key", GroupName: "范围", FirstTagName: "初始"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, `INSERT INTO tag_provider_group_bindings(provider_group_id,group_id) VALUES('provider-scope-group',$1)`, group.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, `INSERT INTO tag_provider_tag_bindings(provider_tag_id,tag_id) VALUES('provider-scope-tag',$1)`, tag.ID); err != nil {
		t.Fatal(err)
	}
	service := tagapp.NewService(uow, repository, repository, repository, repository)
	if err = service.BindProviderMutations(serialCatalogMutationEnqueuer{}); err != nil {
		t.Fatal(err)
	}

	// Two different idempotency keys race a single tag. The parent-group lock
	// and pending receipt let exactly one local mutation and one EER intent win.
	start := make(chan struct{})
	type updateResult struct{ err error }
	results := make(chan updateResult, 2)
	for _, name := range []string{"名称-A", "名称-B"} {
		name := name
		go func() {
			<-start
			_, updateErr := service.UpdateTag(ctx, domain.Command{Actor: 7, TagID: tag.ID, IdempotencyKey: "scope-rename-" + name, TagName: name})
			results <- updateResult{err: updateErr}
		}()
	}
	close(start)
	successes, conflicts := 0, 0
	for range 2 {
		result := <-results
		if result.err == nil {
			successes++
		} else if errors.Is(result.err, tagapp.ErrConflict) {
			conflicts++
		} else {
			t.Fatalf("concurrent update error=%v", result.err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent updates successes=%d conflicts=%d", successes, conflicts)
	}
	var renameEffects, renameAudits int
	var renameEffect string
	if err = native.QueryRow(ctx, `SELECT count(*) FROM tag_catalog_mutation_receipts WHERE operation='tag_update'`).Scan(&renameEffects); err != nil || renameEffects != 1 {
		t.Fatalf("rename receipts=%d err=%v", renameEffects, err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM tag_audit_events WHERE event_type='tag.catalog_tag_update'`).Scan(&renameAudits); err != nil || renameAudits != 1 {
		t.Fatalf("rename audit rows=%d err=%v", renameAudits, err)
	}
	if err = native.QueryRow(ctx, `SELECT effect_ref FROM tag_catalog_mutation_receipts WHERE operation='tag_update'`).Scan(&renameEffect); err != nil {
		t.Fatal(err)
	}

	// An ambiguous outcome is still unresolved. A new key cannot overwrite the
	// local name or make a second external call until a real completion arrives.
	completeCatalogMutationForTest(t, ctx, uow, repository, renameEffect, "outcome_unknown", "", "")
	if _, err = service.UpdateTag(ctx, domain.Command{Actor: 7, TagID: tag.ID, IdempotencyKey: "scope-rename-unknown", TagName: "未知后不得覆盖"}); !errors.Is(err, tagapp.ErrConflict) {
		t.Fatalf("unknown prior effect update error=%v", err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM tag_catalog_mutation_receipts WHERE operation='tag_update'`).Scan(&renameEffects); err != nil || renameEffects != 1 {
		t.Fatalf("unknown prior effect created another receipt count=%d err=%v", renameEffects, err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM tag_audit_events WHERE event_type='tag.catalog_tag_update'`).Scan(&renameAudits); err != nil || renameAudits != 1 {
		t.Fatalf("unknown prior effect created another audit count=%d err=%v", renameAudits, err)
	}

	// A terminal reconciliation releases the scope; the next mutation then
	// queues normally and may itself receive an executed readback.
	completeCatalogMutationForTest(t, ctx, uow, repository, renameEffect, "reconciled", "", "")
	if _, err = service.UpdateTag(ctx, domain.Command{Actor: 7, TagID: tag.ID, IdempotencyKey: "scope-rename-after-reconcile", TagName: "确认后可改"}); err != nil {
		t.Fatalf("terminal prior effect did not release scope: %v", err)
	}
	var executedRenameEffect string
	if err = native.QueryRow(ctx, `SELECT effect_ref FROM tag_catalog_mutation_receipts WHERE operation='tag_update' ORDER BY id DESC LIMIT 1`).Scan(&executedRenameEffect); err != nil {
		t.Fatal(err)
	}
	completeCatalogMutationForTest(t, ctx, uow, repository, executedRenameEffect, "executed", "", "")

	// A child create owns the same parent scope. A group archive that races it
	// must roll back entirely: no hidden archive audit, local archive, or EER.
	child, err := service.CreateTag(ctx, domain.Command{Actor: 7, GroupID: group.ID, GroupName: group.Name, TagName: "待同步子标签", IdempotencyKey: "scope-child-create"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.ArchiveGroup(ctx, domain.Command{Actor: 7, GroupID: group.ID, IdempotencyKey: "scope-group-archive-pending-child"}); !errors.Is(err, tagapp.ErrConflict) {
		t.Fatalf("archive with pending child create error=%v", err)
	}
	if _, err = service.GetGroup(ctx, group.ID); err != nil {
		t.Fatalf("group archive rollback lost local group: %v", err)
	}
	if _, err = service.GetTag(ctx, child.ID); err != nil {
		t.Fatalf("group archive rollback lost child tag: %v", err)
	}
	var archiveEffects, archiveAudits int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM tag_catalog_mutation_receipts WHERE operation='group_archive'`).Scan(&archiveEffects); err != nil || archiveEffects != 0 {
		t.Fatalf("blocked group archive receipt count=%d err=%v", archiveEffects, err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM tag_audit_events WHERE event_type='tag.catalog_group_archive'`).Scan(&archiveAudits); err != nil || archiveAudits != 0 {
		t.Fatalf("blocked group archive audit count=%d err=%v", archiveAudits, err)
	}
	var childEffect string
	if err = native.QueryRow(ctx, `SELECT effect_ref FROM tag_catalog_mutation_receipts WHERE operation='tag_create' ORDER BY id DESC LIMIT 1`).Scan(&childEffect); err != nil {
		t.Fatal(err)
	}
	completeCatalogMutationForTest(t, ctx, uow, repository, childEffect, "executed", "", "provider-scope-child")
	if _, err = service.ArchiveGroup(ctx, domain.Command{Actor: 7, GroupID: group.ID, IdempotencyKey: "scope-group-archive-after-child"}); err != nil {
		t.Fatalf("terminal child effect did not release group archive: %v", err)
	}
}

func completeCatalogMutationForTest(t *testing.T, ctx context.Context, uow platformport.UnitOfWork, repository *Repository, effectRef, state, providerGroupID, providerTagID string) {
	t.Helper()
	if err := uow.Within(ctx, func(tx context.Context) error {
		return repository.CompleteCatalogMutation(tx, tagport.CatalogMutationCompletion{
			EffectRef: effectRef, State: state, ResultDigest: "sha256:" + strings.Repeat("d", 64), ProviderGroupID: providerGroupID, ProviderTagID: providerTagID,
			Attempt: 1, Generation: 1, Fence: 1, CompletedAt: time.Now().UTC(),
		})
	}); err != nil {
		t.Fatalf("complete %s state=%s: %v", effectRef, state, err)
	}
}

func reserveCatalogMutation(t *testing.T, ctx context.Context, uow platformport.UnitOfWork, repository *Repository, plan tagport.CatalogMutationPlan) tagport.CatalogMutationDispatch {
	t.Helper()
	var intent tagport.CatalogMutationIntent
	if err := uow.Within(ctx, func(tx context.Context) error {
		var err error
		intent, err = repository.ReserveCatalogMutation(tx, plan)
		if err != nil {
			return err
		}
		return repository.AcceptCatalogMutation(tx, intent.ID, tagport.CatalogMutationEffectReceipt{EffectID: intent.ID, QueueJobID: intent.ID, EffectRef: "eer_" + strconv.FormatInt(intent.ID, 10), EffectState: "queued", AcceptReceiptID: "eerop_" + strconv.FormatInt(intent.ID, 10), QueueReceiptID: "eerop_" + strconv.FormatInt(intent.ID, 10)})
	}); err != nil {
		t.Fatal(err)
	}
	var dispatch tagport.CatalogMutationDispatch
	if err := uow.Within(ctx, func(tx context.Context) error {
		var err error
		dispatch, err = repository.ReadCatalogMutationDispatch(tx, catalogMutationSource(intent.ID))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return dispatch
}

func tagIntegrationPool(t *testing.T) (*pgxpool.Pool, func()) {
	return tagIntegrationPoolWithProjection(t, true)
}

func tagIntegrationPoolWithProjection(t *testing.T, includeProjection bool) (*pgxpool.Pool, func()) {
	t.Helper()
	raw, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("DATABASE_URL is not configured; skipping PostgreSQL integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	config, err := pgxpool.ParseConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	var bytes [8]byte
	if _, err = rand.Read(bytes[:]); err != nil {
		t.Fatal(err)
	}
	schema := "aicrm_tags_test_" + hex.EncodeToString(bytes[:])
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	config = config.Copy()
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate integration test")
	}
	base := filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations")
	effectsSQL, err := os.ReadFile(filepath.Join(base, "0005_external_effects.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, string(effectsSQL)); err != nil {
		t.Fatal(err)
	}
	sql, err := os.ReadFile(filepath.Join(base, "0008_tag_catalog.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, string(sql)); err != nil {
		t.Fatal(err)
	}
	if includeProjection {
		projectionSQL, readErr := os.ReadFile(filepath.Join(base, "0019_tag_catalog_sync_projection.sql"))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, err = pool.Exec(ctx, string(projectionSQL)); err != nil {
			t.Fatal(err)
		}
		applyTagCatalogMutationMigrations(t, pool, base)
	}
	return pool, func() {
		pool.Close()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_, _ = admin.Exec(cleanupCtx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close()
	}
}

func applyTagCatalogMutationMigrations(t *testing.T, pool *pgxpool.Pool, base string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	for _, migration := range []string{"0106_tag_catalog_mutation_effect.sql", "0107_tag_catalog_mutation_receipts.sql"} {
		body, err := os.ReadFile(filepath.Join(base, migration))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, string(body)); err != nil {
			t.Fatalf("apply %s: %v", migration, err)
		}
	}
}
