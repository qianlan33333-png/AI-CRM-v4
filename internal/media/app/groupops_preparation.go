package app

import (
	"context"
	"errors"
	"time"

	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

var (
	ErrGroupOpsPreparationUnavailable = errors.New("media group ops preparation unavailable")
	ErrGroupOpsPreparationConflict    = errors.New("media group ops preparation conflict")
)

// GroupOpsMaterialPreparationStore is deliberately narrower than Media's
// general repository. The only write it exposes is the receipt/lease record;
// it must be called under the Unit of Work owned by this package.
type GroupOpsMaterialPreparationStore interface {
	RecordPreparedGroupOpsMaterialsWithin(context.Context, mediaport.GroupOpsMaterialPreparationCommand, time.Time) (mediaport.GroupOpsMaterialPreparationReceipt, error)
}

// GroupOpsMaterialPreparationWriter is the transaction-neutral adapter given
// to an approved Provider preparation implementation. Provider I/O must happen
// before calling it; this method then commits the Media receipt, audit, and
// outbox atomically. The disabled Provider has no reference to this writer.
type GroupOpsMaterialPreparationWriter struct {
	uow   platformport.UnitOfWork
	store GroupOpsMaterialPreparationStore
	now   func() time.Time
}

var _ mediaport.GroupOpsMaterialPreparationWriter = (*GroupOpsMaterialPreparationWriter)(nil)

func NewGroupOpsMaterialPreparationWriter(uow platformport.UnitOfWork, store GroupOpsMaterialPreparationStore) *GroupOpsMaterialPreparationWriter {
	return &GroupOpsMaterialPreparationWriter{uow: uow, store: store, now: time.Now}
}

func (writer *GroupOpsMaterialPreparationWriter) RecordPreparedGroupOpsMaterials(ctx context.Context, command mediaport.GroupOpsMaterialPreparationCommand) (receipt mediaport.GroupOpsMaterialPreparationReceipt, err error) {
	if writer == nil || writer.uow == nil || writer.store == nil || ctx == nil || mediaport.ValidateGroupOpsMaterialPreparationCommand(command) != nil {
		return receipt, mediaport.ErrInvalidGroupOpsMaterialPreparation
	}
	now := time.Now().UTC()
	if writer.now != nil {
		now = writer.now().UTC()
	}
	if now.IsZero() {
		return receipt, ErrGroupOpsPreparationUnavailable
	}
	err = writer.uow.Within(ctx, func(tx context.Context) error {
		var writeErr error
		receipt, writeErr = writer.store.RecordPreparedGroupOpsMaterialsWithin(tx, command, now)
		return writeErr
	})
	if err != nil {
		if errors.Is(err, mediaport.ErrGroupOpsMaterialPreparationConflict) {
			return mediaport.GroupOpsMaterialPreparationReceipt{}, ErrGroupOpsPreparationConflict
		}
		if errors.Is(err, mediaport.ErrInvalidGroupOpsMaterialPreparation) {
			return mediaport.GroupOpsMaterialPreparationReceipt{}, mediaport.ErrInvalidGroupOpsMaterialPreparation
		}
		return mediaport.GroupOpsMaterialPreparationReceipt{}, ErrGroupOpsPreparationUnavailable
	}
	return receipt, nil
}
