package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// MediaReference is an opaque Media-owned protection fact. Its owner and
// digest are sufficient to decide whether a material can be changed; Media
// never reads another domain's rows to expand a reference.
type MediaReference struct {
	Owner           string
	ReferenceDigest string
}

// ReferenceReader is the narrow read adapter destructive Media operations
// use. Other owners protect a material by recording an opaque ledger fact.
type ReferenceReader interface {
	ListMediaReferences(context.Context, string, int64) ([]MediaReference, error)
}

var _ mediaport.MaterialReferenceRegistrar = (*Repository)(nil)
var _ mediaport.LegacyMaterialMappingResolver = (*Repository)(nil)
var _ mediaport.LegacyMaterialMappingImporter = (*Repository)(nil)
var _ mediaport.ImageMetadataReader = (*Repository)(nil)
var _ mediaport.AttachmentMetadataReader = (*Repository)(nil)
var _ mediaport.MiniProgramMetadataReader = (*Repository)(nil)
var _ mediaport.GroupInviteMetadataReader = (*Repository)(nil)

const maxMaterialReferences = 100

func (r *Repository) ResolveLegacyMaterialMapping(ctx context.Context, reference mediaport.LegacyMaterialReference) (mediaport.LegacyMaterialMapping, bool, error) {
	if r == nil || !validLegacyMaterialReference(reference) {
		return mediaport.LegacyMaterialMapping{}, false, ErrInvalid
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return mediaport.LegacyMaterialMapping{}, false, err
	}
	var mapping mediaport.LegacyMaterialMapping
	mapping.Reference = reference
	err = tx.QueryRow(ctx, `SELECT material_kind,material_id,source_digest,source_record_digest
		FROM media_legacy_material_mappings
		WHERE source_system=$1 AND legacy_material_kind=$2 AND legacy_material_id=$3`, reference.SourceSystem, reference.MaterialKind, reference.LegacyID).Scan(
		&mapping.MaterialKind, &mapping.MaterialID, &mapping.SourceDigest, &mapping.SourceRecordDigest,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return mediaport.LegacyMaterialMapping{}, false, nil
	}
	if err != nil {
		return mediaport.LegacyMaterialMapping{}, false, err
	}
	if !mediaKind(mapping.MaterialKind) || mapping.MaterialID < 1 || !validDigest(mapping.SourceDigest) || !validDigest(mapping.SourceRecordDigest) {
		return mediaport.LegacyMaterialMapping{}, false, ErrConflict
	}
	return mapping, true, nil
}

func (r *Repository) ImportLegacyMaterialMapping(ctx context.Context, mapping mediaport.LegacyMaterialMapping, importedBy string) error {
	if r == nil || !validLegacyMaterialReference(mapping.Reference) || !mediaKind(mapping.MaterialKind) || mapping.MaterialID < 1 || !validDigest(mapping.SourceDigest) || !validDigest(mapping.SourceRecordDigest) || strings.TrimSpace(importedBy) != importedBy || importedBy == "" || len(importedBy) > 120 {
		return ErrInvalid
	}
	return r.withReferenceTransaction(ctx, func(txctx context.Context) error {
		captured, err := r.captureOne(txctx, mediaport.GroupOpsMaterialReference{Kind: mapping.MaterialKind, ID: mapping.MaterialID}, true)
		if err != nil {
			return err
		}
		if captured.SourceDigest != mapping.SourceDigest {
			return ErrConflict
		}
		tx, _ := platformpostgres.RequireTransaction(txctx)
		command, err := tx.Exec(txctx, `INSERT INTO media_legacy_material_mappings(source_system,legacy_material_kind,legacy_material_id,material_kind,material_id,source_digest,source_record_digest,imported_by)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(source_system,legacy_material_kind,legacy_material_id) DO NOTHING`, mapping.Reference.SourceSystem, mapping.Reference.MaterialKind, mapping.Reference.LegacyID, mapping.MaterialKind, mapping.MaterialID, mapping.SourceDigest, mapping.SourceRecordDigest, importedBy)
		if err != nil {
			return err
		}
		if command.RowsAffected() == 1 {
			return nil
		}
		current, found, err := r.ResolveLegacyMaterialMapping(txctx, mapping.Reference)
		if err != nil || !found {
			return ErrConflict
		}
		if current.MaterialKind != mapping.MaterialKind || current.MaterialID != mapping.MaterialID || current.SourceDigest != mapping.SourceDigest || current.SourceRecordDigest != mapping.SourceRecordDigest {
			return ErrConflict
		}
		return nil
	})
}

