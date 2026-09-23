package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/media/domain"
	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

var _ mediaport.GroupOpsMaterialSourceCapturer = (*Repository)(nil)

// ReserveMutation and CompleteMutation are intentionally separate from the
// older generic Media receipts: ContentDelivery needs raw digests and a typed
// replay snapshot, while legacy UI receipts preserve their frozen DTO shape.
func (r *Repository) ReserveMutation(ctx context.Context, x mediaport.ContentDeliveryMutationReservation) (mediaport.ContentDeliveryMutationReceipt, bool, error) {
	if r == nil || x.Operation == "" || x.Actor < 1 || x.CreatedAt.IsZero() {
		return mediaport.ContentDeliveryMutationReceipt{}, false, ErrInvalid
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return mediaport.ContentDeliveryMutationReceipt{}, false, err
	}
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO media_content_delivery_receipts(operation,actor_admin_user_id,key_digest,payload_digest,created_at)
VALUES($1,$2,$3,$4,$5) ON CONFLICT(operation,actor_admin_user_id,key_digest) DO NOTHING RETURNING id`, x.Operation, x.Actor, x.KeyDigest[:], x.PayloadDigest[:], x.CreatedAt).Scan(&id)
	if err == nil {
		return mediaport.ContentDeliveryMutationReceipt{ID: id, Operation: x.Operation, Actor: x.Actor, KeyDigest: x.KeyDigest, PayloadDigest: x.PayloadDigest}, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return mediaport.ContentDeliveryMutationReceipt{}, false, err
	}
	var operation string
	var actor int64
	var key, payload []byte
	var result []byte
	if err = tx.QueryRow(ctx, `SELECT id,operation,actor_admin_user_id,key_digest,payload_digest,result_snapshot FROM media_content_delivery_receipts WHERE operation=$1 AND actor_admin_user_id=$2 AND key_digest=$3`, x.Operation, x.Actor, x.KeyDigest[:]).Scan(&id, &operation, &actor, &key, &payload, &result); err != nil {
		return mediaport.ContentDeliveryMutationReceipt{}, false, err
	}
	if len(key) != sha256.Size || len(payload) != sha256.Size {
		return mediaport.ContentDeliveryMutationReceipt{}, false, ErrConflict
	}
	var keyDigest, payloadDigest [sha256.Size]byte
	copy(keyDigest[:], key)
	copy(payloadDigest[:], payload)
	return mediaport.ContentDeliveryMutationReceipt{ID: id, Operation: operation, Actor: actor, KeyDigest: keyDigest, PayloadDigest: payloadDigest, ResultSnapshot: json.RawMessage(result)}, false, nil
}

func (r *Repository) CompleteMutation(ctx context.Context, receiptID int64, snapshot json.RawMessage) (mediaport.ContentDeliveryMutationReceipt, error) {
	if r == nil || receiptID < 1 || len(snapshot) == 0 || !json.Valid(snapshot) {
		return mediaport.ContentDeliveryMutationReceipt{}, ErrInvalid
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return mediaport.ContentDeliveryMutationReceipt{}, err
	}
	var operation string
	var actor int64
	var key, payload []byte
	if err = tx.QueryRow(ctx, `UPDATE media_content_delivery_receipts SET result_snapshot=$2::jsonb,completed_at=clock_timestamp()
WHERE id=$1 AND completed_at IS NULL RETURNING operation,actor_admin_user_id,key_digest,payload_digest`, receiptID, snapshot).Scan(&operation, &actor, &key, &payload); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return mediaport.ContentDeliveryMutationReceipt{}, ErrConflict
		}
		return mediaport.ContentDeliveryMutationReceipt{}, err
	}
	if len(key) != sha256.Size || len(payload) != sha256.Size {
		return mediaport.ContentDeliveryMutationReceipt{}, ErrConflict
	}
	resourceID := snapshotID(snapshot)
	if resourceID < 1 {
		return mediaport.ContentDeliveryMutationReceipt{}, ErrConflict
	}
	event := map[string]string{"create": "media.content_package_created", "update": "media.content_package_updated", "bind": "media.content_delivery_bound"}[operation]
	if event == "" {
		event = "media.content_delivery_" + operation
	}
	resourceKind := "content_package"
	if strings.HasPrefix(operation, "upload_") {
		resourceKind = "attachment_upload"
	}
	payloadJSON, err := json.Marshal(map[string]any{"receipt_id": receiptID, "operation": operation, "resource_id": resourceID})
	if err != nil {
		return mediaport.ContentDeliveryMutationReceipt{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO media_audit_events(event_type,resource_kind,resource_id,actor_admin_user_id,payload) VALUES($1,$2,$3,$4,$5::jsonb)`, event, resourceKind, resourceID, actor, payloadJSON); err != nil {
		return mediaport.ContentDeliveryMutationReceipt{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO media_outbox(event_type,aggregate_kind,aggregate_id,payload) VALUES($1,$2,$3,$4::jsonb)`, event, resourceKind, resourceID, payloadJSON); err != nil {
		return mediaport.ContentDeliveryMutationReceipt{}, err
	}
	var kd, pd [sha256.Size]byte
	copy(kd[:], key)
	copy(pd[:], payload)
	return mediaport.ContentDeliveryMutationReceipt{ID: receiptID, Operation: operation, Actor: actor, KeyDigest: kd, PayloadDigest: pd, ResultSnapshot: append(json.RawMessage(nil), snapshot...)}, nil
}

func snapshotID(raw []byte) int64 {
	var item struct {
		ID int64 `json:"id"`
	}
	if json.Unmarshal(raw, &item) == nil && item.ID > 0 {
		return item.ID
	}
	var id int64
	if json.Unmarshal(raw, &id) == nil {
		return id
	}
	return 0
}

func (r *Repository) Eligible(ctx context.Context, kind string, id int64) (bool, error) {
	if r == nil || id < 1 || !mediaKind(kind) {
		return false, ErrInvalid
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return false, err
	}
	var found bool
	switch kind {
	case "image":
		err = tx.QueryRow(ctx, `SELECT enabled FROM media_images WHERE id=$1 FOR KEY SHARE`, id).Scan(&found)
	case "attachment":
		err = tx.QueryRow(ctx, `SELECT enabled FROM media_attachments WHERE id=$1 FOR KEY SHARE`, id).Scan(&found)
	case "miniprogram":
		err = tx.QueryRow(ctx, `SELECT enabled FROM media_miniprograms WHERE id=$1 FOR KEY SHARE`, id).Scan(&found)
	case "group_invite":
		err = tx.QueryRow(ctx, `SELECT enabled AND archived_at IS NULL FROM media_group_invites WHERE id=$1 FOR KEY SHARE`, id).Scan(&found)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return found, err
}

func (r *Repository) Create(ctx context.Context, c mediaport.ContentPackageCommand, now time.Time) (mediaport.ContentPackage, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return mediaport.ContentPackage{}, err
	}
	var id int64
	if err = tx.QueryRow(ctx, `INSERT INTO media_content_packages(name,content_text,enabled,version,created_by,updated_by,created_at,updated_at) VALUES($1,$2,$3,1,$4,$4,$5,$5) RETURNING id`, c.Name, c.ContentText, c.Enabled, c.Actor, now).Scan(&id); err != nil {
		return mediaport.ContentPackage{}, mapContentError(err)
	}
	return r.writeContentVersion(ctx, id, 1, c, now)
}

func (r *Repository) Update(ctx context.Context, c mediaport.ContentPackageUpdateCommand, now time.Time) (mediaport.ContentPackage, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return mediaport.ContentPackage{}, err
	}
	var next int64
	err = tx.QueryRow(ctx, `UPDATE media_content_packages SET name=$1,content_text=$2,enabled=$3,version=version+1,updated_by=$4,updated_at=$5 WHERE id=$6 AND version=$7 RETURNING version`, c.Name, c.ContentText, c.Enabled, c.Actor, now, c.ID, c.ExpectedVersion).Scan(&next)
	if errors.Is(err, pgx.ErrNoRows) {
		return mediaport.ContentPackage{}, ErrConflict
	}
	if err != nil {
		return mediaport.ContentPackage{}, err
	}
	return r.writeContentVersion(ctx, c.ID, next, c.ContentPackageCommand, now)
}

func (r *Repository) writeContentVersion(ctx context.Context, packageID, version int64, c mediaport.ContentPackageCommand, now time.Time) (mediaport.ContentPackage, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return mediaport.ContentPackage{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO media_content_package_versions(package_id,version,name,content_text,enabled,created_by,created_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, packageID, version, c.Name, c.ContentText, c.Enabled, c.Actor, now); err != nil {
		return mediaport.ContentPackage{}, err
	}
	refs := make([]mediaport.ContentRef, len(c.Refs))
	for i, ref := range c.Refs {
		source, err := r.captureOne(ctx, mediaport.GroupOpsMaterialReference{Kind: ref.Kind, ID: ref.ID}, true)
		if err != nil {
			return mediaport.ContentPackage{}, err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO media_content_package_version_refs(package_id,version,position,material_kind,material_id,source_digest) VALUES($1,$2,$3,$4,$5,$6)`, packageID, version, i, ref.Kind, ref.ID, source.SourceDigest); err != nil {
			return mediaport.ContentPackage{}, err
		}
		// Historical package versions are immutable material consumers. Record
		// an opaque Media-owned protection fact in the same UoW so an image,
		// attachment, card, or invite cannot be destructively removed while a
		// persisted content version still snapshots it.
		if _, err = tx.Exec(ctx, `INSERT INTO media_references(material_kind,material_id,owner,reference_digest) VALUES($1,$2,'media.content_package',$3) ON CONFLICT DO NOTHING`, ref.Kind, ref.ID, contentPackageReferenceDigest(packageID, version, i, ref)); err != nil {
			return mediaport.ContentPackage{}, err
		}
		refs[i] = ref
	}
	return mediaport.ContentPackage{ID: packageID, Name: c.Name, ContentText: c.ContentText, Enabled: c.Enabled, Version: version, Refs: refs}, nil
}

