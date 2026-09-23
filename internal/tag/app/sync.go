package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	tagport "github.com/qianlan33333-png/AI-CRM-v3/internal/tag/port"
)

const syncAcceptedEvent = "tag.catalog_sync_accepted"

var (
	ErrInvalidSync    = errors.New("invalid tag catalog sync command")
	ErrSyncConflict   = errors.New("tag catalog sync idempotency command conflict")
	ErrSyncInProgress = tagport.ErrSyncInProgress
	ErrSyncFailed     = errors.New("accept tag catalog sync command")
)

// SyncService accepts a manual or due catalog refresh locally. It never calls
// WeCom: queueing is not Provider execution and is not reported as success.
type SyncService struct {
	uow      platformport.UnitOfWork
	receipts tagport.SyncReceiptStore
	events   tagport.EventAppender
	enqueuer tagport.SyncEnqueuer
	now      func() time.Time
}

func (service *SyncService) Status(ctx context.Context) (tagport.SyncStatus, error) {
	if ctx == nil || !service.ready() {
		return tagport.SyncStatus{}, ErrInvalidSync
	}
	var status tagport.SyncStatus
	err := service.uow.Within(ctx, func(tx context.Context) error {
		var err error
		status, err = service.receipts.LatestSync(tx)
		return err
	})
	if err != nil {
		return tagport.SyncStatus{}, errors.Join(ErrSyncFailed, err)
	}
	return status, nil
}

func NewSyncService(uow platformport.UnitOfWork, receipts tagport.SyncReceiptStore, events tagport.EventAppender, enqueuer tagport.SyncEnqueuer) *SyncService {
	return &SyncService{uow: uow, receipts: receipts, events: events, enqueuer: enqueuer, now: time.Now}
}

func (service *SyncService) Request(ctx context.Context, command tagport.SyncCommand) (tagport.SyncAcceptance, error) {
	return service.RequestWithCommitHook(ctx, command, nil)
}

