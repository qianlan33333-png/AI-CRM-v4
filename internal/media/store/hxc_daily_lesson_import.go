package store

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/media/domain"
	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

const HXCDailyLessonSourceSystem = "hxc-daily-lessons"

var hxcDailyLessonUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type HXCDailyLessonImport struct {
	SourceID, Title, AppID, PagePath   string
	PNG                                []byte
	Width, Height                      int32
	Actor                              int64
	IdempotencyKey, SourceRecordDigest string
}

type HXCDailyLessonImportResult struct {
	ImageID, MiniProgramID int64
	Replayed               bool
}

// ImportHXCDailyLessonWithin owns the one-row import transaction. The caller
// supplies a transaction context so the image, mini-program, receipts,
// audit/outbox, references and immutable source mappings share one commit.
func (r *Repository) ImportHXCDailyLessonWithin(ctx context.Context, input HXCDailyLessonImport) (HXCDailyLessonImportResult, error) {
	result, err := r.InspectHXCDailyLessonWithin(ctx, input)
	if err != nil || result.Replayed {
		return result, err
	}
	result, err = r.createHXCDailyLessonTargets(ctx, input)
	if err != nil {
		return HXCDailyLessonImportResult{}, err
	}
	for _, mapping := range []mediaport.LegacyMaterialMapping{
		{Reference: mediaport.LegacyMaterialReference{SourceSystem: HXCDailyLessonSourceSystem, MaterialKind: "image", LegacyID: input.SourceID}, MaterialKind: "image", MaterialID: result.ImageID, SourceRecordDigest: input.SourceRecordDigest},
		{Reference: mediaport.LegacyMaterialReference{SourceSystem: HXCDailyLessonSourceSystem, MaterialKind: "miniprogram", LegacyID: input.SourceID}, MaterialKind: "miniprogram", MaterialID: result.MiniProgramID, SourceRecordDigest: input.SourceRecordDigest},
	} {
		captured, captureErr := r.captureOne(ctx, mediaport.GroupOpsMaterialReference{Kind: mapping.MaterialKind, ID: mapping.MaterialID}, true)
		if captureErr != nil {
			return HXCDailyLessonImportResult{}, captureErr
		}
		mapping.SourceDigest = captured.SourceDigest
		if err = r.ImportLegacyMaterialMapping(ctx, mapping, "hxc-daily-lessons:admin:"+strconv.FormatInt(input.Actor, 10)); err != nil {
			return HXCDailyLessonImportResult{}, err
		}
	}
	return result, nil
}

// InspectHXCDailyLessonWithin validates one frozen record and reports whether
// its exact immutable mapping already exists, without creating any rows.
func (r *Repository) InspectHXCDailyLessonWithin(ctx context.Context, input HXCDailyLessonImport) (HXCDailyLessonImportResult, error) {
	if r == nil || !hxcDailyLessonUUID.MatchString(input.SourceID) || input.Actor < 1 ||
		!validWebhookLessonMaterializationKey(input.IdempotencyKey) || !validDigest(input.SourceRecordDigest) ||
		input.AppID == "" || len(input.AppID) > 120 || input.PagePath == "" || len(input.PagePath) > 500 ||
		input.Title == "" || strings.TrimSpace(input.Title) != input.Title || !utf8.ValidString(input.Title) || len(input.Title) > 200 {
		return HXCDailyLessonImportResult{}, ErrInvalid
	}
	inspection, err := domain.Inspect("lesson-card.png", "image/png", input.PNG)
	if err != nil || inspection.MediaType != "image/png" || inspection.Width != input.Width || inspection.Height != input.Height {
		return HXCDailyLessonImportResult{}, ErrInvalid
	}
	if _, err = platformpostgres.RequireTransaction(ctx); err != nil {
		return HXCDailyLessonImportResult{}, err
	}

	imageMapping, imageFound, err := r.ResolveLegacyMaterialMapping(ctx, mediaport.LegacyMaterialReference{SourceSystem: HXCDailyLessonSourceSystem, MaterialKind: "image", LegacyID: input.SourceID})
	if err != nil {
		return HXCDailyLessonImportResult{}, err
	}
	miniMapping, miniFound, err := r.ResolveLegacyMaterialMapping(ctx, mediaport.LegacyMaterialReference{SourceSystem: HXCDailyLessonSourceSystem, MaterialKind: "miniprogram", LegacyID: input.SourceID})
	if err != nil {
		return HXCDailyLessonImportResult{}, err
	}
	if imageFound != miniFound {
		return HXCDailyLessonImportResult{}, ErrConflict
	}
	if imageFound {
		if imageMapping.SourceRecordDigest != input.SourceRecordDigest || miniMapping.SourceRecordDigest != input.SourceRecordDigest {
			return HXCDailyLessonImportResult{}, ErrConflict
		}
		if err = r.verifyHXCDailyLessonTargets(ctx, input, imageMapping, miniMapping); err != nil {
			return HXCDailyLessonImportResult{}, err
		}
		return HXCDailyLessonImportResult{ImageID: imageMapping.MaterialID, MiniProgramID: miniMapping.MaterialID, Replayed: true}, nil
	}
	return HXCDailyLessonImportResult{}, nil
}