func contentPackageReferenceDigest(packageID, version int64, position int, ref mediaport.ContentRef) string {
	sum := sha256.Sum256([]byte("media.content_package:" + strconv.FormatInt(packageID, 10) + ":" + strconv.FormatInt(version, 10) + ":" + strconv.Itoa(position) + ":" + ref.Kind + ":" + strconv.FormatInt(ref.ID, 10)))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func (r *Repository) Bind(ctx context.Context, c mediaport.DeliveryBindingCommand, now time.Time) (mediaport.DeliveryBinding, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return mediaport.DeliveryBinding{}, err
	}
	var packageEnabled bool
	if err = tx.QueryRow(ctx, `SELECT enabled FROM media_content_packages WHERE id=$1 FOR UPDATE`, c.PackageID).Scan(&packageEnabled); errors.Is(err, pgx.ErrNoRows) || !packageEnabled {
		return mediaport.DeliveryBinding{}, ErrConflict
	}
	if err != nil {
		return mediaport.DeliveryBinding{}, err
	}
	ok, err := r.Eligible(ctx, "group_invite", c.GroupInviteID)
	if err != nil || !ok {
		return mediaport.DeliveryBinding{}, ErrConflict
	}
	var out mediaport.DeliveryBinding
	err = tx.QueryRow(ctx, `INSERT INTO media_content_delivery_bindings(campaign_code,plan_id,package_id,group_invite_id,version,created_by,updated_by,created_at,updated_at)
VALUES($1,$2,$3,$4,1,$5,$5,$6,$6)
ON CONFLICT(campaign_code,plan_id) DO UPDATE SET package_id=EXCLUDED.package_id,group_invite_id=EXCLUDED.group_invite_id,version=media_content_delivery_bindings.version+1,updated_by=EXCLUDED.updated_by,updated_at=EXCLUDED.updated_at
WHERE ($7=0 OR media_content_delivery_bindings.version=$7)
RETURNING id,campaign_code,plan_id,package_id,group_invite_id,version`, c.CampaignCode, c.PlanID, c.PackageID, c.GroupInviteID, c.Actor, now, c.ExpectedVersion).Scan(&out.ID, &out.CampaignCode, &out.PlanID, &out.PackageID, &out.GroupInviteID, &out.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return mediaport.DeliveryBinding{}, ErrConflict
	}
	return out, err
}