func referenceDigest(owner string, id int64) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%d", owner, id)))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func (r *Repository) ListMediaReferences(ctx context.Context, kind string, id int64) ([]MediaReference, error) {
	if r == nil || id < 1 || !mediaKind(kind) {
		return nil, ErrInvalid
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT owner,reference_digest FROM media_references WHERE material_kind=$1 AND material_id=$2 ORDER BY owner,reference_digest LIMIT $3`, kind, id, maxMaterialReferences+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	references := make([]MediaReference, 0)
	for rows.Next() {
		var reference MediaReference
		if err = rows.Scan(&reference.Owner, &reference.ReferenceDigest); err != nil {
			return nil, err
		}
		references = append(references, reference)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(references) > maxMaterialReferences {
		return nil, ErrReferences
	}
	return references, nil
}

func mediaKind(kind string) bool {
	return kind == "image" || kind == "attachment" || kind == "miniprogram" || kind == "group_invite"
}

func (r *Repository) RegisterMediaReference(ctx context.Context, reference mediaport.MaterialReference) error {
	if r == nil || !validMaterialReference(reference) {
		return ErrInvalid
	}
	return r.withReferenceTransaction(ctx, func(txctx context.Context) error {
		exists, err := lockMaterial(txctx, reference.MaterialKind, reference.MaterialID)
		if err != nil {
			return err
		}
		if !exists {
			return ErrNotFound
		}
		tx, _ := platformpostgres.RequireTransaction(txctx)
		_, err = tx.Exec(txctx, `INSERT INTO media_references(material_kind,material_id,owner,reference_digest) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, reference.MaterialKind, reference.MaterialID, reference.Owner, reference.ReferenceDigest)
		return err
	})
}

func (r *Repository) UnregisterMediaReference(ctx context.Context, reference mediaport.MaterialReference) error {
	if r == nil || !validMaterialReference(reference) {
		return ErrInvalid
	}
	return r.withReferenceTransaction(ctx, func(txctx context.Context) error {
		exists, err := lockMaterial(txctx, reference.MaterialKind, reference.MaterialID)
		if err != nil {
			return err
		}
		if !exists {
			return ErrNotFound
		}
		tx, _ := platformpostgres.RequireTransaction(txctx)
		_, err = tx.Exec(txctx, `DELETE FROM media_references WHERE material_kind=$1 AND material_id=$2 AND owner=$3 AND reference_digest=$4`, reference.MaterialKind, reference.MaterialID, reference.Owner, reference.ReferenceDigest)
		return err
	})
}

// withReferenceTransaction lets a future Media client record a protection
// fact atomically with its own Media transaction, while preserving a complete
// transaction for standalone callers. It never opens a nested transaction.
func (r *Repository) withReferenceTransaction(ctx context.Context, callback func(context.Context) error) error {
	if _, err := platformpostgres.RequireTransaction(ctx); err == nil {
		return callback(ctx)
	} else if !errors.Is(err, platformpostgres.ErrTransactionNeeded) {
		return err
	}
	return r.Within(ctx, callback)
}

func validMaterialReference(reference mediaport.MaterialReference) bool {
	return mediaKind(reference.MaterialKind) && reference.MaterialID > 0 && strings.TrimSpace(reference.Owner) == reference.Owner && len(reference.Owner) <= 120 && reference.Owner != "" && validDigest(reference.ReferenceDigest)
}

func validLegacyMaterialReference(reference mediaport.LegacyMaterialReference) bool {
	return mediaKind(reference.MaterialKind) && strings.TrimSpace(reference.SourceSystem) == reference.SourceSystem && reference.SourceSystem != "" && len(reference.SourceSystem) <= 80 && strings.TrimSpace(reference.LegacyID) == reference.LegacyID && reference.LegacyID != "" && len(reference.LegacyID) <= 128
}

