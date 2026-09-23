package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	config "github.com/qianlan33333-png/AI-CRM-v3/internal/config"
	configport "github.com/qianlan33333-png/AI-CRM-v3/internal/config/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

type repository interface {
	LockKey(context.Context, configport.Key) error
	Get(context.Context, configport.Key) (configport.Setting, bool, error)
	InsertAudit(context.Context, []byte, configport.SetCommand, []byte, time.Time) (configport.Audit, bool, error)
	GetAuditByRequestID(context.Context, string) (configport.Audit, error)
	Upsert(context.Context, configport.SetCommand, []byte, time.Time) (configport.Setting, error)
}

// settingsBatchRepository is intentionally optional for older unit doubles,
// but the composed PostgreSQL repository must implement it. The receipt joins
// all fields in one HTTP form submission under a single idempotency scope.
type requestReceiptRepository interface {
	ReserveCommandRequest(context.Context, string, string, string, []byte, time.Time) (configport.RequestReceipt, bool, error)
	CompleteCommandRequest(context.Context, int64, time.Time) error
}

type Manager struct {
	uow    platformport.UnitOfWork
	repo   repository
	events configport.EventAppender
	now    func() time.Time
}

var _ configport.Service = (*Manager)(nil)

func NewManager(uow platformport.UnitOfWork, repo repository, events configport.EventAppender) *Manager {
	return &Manager{uow: uow, repo: repo, events: events, now: time.Now}
}

func (manager *Manager) Get(ctx context.Context, key configport.Key) (setting configport.Setting, err error) {
	if err = config.ValidateReadableSetting(key); err != nil {
		return configport.Setting{}, err
	}
	if err = manager.ready(); err != nil {
		return configport.Setting{}, err
	}
	err = manager.uow.Within(ctx, func(txCtx context.Context) error {
		var found bool
		setting, found, err = manager.repo.Get(txCtx, key)
		if err == nil && !found {
			err = configport.ErrSettingNotFound
		}
		return err
	})
	return setting, err
}

func (manager *Manager) Set(ctx context.Context, command configport.SetCommand) (setting configport.Setting, err error) {
	canonical, err := config.ValidateSetting(command.Key, command.Value)
	if err != nil {
		return configport.Setting{}, err
	}
	if !validMetadata(command.Actor) || !validMetadata(command.RequestID) {
		return configport.Setting{}, configport.ErrInvalidSetting
	}
	if err = manager.ready(); err != nil {
		return configport.Setting{}, err
	}
	updatedAt := manager.now().UTC()
	if updatedAt.IsZero() {
		return configport.Setting{}, fmt.Errorf("config manager clock is invalid")
	}
	err = manager.uow.Within(ctx, func(txCtx context.Context) error {
		if lockErr := manager.repo.LockKey(txCtx, command.Key); lockErr != nil {
			return lockErr
		}
		current, found, getErr := manager.repo.Get(txCtx, command.Key)
		if getErr != nil {
			return getErr
		}
		var oldValue []byte
		if found {
			oldValue = current.Value
		}
		audit, inserted, auditErr := manager.repo.InsertAudit(txCtx, oldValue, command, canonical, updatedAt)
		if auditErr != nil {
			return auditErr
		}
		if !inserted {
			audit, auditErr = manager.repo.GetAuditByRequestID(txCtx, command.RequestID)
			if auditErr != nil {
				return auditErr
			}
			if audit.Key != command.Key || audit.UpdatedBy != command.Actor || !bytes.Equal(audit.NewValue, canonical) {
				return configport.ErrIdempotencyConflict
			}
			setting = configport.Setting{
				Key: audit.Key, Value: audit.NewValue,
				UpdatedBy: audit.UpdatedBy, UpdatedAt: audit.UpdatedAt,
			}
			return nil
		}
		setting, err = manager.repo.Upsert(txCtx, command, canonical, updatedAt)
		if err != nil {
			return err
		}
		payload, marshalErr := json.Marshal(struct {
			AuditID int64          `json:"audit_id"`
			Key     configport.Key `json:"key"`
		}{AuditID: audit.ID, Key: command.Key})
		if marshalErr != nil {
			return marshalErr
		}
		_, err = manager.events.Append(txCtx, configport.Event{
			Type: "setting.updated", Payload: payload, OccurredAt: updatedAt,
			IdempotencyKey: "setting.updated:" + command.RequestID,
		})
		return err
	})
	return setting, err
}

// SetMany is the compatibility page's atomic multi-setting variant. It keeps
// every state row, idempotency receipt, audit, and outbox fact in one UoW;
// callers must not emulate a batch by repeatedly calling Set.
func (manager *Manager) SetMany(ctx context.Context, commands []configport.SetCommand) error {
	preparedCommands, err := manager.prepareMany(commands)
	if err != nil {
		return err
	}
	return manager.setPreparedMany(ctx, preparedCommands)
}

type preparedSettingCommand struct {
	command   configport.SetCommand
	canonical []byte
}