func (r *Repository) GetBinding(ctx context.Context, campaignCode, planID string) (mediaport.DeliveryBinding, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return mediaport.DeliveryBinding{}, err
	}
	var out mediaport.DeliveryBinding
	err = tx.QueryRow(ctx, `SELECT id,campaign_code,plan_id,package_id,group_invite_id,version FROM media_content_delivery_bindings WHERE campaign_code=$1 AND plan_id=$2`, campaignCode, planID).Scan(&out.ID, &out.CampaignCode, &out.PlanID, &out.PackageID, &out.GroupInviteID, &out.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return mediaport.DeliveryBinding{}, ErrNotFound
	}
	return out, err
}

// CaptureGroupOpsMaterialSources must be called by its consumer inside one
// UoW. It locks each enabled Media record and reads content-derived digests;
// it never manufactures a digest from just kind/id.
func (r *Repository) CaptureGroupOpsMaterialSources(ctx context.Context, plan mediaport.GroupOpsMaterialPlan) (mediaport.GroupOpsMaterialSourceSnapshot, error) {
	if r == nil || mediaport.ValidateGroupOpsMaterialPlan(plan) != nil {
		return mediaport.GroupOpsMaterialSourceSnapshot{}, ErrInvalid
	}
	if _, err := platformpostgres.RequireTransaction(ctx); err != nil {
		return mediaport.GroupOpsMaterialSourceSnapshot{}, err
	}
	out := mediaport.GroupOpsMaterialSourceSnapshot{SchemaVersion: 1, References: make([]mediaport.GroupOpsMaterialSourceReference, len(plan.References))}
	for i, reference := range plan.References {
		item, err := r.captureOne(ctx, reference, true)
		if err != nil {
			return mediaport.GroupOpsMaterialSourceSnapshot{}, err
		}
		out.References[i] = item
	}
	if err := mediaport.ValidateGroupOpsMaterialSourceSnapshot(out); err != nil {
		return mediaport.GroupOpsMaterialSourceSnapshot{}, ErrConflict
	}
	return out, nil
}