func lockMaterial(ctx context.Context, kind string, id int64) (bool, error) {
	if !mediaKind(kind) || id < 1 {
		return false, ErrInvalid
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return false, err
	}
	table := map[string]string{"image": "media_images", "attachment": "media_attachments", "miniprogram": "media_miniprograms", "group_invite": "media_group_invites"}[kind]
	var found int
	err = tx.QueryRow(ctx, "SELECT 1 FROM "+table+" WHERE id=$1 FOR UPDATE", id).Scan(&found)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// ImageExists and AttachmentExists are Media-owned, transaction-bound facts
// for consumers persisting references. Disabled material is intentionally not
// eligible, and KEY SHARE prevents a concurrent destructive change through
// the committing caller UoW.
func (r *Repository) ImageExists(ctx context.Context, id int64) (bool, error) {
	return r.enabledMaterialExists(ctx, "media_images", id)
}

func (r *Repository) AttachmentExists(ctx context.Context, id int64) (bool, error) {
	return r.enabledMaterialExists(ctx, "media_attachments", id)
}

func (r *Repository) MiniProgramExists(ctx context.Context, id int64) (bool, error) {
	return r.enabledMaterialExists(ctx, "media_miniprograms", id)
}

func (r *Repository) GroupInviteExists(ctx context.Context, id int64) (bool, error) {
	return r.enabledMaterialExists(ctx, "media_group_invites", id)
}

func (r *Repository) enabledMaterialExists(ctx context.Context, table string, id int64) (bool, error) {
	if r == nil || id < 1 {
		return false, ErrInvalid
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return false, err
	}
	var found int
	where := "id=$1 AND enabled=true"
	if table == "media_group_invites" {
		where += " AND archived_at IS NULL"
	}
	err = tx.QueryRow(ctx, "SELECT 1 FROM "+table+" WHERE "+where+" FOR KEY SHARE", id).Scan(&found)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func replaceLocalImageReference(ctx context.Context, owner string, localID int64, oldImage, newImage *int64) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	ids := make([]int64, 0, 2)
	if oldImage != nil {
		ids = append(ids, *oldImage)
	}
	if newImage != nil && (oldImage == nil || *newImage != *oldImage) {
		ids = append(ids, *newImage)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, imageID := range ids {
		exists, err := lockMaterial(ctx, "image", imageID)
		if err != nil {
			return err
		}
		if !exists {
			return ErrNotFound
		}
	}
	digest := referenceDigest(owner, localID)
	if oldImage != nil {
		if _, err = tx.Exec(ctx, `DELETE FROM media_references WHERE material_kind='image' AND material_id=$1 AND owner=$2 AND reference_digest=$3`, *oldImage, owner, digest); err != nil {
			return err
		}
	}
	if newImage != nil {
		_, err = tx.Exec(ctx, `INSERT INTO media_references(material_kind,material_id,owner,reference_digest) VALUES('image',$1,$2,$3) ON CONFLICT DO NOTHING`, *newImage, owner, digest)
	}
	return err
}

func removeLocalImageReference(ctx context.Context, owner string, localID int64) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT material_id FROM media_references WHERE material_kind='image' AND owner=$1 AND reference_digest=$2 ORDER BY material_id LIMIT $3`, owner, referenceDigest(owner, localID), maxMaterialReferences+1)
	if err != nil {
		return err
	}
	imageIDs := make([]int64, 0)
	for rows.Next() {
		var imageID int64
		if err = rows.Scan(&imageID); err != nil {
			rows.Close()
			return err
		}
		imageIDs = append(imageIDs, imageID)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if len(imageIDs) > maxMaterialReferences {
		return ErrReferences
	}
	for _, imageID := range imageIDs {
		exists, err := lockMaterial(ctx, "image", imageID)
		if err != nil {
			return err
		}
		if !exists {
			return ErrNotFound
		}
	}
	_, err = tx.Exec(ctx, `DELETE FROM media_references WHERE material_kind='image' AND owner=$1 AND reference_digest=$2`, owner, referenceDigest(owner, localID))
	return err
}