func (r *Repository) createHXCDailyLessonTargets(ctx context.Context, input HXCDailyLessonImport) (HXCDailyLessonImportResult, error) {
	tx, _ := platformpostgres.RequireTransaction(ctx)
	command, err := json.Marshal(map[string]any{"source_id": input.SourceID, "title": input.Title, "appid": input.AppID, "page_path": input.PagePath, "cover_digest": bytesDigest(input.PNG), "source_record_digest": input.SourceRecordDigest})
	if err != nil {
		return HXCDailyLessonImportResult{}, err
	}
	replay, owned, err := r.reserve(ctx, "hxc.daily_lesson.import", "miniprogram", input.Actor, input.IdempotencyKey, string(command))
	if err != nil {
		return HXCDailyLessonImportResult{}, err
	}
	if !owned {
		var result HXCDailyLessonImportResult
		if json.Unmarshal(replay, &result) != nil || result.ImageID < 1 || result.MiniProgramID < 1 {
			return HXCDailyLessonImportResult{}, ErrConflict
		}
		return result, nil
	}

	blobDigest := bytesDigest(input.PNG)
	if _, err = tx.Exec(ctx, `INSERT INTO media_blobs(digest,mime_type,byte_size,content) VALUES($1,'image/png',$2,$3) ON CONFLICT(digest) DO NOTHING`, blobDigest, len(input.PNG), input.PNG); err != nil {
		return HXCDailyLessonImportResult{}, err
	}
	var result HXCDailyLessonImportResult
	err = tx.QueryRow(ctx, `INSERT INTO media_images(blob_digest,file_name,name,description,tags,category,mime_type,byte_size,width,height,enabled,created_by,updated_by)
		VALUES($1,$2,$3,'','日课','日课','image/png',$4,$5,$6,true,$7,$7) RETURNING id`, blobDigest, input.SourceID+".png", input.Title+"封面", len(input.PNG), input.Width, input.Height, input.Actor).Scan(&result.ImageID)
	if err != nil {
		return HXCDailyLessonImportResult{}, err
	}
	if err = r.acceptMaterialPreparationWithin(ctx, "image:"+strconv.FormatInt(result.ImageID, 10)); err != nil {
		return HXCDailyLessonImportResult{}, err
	}
	if err = appendHXCImportEvent(ctx, "media.hxc_daily_lesson_image_created", "image", result.ImageID, input.Actor, input.SourceID); err != nil {
		return HXCDailyLessonImportResult{}, err
	}

	err = tx.QueryRow(ctx, `INSERT INTO media_miniprograms(name,app_id,page_path,title,thumb_image_id,category,enabled,created_by,updated_by)
		VALUES($1,$2,$3,$1,$4,'日课',true,$5,$5) RETURNING id`, input.Title, input.AppID, input.PagePath, result.ImageID, input.Actor).Scan(&result.MiniProgramID)
	if err != nil {
		return HXCDailyLessonImportResult{}, err
	}
	if err = replaceLocalImageReference(ctx, "media.miniprogram.thumbnail", result.MiniProgramID, nil, &result.ImageID); err != nil {
		return HXCDailyLessonImportResult{}, err
	}
	if err = r.complete(ctx, "hxc.daily_lesson.import", "miniprogram", input.Actor, input.IdempotencyKey, result.MiniProgramID, result, "media.hxc_daily_lesson_miniprogram_created"); err != nil {
		return HXCDailyLessonImportResult{}, err
	}
	return result, nil
}

func appendHXCImportEvent(ctx context.Context, event, kind string, resourceID, actor int64, sourceID string) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]any{"resource_id": resourceID, "source_system": HXCDailyLessonSourceSystem, "source_id": sourceID})
	if _, err = tx.Exec(ctx, `INSERT INTO media_audit_events(event_type,resource_kind,resource_id,actor_admin_user_id,payload) VALUES($1,$2,$3,$4,$5::jsonb)`, event, kind, resourceID, actor, payload); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO media_outbox(event_type,aggregate_kind,aggregate_id,payload) VALUES($1,$2,$3,$4::jsonb)`, event, kind, resourceID, payload)
	return err
}

func (r *Repository) verifyHXCDailyLessonTargets(ctx context.Context, input HXCDailyLessonImport, imageMapping, miniMapping mediaport.LegacyMaterialMapping) error {
	for _, mapping := range []mediaport.LegacyMaterialMapping{imageMapping, miniMapping} {
		captured, err := r.captureOne(ctx, mediaport.GroupOpsMaterialReference{Kind: mapping.MaterialKind, ID: mapping.MaterialID}, true)
		if err != nil || captured.SourceDigest != mapping.SourceDigest {
			return ErrConflict
		}
	}
	tx, _ := platformpostgres.RequireTransaction(ctx)
	var appID, pagePath, title, category string
	var thumbID int64
	var enabled bool
	err := tx.QueryRow(ctx, `SELECT app_id,page_path,title,thumb_image_id,category,enabled FROM media_miniprograms WHERE id=$1`, miniMapping.MaterialID).Scan(&appID, &pagePath, &title, &thumbID, &category, &enabled)
	if errors.Is(err, pgx.ErrNoRows) || err != nil || appID != input.AppID || pagePath != input.PagePath || title != input.Title || thumbID != imageMapping.MaterialID || category != "日课" || !enabled {
		return ErrConflict
	}
	return nil
}
