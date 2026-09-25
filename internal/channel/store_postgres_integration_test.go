package channel

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	channeldomain "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/domain"
	channelport "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/port"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformoutbox "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/outbox"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func TestPostgreSQLChannelCatalogAtomicReplayAndImmutableVersionsIntegration(t *testing.T) {
	pool, cleanup := channelIntegrationPool(t)
	defer cleanup()
	unit, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	actorID := insertChannelAdmin(t, ctx, pool)
	store := NewPostgreSQLCatalogStore()
	staff := fixedCatalogStaffReader{actorID: actorID}
	failed := NewCatalogService(unit, store, store, failingCatalogEventAppender{}, nil, nil, staff)
	create := validCatalogCreate()
	create.Config.Assignment.Assignees[0].StaffID = actorID
	command := CatalogMutation{ActorID: actorID, IdempotencyKey: "pg-channel-create-0001", Create: create}
	if _, err = failed.Create(ctx, command); err == nil {
		t.Fatal("event append failure did not roll back")
	}
	var rolledBack int
	if err = pool.Native().QueryRow(ctx, `SELECT (SELECT count(*) FROM channels)+(SELECT count(*) FROM channel_operation_receipts)`).Scan(&rolledBack); err != nil || rolledBack != 0 {
		t.Fatalf("rolled back rows=%d err=%v", rolledBack, err)
	}

	auditService, err := platformaudit.NewService(platformaudit.NewPostgreSQLStore())
	if err != nil {
		t.Fatal(err)
	}
	events, err := NewChannelCatalogEventAppender(auditService, platformoutbox.NewPostgreSQL())
	if err != nil {
		t.Fatal(err)
	}
	service := NewCatalogService(unit, store, store, events, nil, nil, staff)
	created, err := service.Create(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := service.Create(ctx, command)
	if err != nil || !reflect.DeepEqual(replayed, created) {
		t.Fatalf("replay=%#v want=%#v err=%v", replayed, created, err)
	}
	update := CatalogMutation{ActorID: actorID, IdempotencyKey: "pg-channel-update-0001", Update: channeldomain.UpdateChannel{ExpectedVersion: created.Version, Code: created.Code, Status: channeldomain.StatusInactive, Config: created.Config}}
	updated, err := service.Update(ctx, created.ID, update)
	if err != nil || updated.Version != 2 || updated.ConfigVersion != 2 {
		t.Fatalf("update=%#v err=%v", updated, err)
	}
	var channels, versions, receipts, audits, outbox int
	if err = pool.Native().QueryRow(ctx, `SELECT (SELECT count(*) FROM channels),(SELECT count(*) FROM channel_config_versions),(SELECT count(*) FROM channel_operation_receipts),(SELECT count(*) FROM audit_events WHERE resource_type='channel'),(SELECT count(*) FROM outbox_events WHERE aggregate_type='channel')`).Scan(&channels, &versions, &receipts, &audits, &outbox); err != nil {
		t.Fatal(err)
	}
	if channels != 1 || versions != 2 || receipts != 2 || audits != 2 || outbox != 2 {
		t.Fatalf("rows channels=%d versions=%d receipts=%d audits=%d outbox=%d", channels, versions, receipts, audits, outbox)
	}
	if _, err = pool.Native().Exec(ctx, `UPDATE channel_config_versions SET name=name`); err == nil {
		t.Fatal("immutable channel configuration accepted update")
	}
	if _, err = pool.Native().Exec(ctx, `DELETE FROM channels WHERE id=$1`, created.ID); err == nil {
		t.Fatal("archive-only channel accepted delete")
	}
	if _, err = store.Create(ctx, created, actorID); !errors.Is(err, platformpostgres.ErrTransactionNeeded) {
		t.Fatalf("write outside transaction error=%v", err)
	}
}

func TestPostgreSQLArchivedChannelStaffEditAndReactivateIntegration(t *testing.T) {
	pool, cleanup := channelIntegrationPool(t)
	defer cleanup()
	unit, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	actor := insertChannelAdmin(t, ctx, pool)
	store := NewPostgreSQLCatalogStore()
	auditService, err := platformaudit.NewService(platformaudit.NewPostgreSQLStore())
	if err != nil {
		t.Fatal(err)
	}
	events, err := NewChannelCatalogEventAppender(auditService, platformoutbox.NewPostgreSQL())
	if err != nil {
		t.Fatal(err)
	}
	service := NewCatalogService(unit, store, store, events, nil, nil, catalogStaffRefs{})
	create := validCatalogCreate()
	create.Status = channeldomain.StatusArchived
	create.Config.Assignment.Assignees[0].StaffID = actor
	var replacement int64
	if err = pool.Native().QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name) VALUES ('replacement_operator','$argon2id$fixture','Replacement') RETURNING id`).Scan(&replacement); err != nil {
		t.Fatal(err)
	}
	created, err := service.Create(ctx, CatalogMutation{ActorID: actor, IdempotencyKey: "archived-staff-create", Create: create})
	if err != nil {
		t.Fatal(err)
	}
	config := created.Config
	config.Assignment.Assignees = []channeldomain.Assignee{{StaffID: replacement, Priority: 1, Ratio: 100}}
	update := CatalogMutation{ActorID: actor, IdempotencyKey: "archived-staff-edit", Update: channeldomain.UpdateChannel{ExpectedVersion: created.Version, Code: created.Code, Status: channeldomain.StatusArchived, Config: config}}
	edited, err := service.Update(ctx, created.ID, update)
	if err != nil {
		t.Fatal(err)
	}
	if edited.Status != channeldomain.StatusArchived || edited.CanPublish() || edited.Version != 2 || edited.Config.Assignment.Assignees[0].StaffID != replacement {
		t.Fatalf("unexpected archived edit: %+v", edited)
	}
	replay, err := service.Update(ctx, created.ID, update)
	if err != nil || !reflect.DeepEqual(replay, edited) {
		t.Fatalf("replay mismatch: %v", err)
	}
	stale := update
	stale.IdempotencyKey = "archived-staff-stale"
	if _, err = service.Update(ctx, created.ID, stale); !errors.Is(err, ErrCatalogConflict) {
		t.Fatalf("stale edit: %v", err)
	}
	read, err := service.Get(ctx, created.ID)
	if err != nil || !reflect.DeepEqual(read, edited) {
		t.Fatalf("readback mismatch: %v", err)
	}
	update.IdempotencyKey = "archived-staff-reactivate"
	update.Update.ExpectedVersion = edited.Version
	update.Update.Status = channeldomain.StatusActive
	active, err := service.Update(ctx, created.ID, update)
	if err != nil || !active.CanPublish() || active.Version != 3 {
		t.Fatalf("reactivation: %+v %v", active, err)
	}
	var versions, receipts, audits, outbox int
	if err = pool.Native().QueryRow(ctx, `SELECT (SELECT count(*) FROM channel_config_versions),(SELECT count(*) FROM channel_operation_receipts),(SELECT count(*) FROM audit_events WHERE resource_type='channel'),(SELECT count(*) FROM outbox_events WHERE aggregate_type='channel')`).Scan(&versions, &receipts, &audits, &outbox); err != nil {
		t.Fatal(err)
	}
	if versions != 3 || receipts != 3 || audits != 3 || outbox != 3 {
		t.Fatalf("atomic records: %d %d %d %d", versions, receipts, audits, outbox)
	}
	var archivedAt *time.Time
	if err = pool.Native().QueryRow(ctx, `SELECT archived_at FROM channels WHERE id=$1`, created.ID).Scan(&archivedAt); err != nil || archivedAt != nil {
		t.Fatalf("reactivated archive marker: %v %v", archivedAt, err)
	}
}

func TestPostgreSQLCatalogReadsMigrationBlockedAssignmentForRepairIntegration(t *testing.T) {
	pool, cleanup := channelIntegrationPool(t)
	defer cleanup()
	unit, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	actorID := insertChannelAdmin(t, ctx, pool)
	store := NewPostgreSQLCatalogStore()
	events, err := NewChannelCatalogEventAppender(mustChannelAuditService(t), platformoutbox.NewPostgreSQL())
	if err != nil {
		t.Fatal(err)
	}
	service := NewCatalogService(unit, store, store, events, nil, nil, fixedCatalogStaffReader{actorID: actorID})
	create := validCatalogCreate()
	create.Config.Assignment.Assignees[0].StaffID = actorID
	created, err := service.Create(ctx, CatalogMutation{ActorID: actorID, IdempotencyKey: "pg-channel-blocked-read-0001", Create: create})
	if err != nil {
		t.Fatal(err)
	}
	digest := make([]byte, sha256.Size)
	var importRunID, repairRunID int64
	if err = pool.Native().QueryRow(ctx, `INSERT INTO channel_history_import_runs(snapshot_id,source_host_digest,snapshot_timestamp,manifest_digest,state,completed_at) VALUES('blocked-read-snapshot',$1,clock_timestamp(),$1,'reconciled',clock_timestamp()) RETURNING id`, digest).Scan(&importRunID); err != nil {
		t.Fatal(err)
	}
	if err = pool.Native().QueryRow(ctx, `INSERT INTO channel_semantic_repair_runs(import_run_id,state,source_config_count,repaired_config_count,conflict_count,completed_at) VALUES($1,'blocked',1,1,0,clock_timestamp()) RETURNING id`, importRunID).Scan(&repairRunID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Native().Exec(ctx, `INSERT INTO channel_config_versions(
		channel_id,config_version,channel_type,carrier_type,name,scene_value,qrcode_url,customer_channel,link_url,final_url,welcome_message,
		welcome_image_ids,welcome_miniprogram_ids,welcome_attachment_ids,welcome_group_invite_ids,auto_accept_friend,
		entry_tag_id,entry_tag_name,entry_tag_group_name,assignment_mode,assignment_strategy,overflow_policy,config_digest,created_by,created_at)
		SELECT channel_id,2,channel_type,carrier_type,name,scene_value,qrcode_url,customer_channel,link_url,final_url,welcome_message,
		welcome_image_ids,welcome_miniprogram_ids,welcome_attachment_ids,welcome_group_invite_ids,auto_accept_friend,
		entry_tag_id,entry_tag_name,entry_tag_group_name,assignment_mode,assignment_strategy,overflow_policy,$2,created_by,clock_timestamp()
		FROM channel_config_versions WHERE channel_id=$1 AND config_version=1`, created.ID, digest); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Native().Exec(ctx, `UPDATE channels SET current_config_version=2,version=version+1,status='inactive',archived_at=NULL,updated_at=clock_timestamp() WHERE id=$1`, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Native().Exec(ctx, `INSERT INTO channel_semantic_repaired_configs(repair_run_id,channel_id,config_version,desired_status,blockers) VALUES($1,$2,2,'active','["assignees_missing"]')`, repairRunID, created.ID); err != nil {
		t.Fatal(err)
	}
	var loaded channeldomain.Channel
	if err = unit.Within(ctx, func(txctx context.Context) error {
		var readErr error
		loaded, readErr = store.Get(txctx, created.ID)
		return readErr
	}); err != nil {
		t.Fatal(err)
	}
	if loaded.Status != channeldomain.StatusInactive || loaded.ConfigVersion != 2 || len(loaded.Config.Assignment.Assignees) != 0 || loaded.CanPublish() {
		t.Fatalf("blocked migration config was not safely readable: %#v", loaded)
	}
}

func TestPostgreSQLAssetVersionLockAcceptsTypedChannelIDIntegration(t *testing.T) {
	pool, cleanup := channelIntegrationPool(t)
	defer cleanup()
	unit, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store := NewPostgreSQLAssetStore(pool.Native())
	var version int64
	if err = unit.Within(ctx, func(txContext context.Context) error {
		var nextErr error
		version, nextErr = store.NextAssetVersion(txContext, 49, AcquisitionAssetQRCode)
		return nextErr
	}); err != nil {
		t.Fatalf("next asset version rejected bigint channel id: %v", err)
	}
	if version != 1 {
		t.Fatalf("next asset version=%d want=1", version)
	}
}

func TestPostgreSQLAssetVersionBlocksUnresolvedProviderWriteIntegration(t *testing.T) {
	pool, cleanup := channelIntegrationPool(t)
	defer cleanup()
	unit, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	actor := insertChannelAdmin(t, ctx, pool)
	store := NewPostgreSQLAssetStore(pool.Native())
	var channelID, assetID int64
	err = unit.Within(ctx, func(txContext context.Context) error {
		tx, txErr := platformpostgres.RequireTransaction(txContext)
		if txErr != nil {
			return txErr
		}
		if txErr = tx.QueryRow(txContext, `INSERT INTO channels(code,status,created_at,updated_at) VALUES('pending-asset-fixture','active',clock_timestamp(),clock_timestamp()) RETURNING id`).Scan(&channelID); txErr != nil {
			return txErr
		}
		if _, txErr = tx.Exec(txContext, `INSERT INTO channel_config_versions(channel_id,config_version,channel_type,carrier_type,name,assignment_mode,assignment_strategy,config_digest,created_by,created_at) VALUES($1,1,'qrcode','qrcode','fixture','single_owner','ratio',$2,$3,clock_timestamp())`, channelID, make([]byte, 32), actor); txErr != nil {
			return txErr
		}
		return tx.QueryRow(txContext, `INSERT INTO channel_acquisition_assets(channel_id,config_version,asset_version,kind,source_ref_digest,operation_key_digest,request_digest,effect_ref,accept_receipt_ref,queue_receipt_ref,state,created_by) VALUES($1,1,1,'contact_way_qrcode',$2,$3,$3,'eer_1','eerop_1','eerop_2','outcome_unknown',$4) RETURNING id`, channelID, string(effectport.Hash("pending-asset-fixture")), make([]byte, 32), actor).Scan(&assetID)
	})
	if err != nil {
		t.Fatal(err)
	}
	err = unit.Within(ctx, func(txContext context.Context) error {
		_, nextErr := store.NextAssetVersion(txContext, channelID, AcquisitionAssetQRCode)
		return nextErr
	})
	if !errors.Is(err, ErrCatalogConflict) {
		t.Fatalf("unresolved Provider write allowed new version: %v", err)
	}
	if _, err = pool.Native().Exec(ctx, `UPDATE channel_acquisition_assets SET state='final_failed' WHERE id=$1`, assetID); err != nil {
		t.Fatal(err)
	}
	var next int64
	err = unit.Within(ctx, func(txContext context.Context) error {
		var nextErr error
		next, nextErr = store.NextAssetVersion(txContext, channelID, AcquisitionAssetQRCode)
		return nextErr
	})
	if err != nil || next != 2 {
		t.Fatalf("reconciled failure did not release the version lock: version=%d err=%v", next, err)
	}
}

func TestPostgreSQLVerifiedLegacyAssetCanBeRetiredIntegration(t *testing.T) {
	pool, cleanup := channelIntegrationPool(t)
	defer cleanup()
	unit, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	actorID := insertChannelAdmin(t, ctx, pool)
	store := NewPostgreSQLCatalogStore()
	events, err := NewChannelCatalogEventAppender(mustChannelAuditService(t), platformoutbox.NewPostgreSQL())
	if err != nil {
		t.Fatal(err)
	}
	service := NewCatalogService(unit, store, store, events, nil, nil, fixedCatalogStaffReader{actorID: actorID})
	create := validCatalogCreate()
	create.Config.Assignment.Assignees[0].StaffID = actorID
	created, err := service.Create(ctx, CatalogMutation{ActorID: actorID, IdempotencyKey: "pg-legacy-retire-0001", Create: create})
	if err != nil {
		t.Fatal(err)
	}
	digest := make([]byte, sha256.Size)
	var importRunID int64
	if err = pool.Native().QueryRow(ctx, `INSERT INTO channel_history_import_runs(snapshot_id,source_host_digest,snapshot_timestamp,manifest_digest,state,completed_at) VALUES('legacy-retire-snapshot',$1,clock_timestamp(),$1,'reconciled',clock_timestamp()) RETURNING id`, digest).Scan(&importRunID); err != nil {
		t.Fatal(err)
	}
	var legacyID int64
	if err = pool.Native().QueryRow(ctx, `INSERT INTO channel_legacy_acquisition_assets(import_run_id,source_asset_id,channel_id,config_version,asset_version,kind,provider_asset_ref,result_url,source_status,verification_status,source_digest,provider_readback_digest,verified_at) VALUES($1,1,$2,$3,1000000001,'contact_way_qrcode','provider-reference','https://wework.qpic.cn/example.png','active','legacy_verified_active',$4,'sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',clock_timestamp()) RETURNING id`, importRunID, created.ID, created.ConfigVersion, digest).Scan(&legacyID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Native().Exec(ctx, `UPDATE channel_legacy_acquisition_assets SET retired_at=clock_timestamp(),updated_at=clock_timestamp() WHERE id=$1`, legacyID); err != nil {
		t.Fatalf("retire verified legacy asset: %v", err)
	}
	var status string
	var retired bool
	if err = pool.Native().QueryRow(ctx, `SELECT verification_status,retired_at IS NOT NULL FROM channel_legacy_acquisition_assets WHERE id=$1`, legacyID).Scan(&status, &retired); err != nil {
		t.Fatal(err)
	}
	if status != "legacy_verified_active" || !retired {
		t.Fatalf("legacy asset status=%q retired=%v", status, retired)
	}
}

func TestPostgreSQLPublicLeadQRCodeUsesVerifiedLegacyAssetIntegration(t *testing.T) {
	pool, cleanup := channelIntegrationPool(t)
	defer cleanup()
	unit, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	actorID := insertChannelAdmin(t, ctx, pool)
	store := NewPostgreSQLCatalogStore()
	events, err := NewChannelCatalogEventAppender(mustChannelAuditService(t), platformoutbox.NewPostgreSQL())
	if err != nil {
		t.Fatal(err)
	}
	create := validCatalogCreate()
	create.Config.QRCodeURL = ""
	create.Config.Assignment.Assignees[0].StaffID = actorID
	created, err := NewCatalogService(unit, store, store, events, nil, nil, fixedCatalogStaffReader{actorID: actorID}).Create(ctx, CatalogMutation{ActorID: actorID, IdempotencyKey: "pg-public-lead-qr-0001", Create: create})
	if err != nil {
		t.Fatal(err)
	}
	digest := make([]byte, sha256.Size)
	var importRunID int64
	if err = pool.Native().QueryRow(ctx, `INSERT INTO channel_history_import_runs(snapshot_id,source_host_digest,snapshot_timestamp,manifest_digest,state,completed_at) VALUES('public-lead-qr-snapshot',$1,clock_timestamp(),$1,'reconciled',clock_timestamp()) RETURNING id`, digest).Scan(&importRunID); err != nil {
		t.Fatal(err)
	}
	const want = "https://wework.qpic.cn/public-lead.png"
	if _, err = pool.Native().Exec(ctx, `INSERT INTO channel_legacy_acquisition_assets(import_run_id,source_asset_id,channel_id,config_version,asset_version,kind,provider_asset_ref,result_url,source_status,verification_status,source_digest,provider_readback_digest,verified_at) VALUES($1,1,$2,$3,1,'contact_way_qrcode','provider-reference',$4,'active','legacy_verified_active',$5,'sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb',clock_timestamp())`, importRunID, created.ID, created.ConfigVersion, want, digest); err != nil {
		t.Fatal(err)
	}
	reader := NewPostgreSQLPublicLeadQRCodeReader(unit)
	lead, err := reader.ReadPublicLeadQRCode(ctx, created.ID)
	if err != nil || lead.URL != want {
		t.Fatalf("lead=%+v err=%v", lead, err)
	}
	if _, err = pool.Native().Exec(ctx, `UPDATE channels SET status='archived',archived_at=clock_timestamp() WHERE id=$1`, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = reader.ReadPublicLeadQRCode(ctx, created.ID); err == nil {
		t.Fatal("archived Channel exposed a public QR")
	}
}

func mustChannelAuditService(t *testing.T) *platformaudit.Service {
	t.Helper()
	service, err := platformaudit.NewService(platformaudit.NewPostgreSQLStore())
	if err != nil {
		t.Fatal(err)
	}
	return service
}

type failingCatalogEventAppender struct{}

func (failingCatalogEventAppender) Append(context.Context, channelport.CatalogEvent) error {
	return errors.New("injected event failure")
}

type fixedCatalogStaffReader struct{ actorID int64 }

func (reader fixedCatalogStaffReader) ReadChannelStaff(_ context.Context, ids []int64) ([]channelport.StaffSnapshot, error) {
	if len(ids) != 1 || ids[0] != reader.actorID {
		return nil, errors.New("unknown staff")
	}
	return []channelport.StaffSnapshot{{ID: reader.actorID, Name: "Channel Owner", Active: true}}, nil
}

func TestPostgreSQLChannelCallbackPersistenceIntegration(t *testing.T) {
	pool, cleanup := channelIntegrationPool(t)
	defer cleanup()
	unit, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store := NewPostgreSQLStore()
	now := time.Unix(1_788_336_000, 0).UTC()

	t.Run("digest bindings return zero one or multiple without storing raw state", func(t *testing.T) {
		digest := channelStateDigest("shared-callback-handle")
		first := StateBinding{
			CorpID: "wx-corp", DigestKeyVersion: 1, StateDigest: digest,
			ChannelID: 11, AssetKind: AcquisitionAssetQRCode, AssetVersion: 1,
			BindingDigest: sha256.Sum256([]byte("binding-one")), ActiveFrom: now.Add(-time.Hour),
		}
		if _, _, err := store.PutBinding(ctx, first); !errors.Is(err, platformpostgres.ErrTransactionNeeded) {
			t.Fatalf("binding transaction boundary error=%v", err)
		}
		var storedFirst StateBinding
		if err := unit.Within(ctx, func(txContext context.Context) error {
			var created bool
			var putErr error
			storedFirst, created, putErr = store.PutBinding(txContext, first)
			if putErr == nil && !created {
				return errors.New("first binding was a replay")
			}
			return putErr
		}); err != nil {
			t.Fatal(err)
		}
		if err := unit.Within(ctx, func(txContext context.Context) error {
			_, created, putErr := store.PutBinding(txContext, first)
			if putErr == nil && created {
				return errors.New("exact binding replay inserted another row")
			}
			return putErr
		}); err != nil {
			t.Fatal(err)
		}
		conflictingAsset := first
		conflictingAsset.StateDigest = channelStateDigest("different-handle-for-same-asset")
		conflictingAsset.BindingDigest = sha256.Sum256([]byte("different-binding-for-same-asset"))
		assetResults := make(chan error, 2)
		var assetWait sync.WaitGroup
		for _, candidate := range []StateBinding{first, conflictingAsset} {
			assetWait.Add(1)
			go func(candidate StateBinding) {
				defer assetWait.Done()
				assetResults <- unit.Within(ctx, func(txContext context.Context) error {
					_, _, putErr := store.PutBinding(txContext, candidate)
					return putErr
				})
			}(candidate)
		}
		assetWait.Wait()
		close(assetResults)
		assetConflicts := 0
		for putErr := range assetResults {
			if errors.Is(putErr, ErrStateBindingConflict) {
				assetConflicts++
			} else if putErr != nil {
				t.Fatal(putErr)
			}
		}
		if assetConflicts != 1 {
			t.Fatalf("concurrent immutable asset conflicts=%d", assetConflicts)
		}
		if err := unit.Within(ctx, func(txContext context.Context) error {
			resolution, resolveErr := store.ResolveStateDigestAt(txContext, "wx-corp", digest, 1, now)
			if resolveErr == nil && (resolution.Cardinality != StateDigestOne || resolution.Match.ID != storedFirst.ID) {
				return errors.New("single digest did not resolve exactly once")
			}
			return resolveErr
		}); err != nil {
			t.Fatal(err)
		}

		second := first
		second.ChannelID = 12
		second.BindingDigest = sha256.Sum256([]byte("binding-two"))
		if err := unit.Within(ctx, func(txContext context.Context) error {
			_, _, putErr := store.PutBinding(txContext, second)
			return putErr
		}); err != nil {
			t.Fatal(err)
		}
		if err := unit.Within(ctx, func(txContext context.Context) error {
			resolution, resolveErr := store.ResolveStateDigest(txContext, "wx-corp", digest, now)
			if resolveErr == nil && (resolution.Status != channeldomain.StateAmbiguous || resolution.Asset != (channeldomain.AcquisitionAsset{})) {
				return errors.New("overlapping digest bindings did not fail closed as ambiguous")
			}
			return resolveErr
		}); err != nil {
			t.Fatal(err)
		}

		var storedSecond StateBinding
		if err := unit.Within(ctx, func(txContext context.Context) error {
			var getErr error
			storedSecond, getErr = scanStateBinding(platformpostgresRow(txContext, stateBindingByAssetForUpdateSQL, second.ChannelID, second.AssetKind, second.AssetVersion))
			return getErr
		}); err != nil {
			t.Fatal(err)
		}
		if err := unit.Within(ctx, func(txContext context.Context) error {
			_, applied, endErr := store.EndBinding(txContext, EndStateBinding{BindingID: storedSecond.ID, ExpectedVersion: storedSecond.Version, ActiveUntil: now.Add(time.Second)})
			if endErr == nil && !applied {
				return errors.New("binding end was not applied")
			}
			return endErr
		}); err != nil {
			t.Fatal(err)
		}
		if err := unit.Within(ctx, func(txContext context.Context) error {
			resolution, resolveErr := store.ResolveStateDigestAt(txContext, "wx-corp", digest, 1, now.Add(2*time.Second))
			if resolveErr == nil && (resolution.Cardinality != StateDigestOne || resolution.Match.ID != storedFirst.ID) {
				return errors.New("ended overlap did not restore one match")
			}
			return resolveErr
		}); err != nil {
			t.Fatal(err)
		}
		if err := unit.Within(ctx, func(txContext context.Context) error {
			resolution, resolveErr := store.ResolveStateDigestAt(txContext, "wx-corp", channelStateDigest("missing"), 1, now)
			if resolveErr == nil && resolution.Cardinality != StateDigestZero {
				return errors.New("unknown digest did not return zero")
			}
			return resolveErr
		}); err != nil {
			t.Fatal(err)
		}

		columns := channelTableColumns(t, ctx, pool, "channel_acquisition_state_bindings")
		if !columns["state_digest"] || columns["state"] || columns["raw_state"] || columns["callback_state"] {
			t.Fatalf("unsafe state binding columns=%v", columns)
		}
	})

	t.Run("entrant receipts preserve customer on unmatched and reconcile append-only", func(t *testing.T) {
		customerID := insertChannelCustomer(t, ctx, pool)
		actorID := insertChannelAdmin(t, ctx, pool)
		attributedDigest := channelStateDigest("attributed-handle")
		binding := StateBinding{
			CorpID: "wx-corp", DigestKeyVersion: 1, StateDigest: attributedDigest,
			ChannelID: 21, AssetKind: AcquisitionAssetQRCode, AssetVersion: 3,
			BindingDigest: sha256.Sum256([]byte("attributed-binding")), ActiveFrom: now.Add(-time.Hour),
		}
		if err := unit.Within(ctx, func(txContext context.Context) error {
			_, _, putErr := store.PutBinding(txContext, binding)
			return putErr
		}); err != nil {
			t.Fatal(err)
		}
		var attributed channeldomain.StateResolution
		if err := unit.Within(ctx, func(txContext context.Context) error {
			var resolveErr error
			attributed, resolveErr = store.ResolveStateDigest(txContext, "wx-corp", attributedDigest, now)
			return resolveErr
		}); err != nil || attributed.Status != channeldomain.StateAttributed {
			t.Fatalf("attributed resolution=%+v err=%v", attributed, err)
		}
		attributedCallbackID := "wecom:external-contact:receipt-attributed"
		portReceipt := channelport.EntrantReceipt{
			CallbackID: attributedCallbackID,
			InboxID:    insertChannelInbox(t, ctx, pool, attributedCallbackID), CorpID: "wx-corp",
			ChangeType: "add_external_contact", CustomerID: customerID,
			Status: channelport.EntrantReceiptAttributed, OccurredAt: now, Resolution: attributed,
		}
		for iteration := range 2 {
			if err := unit.Within(ctx, func(txContext context.Context) error {
				return store.RecordEntrantReceipt(txContext, portReceipt)
			}); err != nil {
				t.Fatalf("record attributed iteration=%d err=%v", iteration, err)
			}
		}
		if err := unit.Within(ctx, func(txContext context.Context) error {
			_, _, endErr := store.EndBinding(txContext, EndStateBinding{
				BindingID:       attributedBindingID(t, ctx, pool, 21, AcquisitionAssetQRCode, 3),
				ExpectedVersion: 1, ActiveUntil: now,
			})
			return endErr
		}); !errors.Is(err, ErrStateBindingConflict) {
			t.Fatalf("retroactive binding end error=%v", err)
		}
		var attributedCount int
		if err := pool.Native().QueryRow(ctx, `SELECT count(*) FROM channel_acquisition_entrant_receipts WHERE callback_id=$1`, portReceipt.CallbackID).Scan(&attributedCount); err != nil || attributedCount != 1 {
			t.Fatalf("attributed receipt count=%d err=%v", attributedCount, err)
		}

		unmatchedCallbackID := "wecom:external-contact:receipt-unmatched"
		unmatchedPort := channelport.EntrantReceipt{
			CallbackID: unmatchedCallbackID,
			InboxID:    insertChannelInbox(t, ctx, pool, unmatchedCallbackID), CorpID: "wx-corp",
			ChangeType: "add_half_external_contact", CustomerID: customerID, OccurredAt: now,
			Status:     channelport.EntrantReceiptUnmatched,
			Resolution: channeldomain.StateResolution{Status: channeldomain.StateUnmatched},
		}
		if err := unit.Within(ctx, func(txContext context.Context) error {
			return store.RecordEntrantReceipt(txContext, unmatchedPort)
		}); err != nil {
			t.Fatal(err)
		}
		conflictCallbackID := "wecom:external-contact:receipt-identity-conflict"
		conflictPort := channelport.EntrantReceipt{
			CallbackID: conflictCallbackID,
			InboxID:    insertChannelInbox(t, ctx, pool, conflictCallbackID), CorpID: "wx-corp",
			ChangeType: "add_external_contact", Status: channelport.EntrantReceiptIdentityConflict,
			OccurredAt: now,
		}
		if err := unit.Within(ctx, func(txContext context.Context) error {
			return store.RecordEntrantReceipt(txContext, conflictPort)
		}); err != nil {
			t.Fatal(err)
		}
		var conflictStatus string
		var conflictCustomerID *int64
		if err := pool.Native().QueryRow(ctx, `SELECT status, customer_id FROM channel_acquisition_entrant_receipts WHERE callback_id=$1`, conflictCallbackID).Scan(&conflictStatus, &conflictCustomerID); err != nil {
			t.Fatal(err)
		}
		if conflictStatus != string(EntrantIdentityConflict) || conflictCustomerID != nil {
			t.Fatalf("identity conflict status=%q customer=%v", conflictStatus, conflictCustomerID)
		}
		sharedInboxID := insertChannelInbox(t, ctx, pool, "wecom:external-contact:same-inbox-a")
		inboxEntrant := AppendEntrantReceipt{
			CallbackID: "wecom:external-contact:same-inbox-a", InboxID: sharedInboxID,
			CorpID: "wx-corp", InputDigest: sha256.Sum256([]byte("same-inbox-input-a")),
			CommandDigest: sha256.Sum256([]byte("same-inbox-command-a")),
			Status:        EntrantChannelUnmatched, CustomerID: customerID, OccurredAt: now,
		}
		inboxDrift := inboxEntrant
		inboxDrift.CallbackID = "wecom:external-contact:same-inbox-b"
		inboxDrift.InputDigest = sha256.Sum256([]byte("same-inbox-input-b"))
		inboxDrift.CommandDigest = sha256.Sum256([]byte("same-inbox-command-b"))
		inboxResults := make(chan error, 2)
		var inboxWait sync.WaitGroup
		for _, candidate := range []AppendEntrantReceipt{inboxEntrant, inboxDrift} {
			inboxWait.Add(1)
			go func(candidate AppendEntrantReceipt) {
				defer inboxWait.Done()
				inboxResults <- unit.Within(ctx, func(txContext context.Context) error {
					_, _, putErr := store.PutEntrant(txContext, candidate)
					return putErr
				})
			}(candidate)
		}
		inboxWait.Wait()
		close(inboxResults)
		inboxCreated, inboxConflicts := 0, 0
		for putErr := range inboxResults {
			switch {
			case putErr == nil:
				inboxCreated++
			case errors.Is(putErr, ErrEntrantReceiptConflict):
				inboxConflicts++
			default:
				t.Fatal(putErr)
			}
		}
		if inboxCreated != 1 || inboxConflicts != 1 {
			t.Fatalf("same Inbox entrant created=%d conflicts=%d", inboxCreated, inboxConflicts)
		}
		var unmatched EntrantReceipt
		if err := unit.Within(ctx, func(txContext context.Context) error {
			items, listErr := store.ListUnassigned(txContext, EntrantReceiptPage{Limit: 20})
			if listErr != nil {
				return listErr
			}
			for _, item := range items {
				if item.Status == EntrantChannelUnmatched {
					unmatched = item
				}
			}
			if unmatched.ID < 1 || unmatched.CustomerID != customerID {
				return errors.New("unmatched receipt lost its trusted customer")
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		reconcile := ReconcileEntrantReceipt{
			ReceiptID: unmatched.ID, ExpectedStatus: EntrantChannelUnmatched,
			BindingID:  attributedBindingID(t, ctx, pool, 21, AcquisitionAssetQRCode, 3),
			CustomerID: customerID, ActorAdminUserID: actorID,
			Reason:             "verified local binding selected",
			OperationKeyDigest: sha256.Sum256([]byte("entrant-reconcile-key")),
			CommandDigest:      sha256.Sum256([]byte("entrant-reconcile-command")),
			ReconciledAt:       now.Add(time.Minute),
		}
		var reconciled EntrantReceipt
		for iteration := range 2 {
			if err := unit.Within(ctx, func(txContext context.Context) error {
				var created bool
				var reconcileErr error
				reconciled, created, reconcileErr = store.Reconcile(txContext, reconcile)
				if reconcileErr == nil && created != (iteration == 0) {
					return errors.New("reconciliation replay flag mismatch")
				}
				return reconcileErr
			}); err != nil {
				t.Fatalf("reconcile iteration=%d err=%v", iteration, err)
			}
		}
		if reconciled.Status != EntrantReconciled || reconciled.PriorStatus != EntrantChannelUnmatched || reconciled.CustomerID != customerID {
			t.Fatalf("reconciled=%+v", reconciled)
		}
		drift := reconcile
		drift.CustomerID = insertChannelCustomer(t, ctx, pool)
		if err := unit.Within(ctx, func(txContext context.Context) error {
			_, _, reconcileErr := store.Reconcile(txContext, drift)
			return reconcileErr
		}); !errors.Is(err, ErrEntrantReceiptConflict) {
			t.Fatalf("reconcile idempotency drift error=%v", err)
		}
		if _, err := pool.Native().Exec(ctx, `UPDATE channel_acquisition_entrant_receipts SET status='ignored' WHERE id=$1`, unmatched.ID); err == nil {
			t.Fatal("append-only entrant receipt accepted UPDATE")
		}
		if _, err := pool.Native().Exec(ctx, `DELETE FROM channel_acquisition_entrant_reconciliation_receipts WHERE entrant_receipt_id=$1`, unmatched.ID); err == nil {
			t.Fatal("append-only reconciliation receipt accepted DELETE")
		}
		if _, err := pool.Native().Exec(ctx, `TRUNCATE channel_acquisition_entrant_reconciliation_receipts`); err == nil {
			t.Fatal("append-only reconciliation receipt accepted TRUNCATE")
		}
		columns := channelTableColumns(t, ctx, pool, "channel_acquisition_entrant_receipts")
		for _, forbidden := range []string{"state", "state_digest", "external_userid", "employee_id", "raw_payload"} {
			if columns[forbidden] {
				t.Fatalf("entrant receipts contain forbidden column %q", forbidden)
			}
		}
	})
}

func TestPostgreSQLAudienceEntriesCombineNativeHistoryByExactCodeAndLatestTime(t *testing.T) {
	pool, cleanup := channelIntegrationPool(t)
	defer cleanup()
	unit, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	actorID := insertChannelAdmin(t, ctx, pool)
	catalog := NewPostgreSQLCatalogStore()
	events, err := NewChannelCatalogEventAppender(mustChannelAuditService(t), platformoutbox.NewPostgreSQL())
	if err != nil {
		t.Fatal(err)
	}
	create := validCatalogCreate()
	create.Code = "audience-native-history"
	create.Config.Assignment.Assignees[0].StaffID = actorID
	channel, err := NewCatalogService(unit, catalog, catalog, events, nil, nil, fixedCatalogStaffReader{actorID: actorID}).Create(ctx, CatalogMutation{ActorID: actorID, IdempotencyKey: "audience-channel-entries", Create: create})
	if err != nil {
		t.Fatal(err)
	}
	customer := insertChannelCustomer(t, ctx, pool)
	store := NewPostgreSQLStore()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	binding := StateBinding{CorpID: "wx-audience", DigestKeyVersion: 1, StateDigest: channelStateDigest("audience-native"), ChannelID: channel.ID, AssetKind: AcquisitionAssetQRCode, AssetVersion: 1, BindingDigest: sha256.Sum256([]byte("audience-binding")), ActiveFrom: now.Add(-48 * time.Hour)}
	var resolution channeldomain.StateResolution
	if err := unit.Within(ctx, func(tx context.Context) error { _, _, e := store.PutBinding(tx, binding); return e }); err != nil {
		t.Fatal(err)
	}
	if err := unit.Within(ctx, func(tx context.Context) error {
		var e error
		resolution, e = store.ResolveStateDigest(tx, binding.CorpID, binding.StateDigest, now.Add(-time.Hour))
		return e
	}); err != nil {
		t.Fatal(err)
	}
	callbackID := "wecom:external-contact:audience-native"
	if err := unit.Within(ctx, func(tx context.Context) error {
		return store.RecordEntrantReceipt(tx, channelport.EntrantReceipt{CallbackID: callbackID, InboxID: insertChannelInbox(t, ctx, pool, callbackID), CorpID: binding.CorpID, ChangeType: "add_external_contact", CustomerID: customer, Status: channelport.EntrantReceiptAttributed, OccurredAt: now.Add(-24 * time.Hour), Resolution: resolution})
	}); err != nil {
		t.Fatal(err)
	}
	digest := make([]byte, sha256.Size)
	var runID int64
	if err := pool.Native().QueryRow(ctx, `INSERT INTO channel_history_import_runs(snapshot_id,source_host_digest,snapshot_timestamp,manifest_digest,state,completed_at) VALUES('audience-history',$1,$2,$1,'reconciled',$2) RETURNING id`, digest, now).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Native().Exec(ctx, `INSERT INTO channel_history_contacts(import_run_id,channel_id,source_contact_id,customer_id,owner_reference,first_entered_at,last_entered_at,enter_count) VALUES($1,$2,1,$3,'history-owner',$4,$5,2)`, runID, channel.ID, customer, now.Add(-72*time.Hour), now.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	var facts []channelport.AudienceEntry
	if err := unit.Within(ctx, func(tx context.Context) error { var e error; facts, e = store.AudienceEntries(tx, now); return e }); err != nil {
		t.Fatal(err)
	}
	if len(facts) != 1 || facts[0].CustomerID != customer || facts[0].ChannelCode != channel.Code || !facts[0].LastEnteredAt.Equal(now.Add(-2*time.Hour)) || facts[0].OwnerReference != "history-owner" {
		t.Fatalf("facts=%+v", facts)
	}
}

type channelNoopEffects struct{}

func (channelNoopEffects) AcceptAndQueueWithin(context.Context, effectport.AcceptCommand) (effectport.Projection, effectport.Receipt, error) {
	return effectport.Projection{}, effectport.Receipt{}, errors.New("not used")
}

func TestPostgreSQLChannelEntryTagLegacyAndRejectedProjectionIntegration(t *testing.T) {
	pool, cleanup := channelIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	unit, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	actor := insertChannelAdmin(t, ctx, pool)
	catalog := NewPostgreSQLCatalogStore()
	events, err := NewChannelCatalogEventAppender(mustChannelAuditService(t), platformoutbox.NewPostgreSQL())
	if err != nil {
		t.Fatal(err)
	}
	create := validCatalogCreate()
	create.Code = "entry-tag-projection"
	create.Config.Assignment.Assignees[0].StaffID = actor
	created, err := NewCatalogService(unit, catalog, catalog, events, nil, nil, fixedCatalogStaffReader{actorID: actor}).Create(ctx, CatalogMutation{ActorID: actor, IdempotencyKey: "entry-tag-projection-create", Create: create})
	if err != nil {
		t.Fatal(err)
	}
	customer := insertChannelCustomer(t, ctx, pool)
	var oldAssignment, newAssignment int64
	if err = pool.Native().QueryRow(ctx, `INSERT INTO channel_entrant_assignments(callback_id,channel_id,config_version,customer_id,staff_id,strategy,assignment_digest,assigned_at) VALUES('legacy-entry-tag',$1,$2,$3,$4,'ratio',$5,clock_timestamp()) RETURNING id`, created.ID, created.ConfigVersion, customer, actor, make([]byte, sha256.Size)).Scan(&oldAssignment); err != nil {
		t.Fatal(err)
	}
	if err = pool.Native().QueryRow(ctx, `INSERT INTO channel_entrant_assignments(callback_id,channel_id,config_version,customer_id,staff_id,strategy,assignment_digest,assigned_at) VALUES('rejected-entry-tag',$1,$2,$3,$4,'ratio',$5,clock_timestamp()) RETURNING id`, created.ID, created.ConfigVersion, customer, actor, append(make([]byte, sha256.Size-1), byte(1))).Scan(&newAssignment); err != nil {
		t.Fatal(err)
	}
	legacySource := effectport.Hash("channel.entrant.action.source.v1", "legacy-entry-tag", "entry_tag")
	if _, err = pool.Native().Exec(ctx, `INSERT INTO channel_entrant_actions(callback_id,assignment_id,channel_id,config_version,customer_id,staff_id,action_kind,local_tag_id,source_ref_digest,effect_ref,accept_receipt_ref,queue_receipt_ref,state) VALUES('legacy-entry-tag',$1,$2,$3,$4,$5,'entry_tag',1,$6,'eer_99','eerop_99','eerop_100','queued')`, oldAssignment, created.ID, created.ConfigVersion, customer, actor, legacySource); err != nil {
		t.Fatal(err)
	}
	rejectedSource := effectport.Hash("channel.entrant.action.source.v1", "rejected-entry-tag", "entry_tag")
	if _, err = pool.Native().Exec(ctx, `INSERT INTO channel_entrant_actions(callback_id,assignment_id,channel_id,config_version,customer_id,staff_id,action_kind,local_tag_id,source_ref_digest,state,result_reason) VALUES('rejected-entry-tag',$1,$2,$3,$4,$5,'entry_tag',1,$6,'rejected','target_unavailable')`, newAssignment, created.ID, created.ConfigVersion, customer, actor, rejectedSource); err != nil {
		t.Fatal(err)
	}
	// The callback/action unique key is the cross-kind dedupe guard: a later
	// legacy/new adapter attempt cannot manufacture a second tag action.
	if _, err = pool.Native().Exec(ctx, `INSERT INTO channel_entrant_actions(callback_id,assignment_id,channel_id,config_version,customer_id,staff_id,action_kind,local_tag_id,source_ref_digest,state,result_reason) VALUES('rejected-entry-tag',$1,$2,$3,$4,$5,'entry_tag',1,$6,'rejected','target_unavailable') ON CONFLICT(callback_id,action_kind) DO NOTHING`, newAssignment, created.ID, created.ConfigVersion, customer, actor, rejectedSource); err != nil {
		t.Fatal(err)
	}
	var rejected, assignments int
	var reason string
	var refs int
	if err = pool.Native().QueryRow(ctx, `SELECT count(*) FROM channel_entrant_actions WHERE callback_id='rejected-entry-tag'`).Scan(&rejected); err != nil || rejected != 1 {
		t.Fatalf("rejected=%d err=%v", rejected, err)
	}
	if err = pool.Native().QueryRow(ctx, `SELECT result_reason,(effect_ref IS NOT NULL)::int+(accept_receipt_ref IS NOT NULL)::int+(queue_receipt_ref IS NOT NULL)::int FROM channel_entrant_actions WHERE callback_id='rejected-entry-tag'`).Scan(&reason, &refs); err != nil || reason != "target_unavailable" || refs != 0 {
		t.Fatalf("failure reason=%q refs=%d err=%v", reason, refs, err)
	}
	if err = pool.Native().QueryRow(ctx, `SELECT count(*) FROM channel_entrant_assignments WHERE callback_id IN ('legacy-entry-tag','rejected-entry-tag')`).Scan(&assignments); err != nil || assignments != 2 {
		t.Fatalf("assignments=%d err=%v", assignments, err)
	}
	actions := NewEntrantActionStore(channelNoopEffects{}, nil)
	completion := channelport.EntrantActionCompletion{EffectRef: "eer_99", State: "executed", ResultDigest: string(effectport.Hash("entry-tag-done")), Attempt: 1, CompletedAt: time.Now()}
	if err = unit.Within(ctx, func(tx context.Context) error { return actions.CompleteEntrantAction(tx, completion) }); err != nil {
		t.Fatal(err)
	}
	if err = unit.Within(ctx, func(tx context.Context) error { return actions.CompleteEntrantAction(tx, completion) }); err != nil {
		t.Fatalf("completion replay=%v", err)
	}
	var state string
	if err = pool.Native().QueryRow(ctx, `SELECT state FROM channel_entrant_actions WHERE effect_ref='eer_99'`).Scan(&state); err != nil || state != "executed" {
		t.Fatalf("legacy state=%q err=%v", state, err)
	}
}

func platformpostgresRow(ctx context.Context, query string, arguments ...any) pgx.Row {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return errorRow{err: err}
	}
	return tx.QueryRow(ctx, query, arguments...)
}

type errorRow struct{ err error }

func (row errorRow) Scan(...any) error { return row.err }

func channelStateDigest(value string) [32]byte {
	mac := hmac.New(sha256.New, []byte("integration-only-hmac-key"))
	_, _ = mac.Write([]byte(value))
	var digest [32]byte
	copy(digest[:], mac.Sum(nil))
	return digest
}

func insertChannelCustomer(t *testing.T, ctx context.Context, pool *platformpostgres.Pool) customerdomain.CustomerID {
	t.Helper()
	var id int64
	if err := pool.Native().QueryRow(ctx, `INSERT INTO customers (status) VALUES ('active') RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return customerdomain.CustomerID(id)
}