func (manager *Manager) prepareMany(commands []configport.SetCommand) ([]preparedSettingCommand, error) {
	if len(commands) == 0 || len(commands) > 4 || manager.ready() != nil {
		return nil, configport.ErrInvalidSetting
	}
	preparedCommands := make([]preparedSettingCommand, 0, len(commands))
	seen := make(map[configport.Key]struct{}, len(commands))
	for _, command := range commands {
		canonical, err := config.ValidateSetting(command.Key, command.Value)
		if err != nil {
			return nil, err
		}
		if !validMetadata(command.Actor) || !validMetadata(command.RequestID) {
			return nil, configport.ErrInvalidSetting
		}
		if _, exists := seen[command.Key]; exists {
			return nil, configport.ErrInvalidSetting
		}
		seen[command.Key] = struct{}{}
		preparedCommands = append(preparedCommands, preparedSettingCommand{command, canonical})
	}
	// The closed registry has at most four editable keys. A deterministic
	// lexical lock order prevents two form posts from deadlocking.
	sort.Slice(preparedCommands, func(i, j int) bool { return preparedCommands[i].command.Key < preparedCommands[j].command.Key })
	return preparedCommands, nil
}

func (manager *Manager) setPreparedMany(ctx context.Context, preparedCommands []preparedSettingCommand) error {
	now := manager.now().UTC()
	if now.IsZero() {
		return fmt.Errorf("config manager clock is invalid")
	}
	return manager.uow.Within(ctx, func(txCtx context.Context) error {
		for _, item := range preparedCommands {
			if err := manager.repo.LockKey(txCtx, item.command.Key); err != nil {
				return err
			}
		}
		for _, item := range preparedCommands {
			current, found, err := manager.repo.Get(txCtx, item.command.Key)
			if err != nil {
				return err
			}
			var old []byte
			if found {
				old = current.Value
			}
			audit, inserted, err := manager.repo.InsertAudit(txCtx, old, item.command, item.canonical, now)
			if err != nil {
				return err
			}
			if !inserted {
				if audit.Key != item.command.Key || audit.UpdatedBy != item.command.Actor || !bytes.Equal(audit.NewValue, item.canonical) {
					return configport.ErrIdempotencyConflict
				}
				continue
			}
			if _, err = manager.repo.Upsert(txCtx, item.command, item.canonical, now); err != nil {
				return err
			}
			payload, err := json.Marshal(struct {
				AuditID int64          `json:"audit_id"`
				Key     configport.Key `json:"key"`
			}{audit.ID, item.command.Key})
			if err != nil {
				return err
			}
			if _, err = manager.events.Append(txCtx, configport.Event{Type: "setting.updated", Payload: payload, OccurredAt: now, IdempotencyKey: "setting.updated:" + item.command.RequestID}); err != nil {
				return err
			}
		}
		return nil
	})
}

// SetManyWithRequest makes one browser idempotency key cover the entire
// submitted form.  The receipt, per-setting audit rows, state and outbox are
// committed together; a changed payload under the same key is a conflict.
func (manager *Manager) SetManyWithRequest(ctx context.Context, actor, requestID string, payload []byte, commands []configport.SetCommand) error {
	if !validMetadata(actor) || !validMetadata(requestID) || len(payload) == 0 {
		return configport.ErrInvalidSetting
	}
	prepared, err := manager.prepareMany(commands)
	if err != nil {
		return err
	}
	receipts, ok := manager.repo.(requestReceiptRepository)
	if !ok {
		return errors.New("config request receipt repository is required")
	}
	digest := sha256.Sum256(payload)
	now := manager.now().UTC()
	if now.IsZero() {
		return fmt.Errorf("config manager clock is invalid")
	}
	return manager.uow.Within(ctx, func(txCtx context.Context) error {
		receipt, owned, reserveErr := receipts.ReserveCommandRequest(txCtx, "app_settings.save", actor, requestID, digest[:], now)
		if reserveErr != nil {
			return reserveErr
		}
		if !owned {
			if !bytes.Equal(receipt.PayloadDigest, digest[:]) {
				return configport.ErrIdempotencyConflict
			}
			if receipt.State != "completed" {
				return errors.New("config request receipt is not complete")
			}
			return nil
		}
		for _, item := range prepared {
			if err := manager.repo.LockKey(txCtx, item.command.Key); err != nil {
				return err
			}
		}
		for _, item := range prepared {
			current, found, getErr := manager.repo.Get(txCtx, item.command.Key)
			if getErr != nil {
				return getErr
			}
			var old []byte
			if found {
				old = current.Value
			}
			audit, inserted, auditErr := manager.repo.InsertAudit(txCtx, old, item.command, item.canonical, now)
			if auditErr != nil {
				return auditErr
			}
			if !inserted {
				return configport.ErrIdempotencyConflict
			}
			if _, upsertErr := manager.repo.Upsert(txCtx, item.command, item.canonical, now); upsertErr != nil {
				return upsertErr
			}
			eventPayload, marshalErr := json.Marshal(struct {
				AuditID int64          `json:"audit_id"`
				Key     configport.Key `json:"key"`
			}{AuditID: audit.ID, Key: item.command.Key})
			if marshalErr != nil {
				return marshalErr
			}
			if _, appendErr := manager.events.Append(txCtx, configport.Event{Type: "setting.updated", Payload: eventPayload, OccurredAt: now, IdempotencyKey: "setting.updated:" + item.command.RequestID}); appendErr != nil {
				return appendErr
			}
		}
		return receipts.CompleteCommandRequest(txCtx, receipt.ID, now)
	})
}

func validMetadata(value string) bool {
	return value != "" && strings.TrimSpace(value) == value && len(value) <= 200
}

func (manager *Manager) ready() error {
	if manager == nil || manager.uow == nil || manager.repo == nil || manager.events == nil || manager.now == nil {
		return errors.New("config manager dependencies are required")
	}
	return nil
}
