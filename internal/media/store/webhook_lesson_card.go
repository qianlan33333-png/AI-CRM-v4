package store

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/media/domain"
	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// MaterializeWebhookMiniProgramWithin persists an already verified Media
// provider-read cover while the caller's UoW is open. The image, card,
// Media audit/outbox and any later Group Ops/EER writes therefore share one
// commit decision. It deliberately does not call Repository.Within.
func (r *Repository) MaterializeWebhookMiniProgramWithin(ctx context.Context, prepared mediaport.PreparedWebhookMiniProgram, command mediaport.WebhookMiniProgramMaterialization) (mediaport.GroupOpsMaterialReference, error) {
	if r == nil || command.Actor < 1 || !validWebhookLessonMaterializationKey(command.IdempotencyKey) || prepared.AppID == "" || prepared.Path == "" || prepared.Title == "" {
		return mediaport.GroupOpsMaterialReference{}, ErrInvalid
	}
	inspection, err := domain.Inspect("lesson-card.png", "image/png", prepared.PNG)
	if err != nil || inspection.MediaType != "image/png" || inspection.Width != prepared.Width || inspection.Height != prepared.Height {
		return mediaport.GroupOpsMaterialReference{}, ErrInvalid
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return mediaport.GroupOpsMaterialReference{}, err
	}
	imageCommand, err := json.Marshal(map[string]any{
		"appid": prepared.AppID, "path": prepared.Path, "title": prepared.Title,
		"content_digest": bytesDigest(prepared.PNG), "width": prepared.Width, "height": prepared.Height,
	})
	if err != nil {
		return mediaport.GroupOpsMaterialReference{}, err
	}
	imageReplay, imageOwned, err := r.reserve(ctx, "webhook.lesson_card.image.create", "image", command.Actor, command.IdempotencyKey, string(imageCommand))
	if err != nil {
		return mediaport.GroupOpsMaterialReference{}, err
	}
	var imageID int64
	if imageOwned {
		blobDigest := bytesDigest(prepared.PNG)
		if _, err = tx.Exec(ctx, "INSERT INTO media_blobs(digest,mime_type,byte_size,content) VALUES($1,$2,$3,$4) ON CONFLICT(digest) DO NOTHING", blobDigest, "image/png", len(prepared.PNG), prepared.PNG); err != nil {
			return mediaport.GroupOpsMaterialReference{}, err
		}
		var created, updatedAt time.Time
		err = tx.QueryRow(ctx, "INSERT INTO media_images(blob_digest,file_name,name,description,tags,category,mime_type,byte_size,width,height,enabled,created_by,updated_by) VALUES($1,'lesson-card.png','群运营日课课卡封面','','','groupops-webhook','image/png',$2,$3,$4,true,$5,$5) RETURNING id,created_at,updated_at", blobDigest, len(prepared.PNG), prepared.Width, prepared.Height, command.Actor).Scan(&imageID, &created, &updatedAt)
		if err != nil {
			return mediaport.GroupOpsMaterialReference{}, err
		}
		out := imageMap(imageID, "lesson-card.png", "群运营日课课卡封面", "", "", "groupops-webhook", "image/png", int64(len(prepared.PNG)), prepared.Width, prepared.Height, true, created, updatedAt)
		if err = r.acceptMaterialPreparationWithin(ctx, "image:"+strconv.FormatInt(imageID, 10)); err != nil {
			return mediaport.GroupOpsMaterialReference{}, err
		}
		if err = r.complete(ctx, "webhook.lesson_card.image.create", "image", command.Actor, command.IdempotencyKey, imageID, out, "media.webhook_lesson_card_image_created"); err != nil {
			return mediaport.GroupOpsMaterialReference{}, err
		}
	} else if imageID, err = webhookMaterialResultID(imageReplay); err != nil {
		return mediaport.GroupOpsMaterialReference{}, err
	}

	miniCommand, err := json.Marshal(map[string]any{
		"appid": prepared.AppID, "path": prepared.Path, "title": prepared.Title, "image_id": imageID,
	})
	if err != nil {
		return mediaport.GroupOpsMaterialReference{}, err
	}
	miniReplay, miniOwned, err := r.reserve(ctx, "webhook.lesson_card.miniprogram.create", "miniprogram", command.Actor, command.IdempotencyKey, string(miniCommand))
	if err != nil {
		return mediaport.GroupOpsMaterialReference{}, err
	}
	var miniID int64
	if miniOwned {
		var created, updatedAt time.Time
		err = tx.QueryRow(ctx, "INSERT INTO media_miniprograms(name,app_id,page_path,title,thumb_image_id,enabled,created_by,updated_by) VALUES('群运营日课课卡',$1,$2,$3,$4,true,$5,$5) RETURNING id,created_at,updated_at", prepared.AppID, prepared.Path, prepared.Title, imageID, command.Actor).Scan(&miniID, &created, &updatedAt)
		if err != nil {
			return mediaport.GroupOpsMaterialReference{}, err
		}
		if err = replaceLocalImageReference(ctx, "media.miniprogram.thumbnail", miniID, nil, &imageID); err != nil {
			return mediaport.GroupOpsMaterialReference{}, err
		}
		out := miniMap(miniID, "群运营日课课卡", prepared.AppID, prepared.Path, prepared.Title, &imageID, true, 1, command.Actor, command.Actor, created, updatedAt)
		if err = r.complete(ctx, "webhook.lesson_card.miniprogram.create", "miniprogram", command.Actor, command.IdempotencyKey, miniID, out, "media.webhook_lesson_card_miniprogram_created"); err != nil {
			return mediaport.GroupOpsMaterialReference{}, err
		}
	} else if miniID, err = webhookMaterialResultID(miniReplay); err != nil {
		return mediaport.GroupOpsMaterialReference{}, err
	}
	return mediaport.GroupOpsMaterialReference{Kind: "miniprogram", ID: miniID}, nil
}

func webhookMaterialResultID(raw json.RawMessage) (int64, error) {
	var value struct {
		ID int64 `json:"id"`
	}
	if json.Unmarshal(raw, &value) != nil || value.ID < 1 {
		return 0, ErrConflict
	}
	return value.ID, nil
}

func validWebhookLessonMaterializationKey(value string) bool {
	return len(value) >= 16 && len(value) <= 128 && strings.TrimSpace(value) == value
}
