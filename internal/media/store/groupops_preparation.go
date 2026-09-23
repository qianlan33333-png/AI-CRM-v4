package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

var _ mediaport.GroupOpsMaterialPreparationReader = (*Repository)(nil)

// ReadPreparedGroupOpsMaterials reads only Media-owned preparation facts. It
// is deliberately transaction-bound: Group Ops calls it after the capturer
// has locked the mutable Media sources in the same acceptance UoW. A missing
// or expired row is an unavailable preparation, never a reason to derive a
// Provider media ID from a local kind/id pair.
func (r *Repository) ReadPreparedGroupOpsMaterials(ctx context.Context, sources mediaport.GroupOpsMaterialSourceSnapshot, requiredThrough time.Time) ([]mediaport.GroupOpsMaterialPreparation, error) {
	if r == nil || mediaport.ValidateGroupOpsMaterialSourceSnapshot(sources) != nil || requiredThrough.IsZero() {
		return nil, mediaport.ErrInvalidGroupOpsMaterialPreparation
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]mediaport.GroupOpsMaterialPreparation, 0, len(sources.References))
	for _, source := range sources.References {
		if source.Reference.Kind == "group_invite" {
			// A captured invite is already a complete provider link payload. It
			// must not be represented as a fake preparation/lease receipt.
			items = append(items, mediaport.GroupOpsMaterialPreparation{
				Reference:    source.Reference,
				SourceDigest: source.SourceDigest,
				Attachment:   source.ProviderFields,
			})
			continue
		}
		var (
			item       mediaport.GroupOpsMaterialPreparation
			kind       string
			attachment []byte
		)
		item.Reference = source.Reference
		err = tx.QueryRow(ctx, `SELECT material_kind,source_digest,receipt_digest,ready_until,attachment
FROM media_group_ops_preparation_items
WHERE material_kind=$1 AND material_id=$2 AND source_digest=$3 AND ready_until>$4
ORDER BY ready_until DESC,preparation_receipt_id DESC
LIMIT 1`, source.Reference.Kind, source.Reference.ID, source.SourceDigest, requiredThrough).Scan(&kind, &item.SourceDigest, &item.ReceiptDigest, &item.ReadyUntil, &attachment)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		if err != nil {
			return nil, err
		}
		if kind != source.Reference.Kind || json.Unmarshal(attachment, &item.Attachment) != nil {
			return nil, ErrConflict
		}
		items = append(items, item)
	}
	if err := mediaport.ValidateGroupOpsMaterialPreparations(sources, items, requiredThrough); err != nil {
		return nil, ErrConflict
	}
	return items, nil
}