// RequestWithCommitHook lets the composition root atomically join an
// outbound-owned effect acceptance without importing that implementation.
func (service *SyncService) RequestWithCommitHook(ctx context.Context, command tagport.SyncCommand, hook func(context.Context, tagport.SyncAcceptance, bool) error) (tagport.SyncAcceptance, error) {
	if ctx == nil || !validSyncCommand(command) || !service.ready() {
		return tagport.SyncAcceptance{}, ErrInvalidSync
	}
	var result tagport.SyncAcceptance
	err := service.uow.Within(ctx, func(tx context.Context) error {
		receipt, err := service.receipts.ReserveSync(tx, command)
		if err != nil {
			return err
		}
		if receipt.Command != command {
			return ErrSyncConflict
		}
		switch receipt.State {
		case tagport.SyncAccepted, tagport.SyncReceiptExecuted, tagport.SyncReceiptOutcomeUnknown, tagport.SyncReceiptRetryableFailed, tagport.SyncReceiptFinalFailed, tagport.SyncReceiptCancelled, tagport.SyncReceiptReconciled:
			acceptance, err := acceptanceFromReceipt(receipt)
			if err != nil {
				return err
			}
			result = acceptance
			if hook != nil {
				return hook(tx, acceptance, true)
			}
			return nil
		case tagport.SyncReserved:
			if receipt.ID <= 0 {
				return ErrSyncFailed
			}
		default:
			return ErrSyncFailed
		}

		now := service.now().UTC()
		if now.IsZero() {
			return ErrSyncFailed
		}
		payload, err := json.Marshal(struct {
			ReceiptID int64            `json:"receipt_id"`
			Actor     int64            `json:"actor"`
			Kind      tagport.SyncKind `json:"kind"`
			TraceID   string           `json:"trace_id,omitempty"`
		}{ReceiptID: receipt.ID, Actor: command.Actor, Kind: command.Kind, TraceID: command.TraceID})
		if err != nil {
			return errors.Join(ErrSyncFailed, err)
		}
		eventID, err := service.events.Append(tx, tagport.Event{Type: syncAcceptedEvent, Payload: []byte(payload), OccurredAt: now, IdempotencyKey: syncEventKey(command)})
		if err != nil || eventID <= 0 {
			return errors.Join(ErrSyncFailed, err)
		}
		effect, err := service.enqueuer.EnqueueSync(tx, tagport.SyncJob{ReceiptID: receipt.ID, Actor: command.Actor, IdempotencyKey: command.IdempotencyKey, Kind: command.Kind, TraceID: command.TraceID})
		if err != nil || effect.QueueJobID <= 0 || effect.EffectRef == "" || effect.AcceptReceiptID == "" {
			return errors.Join(ErrSyncFailed, err)
		}
		accepted, err := service.receipts.AcceptSync(tx, receipt.ID, eventID, effect)
		if err != nil {
			return err
		}
		result, err = acceptanceFromReceipt(accepted)
		if err != nil {
			return err
		}
		if hook != nil {
			return hook(tx, result, false)
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrInvalidSync) || errors.Is(err, ErrSyncConflict) || errors.Is(err, ErrSyncInProgress) {
			return tagport.SyncAcceptance{}, err
		}
		return tagport.SyncAcceptance{}, errors.Join(ErrSyncFailed, err)
	}
	return result, nil
}

// SyncCanAutoRetry makes the boundary explicit for the eventual worker. Any
// attempted/unknown state requires reconciliation with the original key.
func SyncCanAutoRetry(state tagport.SyncState) bool { return state == tagport.SyncQueued }

func validSyncCommand(command tagport.SyncCommand) bool {
	return command.Actor > 0 && validSyncText(command.IdempotencyKey, 1, 200) && validSyncText(command.TraceID, 0, 200) &&
		(command.Kind == tagport.SyncManual || command.Kind == tagport.SyncDue)
}

func validSyncText(value string, minimum, maximum int) bool {
	return len(value) >= minimum && len(value) <= maximum && strings.TrimSpace(value) == value
}

func acceptanceFromReceipt(receipt tagport.SyncReceipt) (tagport.SyncAcceptance, error) {
	// ReserveSync validates/reconciles the full caller command before this
	// point.  Receipts retain only a digest of an idempotency key, so requiring
	// the raw key here would make a legitimate committed receipt unreadable.
	if receipt.ID <= 0 || receipt.State == tagport.SyncReserved || receipt.EventID <= 0 || receipt.Effect.QueueJobID <= 0 || receipt.Effect.EffectID <= 0 || receipt.Effect.EffectRef == "" || receipt.Effect.EffectState != "queued" || receipt.Effect.AcceptReceiptID == "" || receipt.Effect.QueueReceiptID == "" {
		return tagport.SyncAcceptance{}, ErrSyncFailed
	}
	return tagport.SyncAcceptance{ReceiptID: receipt.ID, EventID: receipt.EventID, QueueJobID: receipt.Effect.QueueJobID, EffectID: receipt.Effect.EffectRef, EffectState: receipt.Effect.EffectState, AcceptReceiptID: receipt.Effect.AcceptReceiptID, QueueReceiptID: receipt.Effect.QueueReceiptID, State: tagport.SyncQueued}, nil
}

func syncEventKey(command tagport.SyncCommand) string {
	digest := sha256.Sum256([]byte(fmt.Sprintf("%d\x00%s\x00%s", command.Actor, command.Kind, command.IdempotencyKey)))
	return "tag-catalog:sync-accepted:" + hex.EncodeToString(digest[:])
}

func (service *SyncService) ready() bool {
	return service != nil && !nilSyncDependency(service.uow) && !nilSyncDependency(service.receipts) &&
		!nilSyncDependency(service.events) && !nilSyncDependency(service.enqueuer) && service.now != nil
}

func nilSyncDependency(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