func insertChannelAdmin(t *testing.T, ctx context.Context, pool *platformpostgres.Pool) int64 {
	t.Helper()
	var id int64
	if err := pool.Native().QueryRow(ctx, `
		INSERT INTO admin_users (username, password_hash, display_name)
		VALUES ('channel_operator', '$argon2id$fixture', 'Channel Operator')
		RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Native().Exec(ctx, `INSERT INTO admin_user_roles (admin_user_id, role_code) VALUES ($1, 'super_admin')`, id); err != nil {
		t.Fatal(err)
	}
	return id
}

func insertChannelInbox(t *testing.T, ctx context.Context, pool *platformpostgres.Pool, idempotencyKey string) int64 {
	t.Helper()
	digest := sha256.Sum256([]byte("channel-inbox-" + idempotencyKey))
	var id int64
	err := pool.Native().QueryRow(ctx, `
		INSERT INTO webhook_inbox (
			provider, idempotency_key, payload_hash, payload, status,
			attempt_count, max_attempts, next_attempt_at
		) VALUES ('wecom.external_contact', $1, $2, '{}'::jsonb, 'processing', 1, 8, clock_timestamp())
		RETURNING id`, idempotencyKey, digest[:]).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func attributedBindingID(t *testing.T, ctx context.Context, pool *platformpostgres.Pool, channelID int64, kind AcquisitionAssetKind, version int64) int64 {
	t.Helper()
	var id int64
	if err := pool.Native().QueryRow(ctx, `
		SELECT id FROM channel_acquisition_state_bindings
		WHERE channel_id=$1 AND asset_kind=$2 AND asset_version=$3`, channelID, kind, version).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func channelTableColumns(t *testing.T, ctx context.Context, pool *platformpostgres.Pool, table string) map[string]bool {
	t.Helper()
	rows, err := pool.Native().Query(ctx, `
		SELECT column_name FROM information_schema.columns
		WHERE table_schema=current_schema() AND table_name=$1`, table)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns := make(map[string]bool)
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		columns[name] = true
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return columns
}

func channelIntegrationPool(t *testing.T) (*platformpostgres.Pool, func()) {
	t.Helper()
	databaseURL, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured; skipping PostgreSQL integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal("parse AICRM_DATABASE_URL")
	}
	admin, err := pgxpool.NewWithConfig(ctx, config.Copy())
	if err != nil {
		t.Fatal("open PostgreSQL integration database")
	}
	if err = admin.Ping(ctx); err != nil {
		admin.Close()
		t.Fatal("ping PostgreSQL integration database")
	}
	random := make([]byte, 8)
	if _, err = rand.Read(random); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	schema := "aicrm_channel_test_" + hex.EncodeToString(random)
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		admin.Close()
		t.Fatal("create PostgreSQL integration schema")
	}
	testConfig := config.Copy()
	testConfig.ConnConfig.RuntimeParams["search_path"] = schema
	native, err := pgxpool.NewWithConfig(ctx, testConfig)
	if err != nil {
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close()
		t.Fatal("open isolated PostgreSQL integration schema")
	}
	for _, path := range channelMigrationPaths(t) {
		sql, readErr := os.ReadFile(path)
		if readErr != nil {
			native.Close()
			_, _ = admin.Exec(ctx, "DROP SCHEMA "+identifier+" CASCADE")
			admin.Close()
			t.Fatal(readErr)
		}
		if _, execErr := native.Exec(ctx, string(sql)); execErr != nil {
			native.Close()
			_, _ = admin.Exec(ctx, "DROP SCHEMA "+identifier+" CASCADE")
			admin.Close()
			t.Fatalf("apply channel integration migration %s: %v", filepath.Base(path), execErr)
		}
	}
	pool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		native.Close()
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close()
		t.Fatal(err)
	}
	return pool, func() {
		pool.Close()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_, _ = admin.Exec(cleanupCtx, "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close()
	}
}

func channelMigrationPaths(t *testing.T) []string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate channel integration test source")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	return []string{
		filepath.Join(root, "migrations", "0001_platform.sql"),
		filepath.Join(root, "migrations", "0002_identity.sql"),
		filepath.Join(root, "migrations", "0003_access.sql"),
		filepath.Join(root, "migrations", "0004_wecom.sql"),
		filepath.Join(root, "migrations", "0005_external_effects.sql"),
		filepath.Join(root, "migrations", "0006_wecom_callback_channel_acquisition.sql"),
		filepath.Join(root, "migrations", "0009_customer_activation.sql"),
		filepath.Join(root, "migrations", "0029_channel_center.sql"),
		filepath.Join(root, "migrations", "0031_channel_history_import.sql"),
		filepath.Join(root, "migrations", "0032_channel_acquisition_assets.sql"),
		filepath.Join(root, "migrations", "0033_wecom_welcome_grants.sql"),
		filepath.Join(root, "migrations", "0034_channel_entrant_actions.sql"),
		filepath.Join(root, "migrations", "0035_channel_acquisition_links.sql"),
		filepath.Join(root, "migrations", "0059_channel_v1_semantic_repair.sql"),
		filepath.Join(root, "migrations", "0065_channel_legacy_asset_retirement.sql"),
		filepath.Join(root, "migrations", "0066_channel_welcome_intents.sql"),
		filepath.Join(root, "migrations", "0093_customer_tag_commands.sql"),
		filepath.Join(root, "migrations", "0148_channel_archive_edit.sql"),
		filepath.Join(root, "migrations", "0150_channel_welcome_message_snapshots.sql"),
		filepath.Join(root, "migrations", "0164_channel_welcome_provider_rejections.sql"),
	}
}