// RecordPreparedGroupOpsMaterialsWithin is the Media Store's transaction-
// bound write primitive. The public transaction-neutral writer in
// internal/media/app wraps this method in the Media Unit of Work, so a
// Provider network call can finish before this receipt/audit/outbox write
// starts and no database transaction is held across the call.
func (r *Repository) RecordPreparedGroupOpsMaterialsWithin(ctx context.Context, command mediaport.GroupOpsMaterialPreparationCommand, now time.Time) (mediaport.GroupOpsMaterialPreparationReceipt, error) {
	if r == nil || now.IsZero() {
		return mediaport.GroupOpsMaterialPreparationReceipt{}, mediaport.ErrInvalidGroupOpsMaterialPreparation
	}
	if err := mediaport.ValidateGroupOpsMaterialPreparationCommand(command); err != nil {
		return mediaport.GroupOpsMaterialPreparationReceipt{}, err
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return mediaport.GroupOpsMaterialPreparationReceipt{}, err
	}
	keyDigest := sha256.Sum256([]byte(command.IdempotencyKey))
	request := command
	request.IdempotencyKey = ""
	encoded, err := json.Marshal(request)
	if err != nil {
		return mediaport.GroupOpsMaterialPreparationReceipt{}, err
	}
	commandDigest := sha256.Sum256(encoded)
	keyText := digestText(keyDigest)
	commandText := digestText(commandDigest)
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO media_group_ops_preparation_receipts(actor_admin_user_id,idempotency_key_digest,command_digest,item_count,required_through,created_at)
VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(actor_admin_user_id,idempotency_key_digest) DO NOTHING RETURNING id`, command.Actor, keyDigest[:], commandDigest[:], len(command.Items), command.RequiredThrough, now).Scan(&id)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return mediaport.GroupOpsMaterialPreparationReceipt{}, err
	}
	if errors.Is(err, pgx.ErrNoRows) {
		var existing mediaport.GroupOpsMaterialPreparationReceipt
		var existingKey, existingCommand []byte
		if err = tx.QueryRow(ctx, `SELECT id,actor_admin_user_id,idempotency_key_digest,command_digest,item_count,created_at
FROM media_group_ops_preparation_receipts
WHERE actor_admin_user_id=$1 AND idempotency_key_digest=$2`, command.Actor, keyDigest[:]).Scan(&existing.ID, &existing.Actor, &existingKey, &existingCommand, &existing.ItemCount, &existing.CreatedAt); err != nil {
			return mediaport.GroupOpsMaterialPreparationReceipt{}, err
		}
		if len(existingKey) != sha256.Size || len(existingCommand) != sha256.Size || !bytesEqual(existingCommand, commandDigest[:]) {
			return mediaport.GroupOpsMaterialPreparationReceipt{}, mediaport.ErrGroupOpsMaterialPreparationConflict
		}
		existing.KeyDigest = digestTextBytes(existingKey)
		existing.CommandDigest = digestTextBytes(existingCommand)
		return existing, nil
	}
	preparedItemCount := 0
	for position, item := range command.Items {
		if item.Reference.Kind == "group_invite" {
			continue
		}
		preparedItemCount++
		attachment, marshalErr := json.Marshal(item.Attachment)
		if marshalErr != nil {
			return mediaport.GroupOpsMaterialPreparationReceipt{}, marshalErr
		}
		if _, err = tx.Exec(ctx, `INSERT INTO media_group_ops_preparation_items(preparation_receipt_id,position,material_kind,material_id,source_digest,receipt_digest,ready_until,attachment)
VALUES($1,$2,$3,$4,$5,$6,$7,$8::jsonb)`, id, position, item.Reference.Kind, item.Reference.ID, item.SourceDigest, item.ReceiptDigest, item.ReadyUntil, attachment); err != nil {
			return mediaport.GroupOpsMaterialPreparationReceipt{}, err
		}
	}
	payload, err := json.Marshal(map[string]any{"receipt_id": id, "item_count": preparedItemCount, "command_digest": commandText})
	if err != nil {
		return mediaport.GroupOpsMaterialPreparationReceipt{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO media_audit_events(event_type,resource_kind,resource_id,actor_admin_user_id,payload) VALUES('media.group_ops_material_prepared','group_ops_material_preparation',$1,$2,$3::jsonb)`, id, command.Actor, payload); err != nil {
		return mediaport.GroupOpsMaterialPreparationReceipt{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO media_outbox(event_type,aggregate_kind,aggregate_id,payload) VALUES('media.group_ops_material_prepared','group_ops_material_preparation',$1,$2::jsonb)`, id, payload); err != nil {
		return mediaport.GroupOpsMaterialPreparationReceipt{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE media_group_ops_preparation_receipts SET item_count=$2 WHERE id=$1`, id, preparedItemCount); err != nil {
		return mediaport.GroupOpsMaterialPreparationReceipt{}, err
	}
	return mediaport.GroupOpsMaterialPreparationReceipt{ID: id, Actor: command.Actor, KeyDigest: keyText, CommandDigest: commandText, ItemCount: preparedItemCount, CreatedAt: now}, nil
}

func digestText(value [sha256.Size]byte) string {
	return "sha256:" + hex.EncodeToString(value[:])
}

func digestTextBytes(value []byte) string {
	if len(value) != sha256.Size {
		return ""
	}
	var digest [sha256.Size]byte
	copy(digest[:], value)
	return digestText(digest)
}

func bytesEqual(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