func (r *Repository) captureOne(ctx context.Context, reference mediaport.GroupOpsMaterialReference, lock bool) (mediaport.GroupOpsMaterialSourceReference, error) {
	if reference.ID < 1 || !mediaKind(reference.Kind) {
		return mediaport.GroupOpsMaterialSourceReference{}, ErrInvalid
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return mediaport.GroupOpsMaterialSourceReference{}, err
	}
	suffix := ""
	if lock {
		suffix = " FOR UPDATE"
	}
	out := mediaport.GroupOpsMaterialSourceReference{Reference: reference}
	switch reference.Kind {
	case "image":
		var enabled bool
		err = tx.QueryRow(ctx, `SELECT blob_digest,enabled FROM media_images WHERE id=$1`+suffix, reference.ID).Scan(&out.SourceDigest, &enabled)
		if errors.Is(err, pgx.ErrNoRows) || !enabled {
			return mediaport.GroupOpsMaterialSourceReference{}, ErrNotFound
		}
	case "attachment":
		var enabled bool
		err = tx.QueryRow(ctx, `SELECT blob_digest,enabled FROM media_attachments WHERE id=$1`+suffix, reference.ID).Scan(&out.SourceDigest, &enabled)
		if errors.Is(err, pgx.ErrNoRows) || !enabled {
			return mediaport.GroupOpsMaterialSourceReference{}, ErrNotFound
		}
	case "miniprogram":
		var appID, page, title, thumbDigest string
		var thumbID int64
		var enabled bool
		err = tx.QueryRow(ctx, `SELECT m.app_id,m.page_path,m.title,m.thumb_image_id,m.enabled,i.blob_digest FROM media_miniprograms m JOIN media_images i ON i.id=m.thumb_image_id WHERE m.id=$1 AND i.enabled`+suffix, reference.ID).Scan(&appID, &page, &title, &thumbID, &enabled, &thumbDigest)
		if errors.Is(err, pgx.ErrNoRows) || !enabled {
			return mediaport.GroupOpsMaterialSourceReference{}, ErrNotFound
		}
		out.ThumbnailImageID, out.ThumbnailSourceDigest = thumbID, thumbDigest
		out.ProviderFields = mediaport.GroupOpsProviderReadyAttachment{MsgType: "miniprogram", AppID: appID, PagePath: page, Title: title}
		out.SourceDigest = canonicalSourceDigest(struct {
			AppID, PagePath, Title, ThumbDigest string
			ThumbID                             int64
		}{appID, page, title, thumbDigest, thumbID})
	case "group_invite":
		var title, description, url string
		var coverID *int64
		var coverDigest *string
		var enabled bool
		var archived *time.Time
		// Lock the invite row first. A generic FOR UPDATE on the previous
		// LEFT JOIN is rejected by PostgreSQL because the nullable side cannot
		// be locked; the cover image is locked explicitly below.
		err = tx.QueryRow(ctx, `SELECT title,description,join_url,cover_image_id,enabled,archived_at FROM media_group_invites WHERE id=$1`+suffix, reference.ID).Scan(&title, &description, &url, &coverID, &enabled, &archived)
		if errors.Is(err, pgx.ErrNoRows) {
			return mediaport.GroupOpsMaterialSourceReference{}, ErrNotFound
		}
		if err != nil {
			return mediaport.GroupOpsMaterialSourceReference{}, err
		}
		if !enabled || archived != nil {
			return mediaport.GroupOpsMaterialSourceReference{}, ErrNotFound
		}
		if coverID != nil {
			var imageEnabled bool
			var imageDigest string
			imageSuffix := ""
			if lock {
				imageSuffix = " FOR UPDATE"
			}
			err = tx.QueryRow(ctx, `SELECT blob_digest,enabled FROM media_images WHERE id=$1`+imageSuffix, *coverID).Scan(&imageDigest, &imageEnabled)
			if errors.Is(err, pgx.ErrNoRows) || !imageEnabled {
				return mediaport.GroupOpsMaterialSourceReference{}, ErrNotFound
			}
			if err != nil {
				return mediaport.GroupOpsMaterialSourceReference{}, err
			}
			if imageDigest == "" {
				return mediaport.GroupOpsMaterialSourceReference{}, ErrConflict
			}
			coverDigest = &imageDigest
			out.ThumbnailImageID = *coverID
			out.ThumbnailSourceDigest = *coverDigest
		}
		out.ProviderFields = mediaport.GroupOpsProviderReadyAttachment{MsgType: "link", Title: title, URL: url, Description: description}
		out.SourceDigest = canonicalSourceDigest(struct {
			Title, Description, URL string
			CoverID                 int64
			CoverDigest             string
		}{title, description, url, out.ThumbnailImageID, out.ThumbnailSourceDigest})
	}
	if err != nil {
		return mediaport.GroupOpsMaterialSourceReference{}, err
	}
	if !validDigest(out.SourceDigest) {
		return mediaport.GroupOpsMaterialSourceReference{}, ErrConflict
	}
	return out, nil
}

