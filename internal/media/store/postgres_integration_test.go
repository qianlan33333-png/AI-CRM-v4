package store

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func TestPostgreSQLContentPackageVersionsSnapshotsBindingsAndCapture(t *testing.T) {
	url, urlErr := platformconfig.DatabaseURL()
	if urlErr != nil {
		t.Skip("database URL not configured")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	raw := make([]byte, 6)
	if _, err = rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	schema := "media_content_" + hex.EncodeToString(raw)
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	native, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer native.Close()
	_, file, _, _ := runtime.Caller(0)
	for _, migration := range []string{"0007_media.sql", "0016_media_content_packages.sql", "0080_media_legacy_material_mappings.sql"} {
		sql, readErr := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations", migration))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, execErr := native.Exec(ctx, string(sql)); execErr != nil {
			t.Fatalf("%s: %v", migration, execErr)
		}
	}
	pool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	image, err := repo.CreateImage(ctx, 7, "content-source-image-key-0001", ImageInput{FileName: "content.png", MIME: "image/png", Name: "content", Content: testPNG(t), Width: 2, Height: 2, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	imageID := image["id"].(int64)
	command := mediaport.ContentPackageCommand{Name: "晨间内容", ContentText: "早上好", Enabled: true, Refs: []mediaport.ContentRef{{Kind: "image", ID: imageID}}, Actor: 7, IdempotencyKey: "content-package-create-0001"}
	now := time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC)
	created := contentPackageMutation(t, ctx, uow, repo, "create", 7, command.IdempotencyKey, command, now, func(tx context.Context) (mediaport.ContentPackage, error) { return repo.Create(tx, command, now) })
	replayed := contentPackageMutation(t, ctx, uow, repo, "create", 7, command.IdempotencyKey, command, now, func(context.Context) (mediaport.ContentPackage, error) {
		t.Fatal("replay unexpectedly owned mutation")
		return mediaport.ContentPackage{}, nil
	})
	if replayed.ID != created.ID || replayed.Version != 1 {
		t.Fatalf("replay=%+v", replayed)
	}
	var sourceDigest, imageDigest string
	if err = native.QueryRow(ctx, `SELECT source_digest FROM media_content_package_version_refs WHERE package_id=$1 AND version=1 AND position=0`, created.ID).Scan(&sourceDigest); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT blob_digest FROM media_images WHERE id=$1`, imageID).Scan(&imageDigest); err != nil || sourceDigest != imageDigest {
		t.Fatalf("snapshot=%q image=%q err=%v", sourceDigest, imageDigest, err)
	}
	mapping := mediaport.LegacyMaterialMapping{Reference: mediaport.LegacyMaterialReference{SourceSystem: "ai-crm-v2", MaterialKind: "image", LegacyID: "legacy-image-1"}, MaterialKind: "image", MaterialID: imageID, SourceDigest: imageDigest, SourceRecordDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	if err = uow.Within(ctx, func(tx context.Context) error {
		return repo.ImportLegacyMaterialMapping(tx, mapping, "frozen-import:test")
	}); err != nil {
		t.Fatal(err)
	}
	if err = uow.Within(ctx, func(tx context.Context) error {
		resolved, found, resolveErr := repo.ResolveLegacyMaterialMapping(tx, mapping.Reference)
		if resolveErr != nil || !found || resolved.MaterialID != imageID || resolved.SourceDigest != imageDigest {
			t.Fatalf("resolved=%+v found=%t err=%v", resolved, found, resolveErr)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	command.IdempotencyKey = "content-package-update-0001"
	command.ContentText = "早上好，更新版"
	update := mediaport.ContentPackageUpdateCommand{ID: created.ID, ExpectedVersion: created.Version, ContentPackageCommand: command}
	updated := contentPackageMutation(t, ctx, uow, repo, "update", 7, command.IdempotencyKey, update, now, func(tx context.Context) (mediaport.ContentPackage, error) { return repo.Update(tx, update, now) })
	if updated.Version != 2 {
		t.Fatalf("updated=%+v", updated)
	}
	var versionCount, refs, protectionRefs int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM media_content_package_versions WHERE package_id=$1`, created.ID).Scan(&versionCount); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM media_content_package_version_refs WHERE package_id=$1`, created.ID).Scan(&refs); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM media_references WHERE material_kind='image' AND material_id=$1 AND owner='media.content_package'`, imageID).Scan(&protectionRefs); err != nil {
		t.Fatal(err)
	}
	if versionCount != 2 || refs != 2 || protectionRefs != 2 {
		t.Fatalf("versions=%d refs=%d protection_refs=%d", versionCount, refs, protectionRefs)
	}
	invite, err := repo.CreateGroupInvite(ctx, 7, "content-bind-invite-0001", map[string]any{"name": "体验群", "title": "加入体验群", "description": "资料", "join_url": "https://work.weixin.qq.com/gm/0123456789abcdef0123456789abcdef", "enabled": true})
	if err != nil {
		t.Fatal(err)
	}
	mini, err := repo.CreateMiniProgram(ctx, 7, "content-capture-mini-0001", map[string]any{"name": "课程卡", "appid": "wx-course", "pagepath": "pages/today", "title": "今日课程", "thumb_image_id": float64(imageID), "enabled": true})
	if err != nil {
		t.Fatal(err)
	}
	var captured mediaport.GroupOpsMaterialSourceSnapshot
	if err = uow.Within(ctx, func(tx context.Context) error {
		var captureErr error
		captured, captureErr = repo.CaptureGroupOpsMaterialSources(tx, mediaport.GroupOpsMaterialPlan{References: []mediaport.GroupOpsMaterialReference{{Kind: "image", ID: imageID}, {Kind: "miniprogram", ID: mini["id"].(int64)}, {Kind: "group_invite", ID: invite["id"].(int64)}}})
		return captureErr
	}); err != nil {
		t.Fatal(err)
	}
	if len(captured.References) != 3 || captured.References[0].SourceDigest != imageDigest || captured.References[1].ThumbnailSourceDigest != imageDigest || captured.References[1].ProviderFields.AppID != "wx-course" || captured.References[2].ProviderFields.URL == "" {
		t.Fatalf("captured=%+v", captured)
	}
	prepRequiredThrough := now.Add(time.Hour)
	prepCommand := mediaport.GroupOpsMaterialPreparationCommand{
		SourceSnapshot:  captured,
		RequiredThrough: prepRequiredThrough,
		Actor:           7,
		IdempotencyKey:  "content-preparation-key-0001",
		Items: []mediaport.GroupOpsMaterialPreparation{
			{Reference: captured.References[0].Reference, SourceDigest: captured.References[0].SourceDigest, ReceiptDigest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", ReadyUntil: prepRequiredThrough.Add(time.Hour), Attachment: mediaport.GroupOpsProviderReadyAttachment{MsgType: "image", MediaID: "provider-image-1"}},
			{Reference: captured.References[1].Reference, SourceDigest: captured.References[1].SourceDigest, ReceiptDigest: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc", ReadyUntil: prepRequiredThrough.Add(time.Hour), Attachment: mediaport.GroupOpsProviderReadyAttachment{MsgType: "miniprogram", MediaID: "provider-mini-1", AppID: "wx-course", PagePath: "pages/today", Title: "今日课程"}},
			{Reference: captured.References[2].Reference, SourceDigest: captured.References[2].SourceDigest, Attachment: captured.References[2].ProviderFields},
		},
	}
	var prepReceipt mediaport.GroupOpsMaterialPreparationReceipt
	if err = uow.Within(ctx, func(tx context.Context) error {
		var prepErr error
		prepReceipt, prepErr = repo.RecordPreparedGroupOpsMaterialsWithin(tx, prepCommand, now)
		return prepErr
	}); err != nil || prepReceipt.ID < 1 {
		t.Fatalf("preparation receipt=%+v err=%v", prepReceipt, err)
	}
	var prepared []mediaport.GroupOpsMaterialPreparation
	if err = uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		prepared, readErr = repo.ReadPreparedGroupOpsMaterials(tx, captured, prepRequiredThrough)
		return readErr
	}); err != nil || len(prepared) != 3 || prepared[0].Attachment.MediaID != "provider-image-1" || prepared[1].Attachment.MediaID != "provider-mini-1" || prepared[2].Attachment.URL != captured.References[2].ProviderFields.URL || prepared[2].ReceiptDigest != "" {
		t.Fatalf("prepared=%+v err=%v", prepared, err)
	}
	var replay mediaport.GroupOpsMaterialPreparationReceipt
	if err = uow.Within(ctx, func(tx context.Context) error {
		var replayErr error
		replay, replayErr = repo.RecordPreparedGroupOpsMaterialsWithin(tx, prepCommand, now.Add(time.Minute))
		return replayErr
	}); err != nil || replay.ID != prepReceipt.ID {
		t.Fatalf("preparation replay=%+v first=%+v err=%v", replay, prepReceipt, err)
	}
	bindingCommand := mediaport.DeliveryBindingCommand{CampaignCode: "campaign-local", PlanID: "plan-local", PackageID: updated.ID, GroupInviteID: invite["id"].(int64), Actor: 7, IdempotencyKey: "content-binding-key-0001"}
	binding := contentBindingMutation(t, ctx, uow, repo, bindingCommand, now)
	if binding.ID < 1 || binding.Version != 1 {
		t.Fatalf("binding=%+v", binding)
	}
	var receipts, audits, outbox int
	if err = native.QueryRow(ctx, `SELECT (SELECT count(*) FROM media_content_delivery_receipts),(SELECT count(*) FROM media_audit_events WHERE resource_kind='content_package'),(SELECT count(*) FROM media_outbox WHERE aggregate_kind='content_package')`).Scan(&receipts, &audits, &outbox); err != nil {
		t.Fatal(err)
	}
	if receipts != 3 || audits != 3 || outbox != 3 {
		t.Fatalf("receipt/audit/outbox=%d/%d/%d", receipts, audits, outbox)
	}
}