func canonicalSourceDigest(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func mapContentError(err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "duplicate key") {
		return ErrConflict
	}
	return err
}

func (r *Repository) Initiate(ctx context.Context, c mediaport.AttachmentUploadInitiateCommand, digestValue [32]byte, now time.Time) (int64, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return 0, err
	}
	if c.FileName == "" || strings.TrimSpace(c.FileName) != c.FileName || c.Name == "" || c.Size < 1 || c.Size > domain.MaxAttachmentBytes {
		return 0, ErrInvalid
	}
	keyDigest := sha256.Sum256([]byte(c.IdempotencyKey))
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO media_attachment_uploads(actor_admin_user_id,idempotency_key_digest,file_name,name,description,expected_size,expected_digest,enabled,expires_at,created_at)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING id`, c.Actor, "sha256:"+hex.EncodeToString(keyDigest[:]), c.FileName, c.Name, c.Description, c.Size, "sha256:"+hex.EncodeToString(digestValue[:]), c.Enabled, now.Add(time.Hour), now).Scan(&id)
	if err != nil {
		return 0, mapContentError(err)
	}
	return id, nil
}

func (r *Repository) PutPart(ctx context.Context, c mediaport.AttachmentUploadPartCommand, digestValue [32]byte, _ time.Time) (bool, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return false, err
	}
	if c.PartNumber < 1 || len(c.Content) < 1 || len(c.Content) > 1<<20 || sha256.Sum256(c.Content) != digestValue {
		return false, ErrInvalid
	}
	var expectedSize int64
	var completed *int64
	var expires time.Time
	if err = tx.QueryRow(ctx, `SELECT expected_size,completed_attachment_id,expires_at FROM media_attachment_uploads WHERE id=$1 AND actor_admin_user_id=$2 FOR UPDATE`, c.UploadID, c.Actor).Scan(&expectedSize, &completed, &expires); errors.Is(err, pgx.ErrNoRows) {
		return false, ErrNotFound
	}
	if err != nil {
		return false, err
	}
	if completed != nil || !expires.After(time.Now().UTC()) {
		return false, ErrConflict
	}
	var count int
	var stored int64
	if err = tx.QueryRow(ctx, `SELECT count(*),COALESCE(sum(octet_length(content)),0) FROM media_attachment_upload_parts WHERE upload_id=$1`, c.UploadID).Scan(&count, &stored); err != nil {
		return false, err
	}
	if count >= 1024 || stored+int64(len(c.Content)) > expectedSize {
		return false, ErrInvalid
	}
	digestText := "sha256:" + hex.EncodeToString(digestValue[:])
	tag, err := tx.Exec(ctx, `INSERT INTO media_attachment_upload_parts(upload_id,part_number,digest,content) VALUES($1,$2,$3,$4) ON CONFLICT(upload_id,part_number) DO NOTHING`, c.UploadID, c.PartNumber, digestText, c.Content)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		var existing string
		if err = tx.QueryRow(ctx, `SELECT digest FROM media_attachment_upload_parts WHERE upload_id=$1 AND part_number=$2`, c.UploadID, c.PartNumber).Scan(&existing); err != nil || existing != digestText {
			return false, ErrConflict
		}
	}
	return true, nil
}

func (r *Repository) Complete(ctx context.Context, c mediaport.AttachmentUploadCompleteCommand, now time.Time) (int64, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return 0, err
	}
	var fileName, name, description, expectedDigest string
	var expectedSize int64
	var enabled bool
	var existing *int64
	err = tx.QueryRow(ctx, `SELECT file_name,name,description,expected_size,expected_digest,enabled,completed_attachment_id FROM media_attachment_uploads WHERE id=$1 AND actor_admin_user_id=$2 AND expires_at>clock_timestamp() FOR UPDATE`, c.UploadID, c.Actor).Scan(&fileName, &name, &description, &expectedSize, &expectedDigest, &enabled, &existing)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	if existing != nil {
		return 0, ErrConflict
	}
	rows, err := tx.Query(ctx, `SELECT part_number,digest,content FROM media_attachment_upload_parts WHERE upload_id=$1 ORDER BY part_number`, c.UploadID)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var joined bytes.Buffer
	expectedPart := int32(1)
	for rows.Next() {
		var part int32
		var digestText string
		var content []byte
		if err = rows.Scan(&part, &digestText, &content); err != nil {
			return 0, err
		}
		if part != expectedPart || bytesDigest(content) != digestText {
			return 0, ErrConflict
		}
		expectedPart++
		_, _ = joined.Write(content)
	}
	if err = rows.Err(); err != nil {
		return 0, err
	}
	content := joined.Bytes()
	if int64(len(content)) != expectedSize || bytesDigest(content) != expectedDigest {
		return 0, ErrConflict
	}
	if _, err = domain.InspectAttachment(fileName, "application/pdf", content); err != nil {
		return 0, ErrInvalid
	}
	if _, err = tx.Exec(ctx, `INSERT INTO media_blobs(digest,mime_type,byte_size,content) VALUES($1,'application/pdf',$2,$3) ON CONFLICT(digest) DO NOTHING`, expectedDigest, len(content), content); err != nil {
		return 0, err
	}
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO media_attachments(blob_digest,file_name,name,description,tags,mime_type,byte_size,enabled,created_by,updated_by,created_at,updated_at) VALUES($1,$2,$3,$4,'[]'::jsonb,'application/pdf',$5,$6,$7,$7,$8,$8) RETURNING id`, expectedDigest, fileName, name, description, len(content), enabled, c.Actor, now).Scan(&id)
	if err != nil {
		return 0, err
	}
	if _, err = tx.Exec(ctx, `UPDATE media_attachment_uploads SET completed_attachment_id=$2 WHERE id=$1`, c.UploadID, id); err != nil {
		return 0, err
	}
	return id, nil
}