func contentPackageMutation(t *testing.T, ctx context.Context, uow *platformpostgres.UnitOfWork, repo *Repository, operation string, actor int64, key string, payload any, now time.Time, mutate func(context.Context) (mediaport.ContentPackage, error)) mediaport.ContentPackage {
	t.Helper()
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	reservation := mediaport.ContentDeliveryMutationReservation{Operation: operation, Actor: actor, KeyDigest: sha256.Sum256([]byte(key)), PayloadDigest: sha256.Sum256(encoded), CreatedAt: now}
	var out mediaport.ContentPackage
	err = uow.Within(ctx, func(tx context.Context) error {
		receipt, owned, e := repo.ReserveMutation(tx, reservation)
		if e != nil {
			return e
		}
		if !owned {
			return json.Unmarshal(receipt.ResultSnapshot, &out)
		}
		out, e = mutate(tx)
		if e != nil {
			return e
		}
		snapshot, e := json.Marshal(out)
		if e != nil {
			return e
		}
		_, e = repo.CompleteMutation(tx, receipt.ID, snapshot)
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func contentBindingMutation(t *testing.T, ctx context.Context, uow *platformpostgres.UnitOfWork, repo *Repository, command mediaport.DeliveryBindingCommand, now time.Time) mediaport.DeliveryBinding {
	t.Helper()
	payload := command
	payload.IdempotencyKey = ""
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	reservation := mediaport.ContentDeliveryMutationReservation{Operation: "bind", Actor: command.Actor, KeyDigest: sha256.Sum256([]byte(command.IdempotencyKey)), PayloadDigest: sha256.Sum256(encoded), CreatedAt: now}
	var out mediaport.DeliveryBinding
	err = uow.Within(ctx, func(tx context.Context) error {
		receipt, owned, e := repo.ReserveMutation(tx, reservation)
		if e != nil {
			return e
		}
		if !owned {
			return json.Unmarshal(receipt.ResultSnapshot, &out)
		}
		out, e = repo.Bind(tx, command, now)
		if e != nil {
			return e
		}
		snapshot, e := json.Marshal(out)
		if e != nil {
			return e
		}
		_, e = repo.CompleteMutation(tx, receipt.ID, snapshot)
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestPostgreSQLReceiptAuditOutboxAndPayloadDrift(t *testing.T) {
	url, urlErr := platformconfig.DatabaseURL()
	if urlErr != nil {
		t.Skip("database URL not configured")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	raw := make([]byte, 6)
	if _, err = rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	schema := "media_it_" + hex.EncodeToString(raw)
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	native, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer native.Close()
	_, file, _, _ := runtime.Caller(0)
	sql, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations", "0007_media.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, string(sql)); err != nil {
		t.Fatal(err)
	}
	pool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	key := "integration-key-0001"
	first, err := repo.CreateMiniProgram(ctx, 7, key, map[string]any{"name": "素材", "appid": "wx123", "pagepath": "pages/a", "title": "卡片", "enabled": true})
	if err != nil {
		t.Fatal(err)
	}
	replay, err := repo.CreateMiniProgram(ctx, 7, key, map[string]any{"name": "素材", "appid": "wx123", "pagepath": "pages/a", "title": "卡片", "enabled": true})
	if err != nil || fmt.Sprint(replay["id"]) != fmt.Sprint(first["id"]) {
		t.Fatalf("replay=%v err=%v", replay, err)
	}
	if _, err = repo.CreateMiniProgram(ctx, 7, key, map[string]any{"name": "漂移", "appid": "wx123", "pagepath": "pages/a", "title": "卡片"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("payload drift=%v", err)
	}
	var receipts, audits, outbox int
	for _, q := range []string{"SELECT count(*) FROM media_operation_receipts", "SELECT count(*) FROM media_audit_events", "SELECT count(*) FROM media_outbox"} {
		var n int
		if err = native.QueryRow(ctx, q).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if receipts == 0 {
			receipts = n
		} else if audits == 0 {
			audits = n
		} else {
			outbox = n
		}
	}
	if receipts != 1 || audits != 1 || outbox != 1 {
		t.Fatalf("atomic facts receipts=%d audit=%d outbox=%d", receipts, audits, outbox)
	}
}

func TestPostgreSQLReferenceLedgerProtectsDeleteAndArchive(t *testing.T) {
	url, urlErr := platformconfig.DatabaseURL()
	if urlErr != nil {
		t.Skip("database URL not configured")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	raw := make([]byte, 6)
	if _, err = rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	schema := "media_refs_" + hex.EncodeToString(raw)
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	native, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer native.Close()
	_, file, _, _ := runtime.Caller(0)
	sql, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations", "0007_media.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, string(sql)); err != nil {
		t.Fatal(err)
	}
	pool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	imageBytes := testPNG(t)
	created, err := repo.CreateImage(ctx, 7, "image-reference-key-0001", ImageInput{FileName: "cover.png", MIME: "image/png", Name: "cover", Content: imageBytes, Width: 2, Height: 2, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	imageID := created["id"].(int64)
	mini, err := repo.CreateMiniProgram(ctx, 7, "mini-reference-key-0001", map[string]any{"name": "mini", "appid": "wx123", "pagepath": "pages/a", "title": "card", "thumb_image_id": float64(imageID)})
	if err != nil {
		t.Fatal(err)
	}
	attachment, err := repo.CreateAttachment(ctx, 7, "attachment-reference-key-0001", AttachmentInput{FileName: "guide.pdf", Name: "guide", Content: []byte("%PDF-1.4"), Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.Within(ctx, func(tx context.Context) error {
		for _, check := range []struct {
			id int64
			fn func(context.Context, int64) (bool, error)
		}{{imageID, repo.ImageExists}, {mini["id"].(int64), repo.MiniProgramExists}, {attachment["id"].(int64), repo.AttachmentExists}} {
			exists, checkErr := check.fn(tx, check.id)
			if checkErr != nil || !exists {
				t.Fatalf("media stable reader id=%d exists=%v err=%v", check.id, exists, checkErr)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.Delete(ctx, "image", imageID, 7, "delete-image-reference-0001"); !errors.Is(err, ErrReferences) {
		t.Fatalf("local mini reference must block image delete: %v", err)
	}
	references, err := repoReferenceSnapshot(ctx, repo, "image", imageID)
	if err != nil || len(references) != 1 || references[0].Owner != "media.miniprogram.thumbnail" {
		t.Fatalf("references=%+v err=%v", references, err)
	}
	if _, err = repo.Delete(ctx, "miniprogram", mini["id"].(int64), 7, "delete-mini-reference-0001"); err != nil {
		t.Fatal(err)
	}
	deleted, err := repo.Delete(ctx, "image", imageID, 7, "delete-image-reference-0002")
	if err != nil || deleted["references"] != nil {
		t.Fatalf("empty ledger must allow delete without fabricated references: result=%v err=%v", deleted, err)
	}
	cover, err := repo.CreateImage(ctx, 7, "group-cover-image-key-0001", ImageInput{FileName: "group.png", MIME: "image/png", Name: "group", Content: imageBytes, Width: 2, Height: 2, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	coverID := cover["id"].(int64)
	group, err := repo.CreateGroupInvite(ctx, 7, "group-reference-key-0001", map[string]any{"name": "group", "title": "group", "join_url": "https://work.weixin.qq.com/gm/a", "cover_image_id": float64(coverID)})
	if err != nil {
		t.Fatal(err)
	}
	groupID := group["id"].(int64)
	if err = repo.Within(ctx, func(tx context.Context) error {
		exists, checkErr := repo.GroupInviteExists(tx, groupID)
		if checkErr != nil || !exists {
			t.Fatalf("group invite stable reader exists=%v err=%v", exists, checkErr)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	updatedGroup, err := repo.UpdateGroupInvite(ctx, groupID, 8, "group-created-by-key-0001", map[string]any{"description": "updated"})
	if err != nil || updatedGroup["created_by"] != int64(7) || updatedGroup["updated_by"] != int64(8) {
		t.Fatalf("group update ownership=%v err=%v", updatedGroup, err)
	}
	if _, err = repo.Delete(ctx, "image", coverID, 7, "delete-group-cover-key-0001"); !errors.Is(err, ErrReferences) {
		t.Fatalf("local group cover reference must block image delete: %v", err)
	}
	if _, err = native.Exec(ctx, `INSERT INTO media_references(material_kind,material_id,owner,reference_digest) VALUES('group_invite',$1,'automation.group','sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa')`, groupID); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.ArchiveGroupInvite(ctx, groupID, 7, "archive-group-reference-0001"); !errors.Is(err, ErrReferences) {
		t.Fatalf("ledger reference must block archive: %v", err)
	}
	if _, err = native.Exec(ctx, `DELETE FROM media_references WHERE material_kind='group_invite' AND material_id=$1`, groupID); err != nil {
		t.Fatal(err)
	}
	archivedGroup, err := repo.ArchiveGroupInvite(ctx, groupID, 9, "archive-group-reference-0002")
	if err != nil || archivedGroup["created_by"] != int64(7) || archivedGroup["updated_by"] != int64(9) {
		t.Fatalf("group archive ownership=%v err=%v", archivedGroup, err)
	}
	if _, err = repo.Delete(ctx, "image", coverID, 7, "delete-group-cover-key-0002"); !errors.Is(err, ErrReferences) {
		t.Fatalf("archived group must retain the local cover protection fact: %v", err)
	}
}

func repoReferenceSnapshot(ctx context.Context, repo *Repository, kind string, id int64) ([]MediaReference, error) {
	var references []MediaReference
	err := repo.Within(ctx, func(tx context.Context) error {
		var err error
		references, err = repo.ListMediaReferences(tx, kind, id)
		return err
	})
	return references, err
}

func TestPostgreSQLReferenceRegistrationRacesImageDeleteWithoutDanglingLedger(t *testing.T) {
	url, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("database URL not configured")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	raw := make([]byte, 6)
	if _, err = rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	schema := "media_reference_race_" + hex.EncodeToString(raw)
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	native, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer native.Close()
	_, file, _, _ := runtime.Caller(0)
	sql, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations", "0007_media.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, string(sql)); err != nil {
		t.Fatal(err)
	}
	pool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	created, err := repo.CreateImage(ctx, 7, "image-registration-race-0001", ImageInput{FileName: "race.png", MIME: "image/png", Name: "race", Content: testPNG(t), Width: 2, Height: 2, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	imageID := created["id"].(int64)
	start := make(chan struct{})
	results := make(chan error, 2)
	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		<-start
		results <- repo.RegisterMediaReference(ctx, mediaport.MaterialReference{MaterialKind: "image", MaterialID: imageID, Owner: "future.domain", ReferenceDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"})
	}()
	go func() {
		defer wait.Done()
		<-start
		_, deleteErr := repo.Delete(ctx, "image", imageID, 7, "image-delete-race-0001")
		results <- deleteErr
	}()
	close(start)
	wait.Wait()
	close(results)
	for result := range results {
		if result != nil && !errors.Is(result, ErrNotFound) && !errors.Is(result, ErrReferences) && !errors.Is(result, ErrReferenceReaderUnavailable) {
			t.Fatalf("unexpected concurrent result: %v", result)
		}
	}
	var imageExists bool
	var references int
	if err = native.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM media_images WHERE id=$1)`, imageID).Scan(&imageExists); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM media_references WHERE material_kind='image' AND material_id=$1`, imageID).Scan(&references); err != nil {
		t.Fatal(err)
	}
	if !imageExists && references != 0 {
		t.Fatalf("dangling ledger after concurrent delete/register: image=%v references=%d", imageExists, references)
	}
}

func testPNG(t *testing.T) []byte {
	t.Helper()
	value := image.NewRGBA(image.Rect(0, 0, 2, 2))
	value.Set(0, 0, color.RGBA{R: 255, A: 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, value); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

func TestPostgreSQLMultipartPartVsCompleteAndBlobChecksum(t *testing.T) {
	url, urlErr := platformconfig.DatabaseURL()
	if urlErr != nil {
		t.Skip("database URL not configured")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	raw := make([]byte, 6)
	if _, err = rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	schema := "media_concurrent_" + hex.EncodeToString(raw)
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	native, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer native.Close()
	_, file, _, _ := runtime.Caller(0)
	sql, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations", "0007_media.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, string(sql)); err != nil {
		t.Fatal(err)
	}
	pool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("%PDF-1.4\n1 0 obj\n<<>>\nendobj\ntrailer\n<<>>\n%%EOF\n")
	uploadID, err := repo.InitiateAttachmentUpload(ctx, 7, "concurrency-init-key-0001", AttachmentUploadInput{FileName: "guide.pdf", Name: "guide", Size: int64(len(content)), Digest: bytesDigest(content), Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	var wait sync.WaitGroup
	var partErr, completeErr error
	var attachmentID int64
	wait.Add(2)
	go func() {
		defer wait.Done()
		<-start
		partErr = repo.PutAttachmentUploadPart(ctx, uploadID, 1, 7, "concurrency-part-key-0001", bytesDigest(content), content)
	}()
	go func() {
		defer wait.Done()
		<-start
		attachmentID, completeErr = repo.CompleteAttachmentUpload(ctx, uploadID, 7, "concurrency-complete-key-0001")
	}()
	close(start)
	wait.Wait()
	if partErr != nil {
		t.Fatalf("concurrent part failed: %v", partErr)
	}
	if completeErr != nil && !errors.Is(completeErr, ErrConflict) {
		t.Fatalf("concurrent complete err=%v", completeErr)
	}
	if attachmentID == 0 {
		attachmentID, err = repo.CompleteAttachmentUpload(ctx, uploadID, 7, "concurrency-complete-key-0001")
		if err != nil {
			t.Fatalf("complete after part: %v", err)
		}
	}
	var attachmentCount int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM media_attachments`).Scan(&attachmentCount); err != nil || attachmentCount != 1 {
		t.Fatalf("attachment count=%d err=%v", attachmentCount, err)
	}
	if _, _, err = repo.Attachment(ctx, attachmentID); err != nil {
		t.Fatalf("stored attachment=%d err=%v", attachmentID, err)
	}
	if _, err = native.Exec(ctx, `UPDATE media_blobs SET content=convert_to(repeat('x', byte_size::integer),'UTF8') WHERE digest=$1`, bytesDigest(content)); err != nil {
		t.Fatal(err)
	}
	if _, _, err = repo.Attachment(ctx, attachmentID); !errors.Is(err, ErrConflict) {
		t.Fatalf("blob checksum corruption must fail closed: %v", err)
	}
}
