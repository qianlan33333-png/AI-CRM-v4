// Package app implements the Admin Config and Jobs local control plane.
package app

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	adminopsport "github.com/qianlan33333-png/AI-CRM-v3/internal/adminops/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

var (
	ErrInvalidCommand    = errors.New("invalid admin ops command")
	ErrConflict          = errors.New("admin ops idempotency conflict")
	ErrUnavailable       = errors.New("admin ops operation unavailable")
	ErrSecretMaterial    = errors.New("secret material is forbidden")
	ErrVersionConflict   = errors.New("admin ops version conflict")
	ErrInvalidTransition = errors.New("admin ops state transition is invalid")
)

type NotificationSetting struct {
	Enabled         bool      `json:"enabled"`
	Channel         string    `json:"channel"`
	SecretRef       string    `json:"secret_ref"`
	SecretMask      string    `json:"secret_mask"`
	ValidationState string    `json:"validation_status"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type Service struct {
	uow   platformport.UnitOfWork
	store adminopsport.Repository
	now   func() time.Time
}

func NewService(uow platformport.UnitOfWork, store adminopsport.Repository) *Service {
	return &Service{uow: uow, store: store, now: time.Now}
}

type CredentialCommand struct {
	Kind        adminopsport.CredentialKind
	ClientID    string
	DisplayName string
	Metadata    map[string]any
	Actor       string
	RequestID   string
}

func (service *Service) CreateCredential(ctx context.Context, command CredentialCommand) (adminopsport.Credential, error) {
	if err := service.ready(command.Actor, command.RequestID); err != nil || !validCredentialCommand(command) {
		return adminopsport.Credential{}, ErrInvalidCommand
	}
	metadata, err := safeJSON(command.Metadata)
	if err != nil {
		return adminopsport.Credential{}, err
	}
	ref := newSecretRef(command.Kind, command.ClientID, command.RequestID)
	result, _, err := service.mutate(ctx, "credential.create", command.Actor, command.RequestID, struct {
		CredentialCommand
		Ref string
	}{command, ref}, func(tx context.Context, now time.Time) (json.RawMessage, error) {
		item, itemErr := service.store.CreateCredential(tx, adminopsport.Credential{Kind: command.Kind, ClientID: command.ClientID, DisplayName: command.DisplayName, State: stateForCreate(command.Kind), SecretRef: ref, SecretMask: maskSecretRef(ref), Metadata: metadata, CreatedBy: command.Actor, CreatedAt: now})
		if itemErr != nil {
			return nil, itemErr
		}
		return json.Marshal(item)
	})
	return decodeCredential(result, err)
}

func (service *Service) RotateCredential(ctx context.Context, kind adminopsport.CredentialKind, clientID, actor, requestID string) (adminopsport.Credential, error) {
	if err := service.ready(actor, requestID); err != nil || !validKind(kind) || !validCredentialIdentifier(clientID) {
		return adminopsport.Credential{}, ErrInvalidCommand
	}
	ref := newSecretRef(kind, clientID, requestID)
	result, _, err := service.mutate(ctx, "credential.rotate", actor, requestID, struct {
		Kind          adminopsport.CredentialKind
		ClientID, Ref string
	}{kind, clientID, ref}, func(tx context.Context, now time.Time) (json.RawMessage, error) {
		current, itemErr := service.store.GetCredential(tx, kind, clientID)
		if itemErr != nil {
			return nil, itemErr
		}
		current.State, current.SecretRef, current.SecretMask, current.UpdatedAt = "pending_activation", ref, maskSecretRef(ref), now
		item, itemErr := service.store.UpdateCredential(tx, current)
		if itemErr != nil {
			return nil, itemErr
		}
		return json.Marshal(item)
	})
	return decodeCredential(result, err)
}

func (service *Service) SetCredentialEnabled(ctx context.Context, kind adminopsport.CredentialKind, clientID string, enabled bool, secretRef, actor, requestID string) (adminopsport.Credential, error) {
	if err := service.ready(actor, requestID); err != nil || !validKind(kind) || !validCredentialIdentifier(clientID) {
		return adminopsport.Credential{}, ErrInvalidCommand
	}
	action := "credential.disable"
	if enabled {
		action = "credential.activate"
	}
	result, _, err := service.mutate(ctx, action, actor, requestID, struct {
		Kind      adminopsport.CredentialKind
		ClientID  string
		Enabled   bool
		SecretRef string
	}{kind, clientID, enabled, secretRef}, func(tx context.Context, now time.Time) (json.RawMessage, error) {
		current, itemErr := service.store.GetCredential(tx, kind, clientID)
		if itemErr != nil {
			return nil, itemErr
		}
		if enabled {
			if !validSecretRef(secretRef) || subtle.ConstantTimeCompare([]byte(secretRef), []byte(current.SecretRef)) != 1 {
				return nil, ErrSecretMaterial
			}
			current.State = "active"
		} else {
			current.State = "disabled"
		}
		current.UpdatedAt = now
		item, itemErr := service.store.UpdateCredential(tx, current)
		if itemErr != nil {
			return nil, itemErr
		}
		return json.Marshal(item)
	})
	return decodeCredential(result, err)
}

func (service *Service) UpdateCredential(ctx context.Context, command CredentialCommand) (adminopsport.Credential, error) {
	if err := service.ready(command.Actor, command.RequestID); err != nil || !validCredentialCommand(command) {
		return adminopsport.Credential{}, ErrInvalidCommand
	}
	metadata, err := safeJSON(command.Metadata)
	if err != nil {
		return adminopsport.Credential{}, err
	}
	result, _, err := service.mutate(ctx, "credential.update", command.Actor, command.RequestID, command, func(tx context.Context, now time.Time) (json.RawMessage, error) {
		current, itemErr := service.store.GetCredential(tx, command.Kind, command.ClientID)
		if itemErr != nil {
			return nil, itemErr
		}
		if current.State == "active" {
			return nil, ErrInvalidTransition
		}
		current.DisplayName, current.Metadata, current.UpdatedAt = command.DisplayName, metadata, now
		item, itemErr := service.store.UpdateCredential(tx, current)
		if itemErr != nil {
			return nil, itemErr
		}
		return json.Marshal(item)
	})
	return decodeCredential(result, err)
}

func (service *Service) ListCredentials(ctx context.Context) ([]adminopsport.Credential, error) {
	if !service.readyRead() {
		return nil, ErrUnavailable
	}
	var result []adminopsport.Credential
	err := service.uow.Within(ctx, func(tx context.Context) error {
		var itemErr error
		result, itemErr = service.store.ListCredentials(tx)
		return itemErr
	})
	return result, classify(err)
}

func (service *Service) GetCredential(ctx context.Context, kind adminopsport.CredentialKind, clientID string) (adminopsport.Credential, error) {
	if !service.readyRead() || !validKind(kind) || !validCredentialIdentifier(clientID) {
		return adminopsport.Credential{}, ErrInvalidCommand
	}
	var result adminopsport.Credential
	err := service.uow.Within(ctx, func(tx context.Context) error {
		var itemErr error
		result, itemErr = service.store.GetCredential(tx, kind, clientID)
		return itemErr
	})
	return result, classify(err)
}

func (service *Service) SetCategory(ctx context.Context, key string, enabled bool, settings map[string]any, actor, requestID string) (CategoryView, error) {
	if err := service.ready(actor, requestID); err != nil || !validCategory(key) {
		return CategoryView{}, ErrInvalidCommand
	}
	payload, err := canonicalCategorySettings(settings)
	if err != nil {
		return CategoryView{}, err
	}
	result, _, err := service.mutate(ctx, "category.set", actor, requestID, struct {
		Key      string
		Enabled  bool
		Settings json.RawMessage
	}{key, enabled, payload}, func(tx context.Context, now time.Time) (json.RawMessage, error) {
		item, itemErr := service.store.UpsertCategory(tx, adminopsport.Category{Key: key, Enabled: enabled, Settings: payload, UpdatedBy: actor, UpdatedAt: now})
		if itemErr != nil {
			return nil, itemErr
		}
		return json.Marshal(projectCategory(item))
	})
	return decodeCategoryResult(result, err)
}

func (service *Service) ListCategories(ctx context.Context) ([]CategoryView, error) {
	if !service.readyRead() {
		return nil, ErrUnavailable
	}
	var stored []adminopsport.Category
	err := service.uow.Within(ctx, func(tx context.Context) error {
		var itemErr error
		stored, itemErr = service.store.ListCategories(tx)
		return itemErr
	})
	if err != nil {
		return nil, classify(err)
	}
	result := make([]CategoryView, len(stored))
	for index, item := range stored {
		result[index] = projectCategory(item)
	}
	return result, nil
}

func (service *Service) GetCategory(ctx context.Context, key string) (CategoryView, error) {
	if !service.readyRead() || !validCategory(key) {
		return CategoryView{}, ErrInvalidCommand
	}
	var result adminopsport.Category
	err := service.uow.Within(ctx, func(tx context.Context) error {
		var itemErr error
		result, itemErr = service.store.GetCategory(tx, key)
		return itemErr
	})
	if err != nil {
		return CategoryView{}, classify(err)
	}
	return projectCategory(result), nil
}

type ReleaseCommand struct {
	Changes          map[string]any
	BasedOnReleaseID *int64
	Actor, RequestID string
}

func (service *Service) CreateRelease(ctx context.Context, command ReleaseCommand) (ReleaseView, error) {
	if err := service.ready(command.Actor, command.RequestID); err != nil || len(command.Changes) == 0 {
		return ReleaseView{}, ErrInvalidCommand
	}
	changes, err := canonicalReleaseChanges(command.Changes)
	if err != nil {
		return ReleaseView{}, err
	}
	checksum := sha256.Sum256(changes)
	result, _, err := service.mutate(ctx, "release.create", command.Actor, command.RequestID, command, func(tx context.Context, now time.Time) (json.RawMessage, error) {
		item, itemErr := service.store.CreateRelease(tx, adminopsport.Release{State: "draft", Changes: changes, Checksum: hex.EncodeToString(checksum[:]), BasedOnReleaseID: command.BasedOnReleaseID, CreatedBy: command.Actor, CreatedAt: now})
		if itemErr != nil {
			return nil, itemErr
		}
		return json.Marshal(projectRelease(item))
	})
	return decodeReleaseResult(result, err)
}

func (service *Service) ValidateRelease(ctx context.Context, id int64, actor, requestID string) (ReleaseView, error) {
	if err := service.ready(actor, requestID); err != nil || id < 1 {
		return ReleaseView{}, ErrInvalidCommand
	}
	result, _, err := service.mutate(ctx, "release.validate", actor, requestID, struct{ ID int64 }{id}, func(tx context.Context, now time.Time) (json.RawMessage, error) {
		current, itemErr := service.store.GetRelease(tx, id)
		if itemErr != nil {
			return nil, itemErr
		}
		if itemErr = validateStoredReleaseChanges(current.Changes); itemErr != nil {
			return nil, itemErr
		}
		item, itemErr := service.store.ValidateRelease(tx, id, now)
		if itemErr != nil {
			return nil, itemErr
		}
		return json.Marshal(projectRelease(item))
	})
	return decodeReleaseResult(result, err)
}

func (service *Service) PublishRelease(ctx context.Context, id int64, checksum, actor, requestID string) (ReleaseView, error) {
	if err := service.ready(actor, requestID); err != nil || id < 1 || len(checksum) != 64 {
		return ReleaseView{}, ErrInvalidCommand
	}
	result, _, err := service.mutate(ctx, "release.publish", actor, requestID, struct {
		ID       int64
		Checksum string
	}{id, checksum}, func(tx context.Context, now time.Time) (json.RawMessage, error) {
		current, itemErr := service.store.GetRelease(tx, id)
		if itemErr != nil {
			return nil, itemErr
		}
		if itemErr = validateStoredReleaseChanges(current.Changes); itemErr != nil {
			return nil, itemErr
		}
		item, itemErr := service.store.PublishRelease(tx, id, checksum, actor, now)
		if itemErr != nil {
			return nil, itemErr
		}
		return json.Marshal(projectRelease(item))
	})
	return decodeReleaseResult(result, err)
}

func (service *Service) RollbackRelease(ctx context.Context, id int64, actor, requestID string) (ReleaseView, error) {
	if err := service.ready(actor, requestID); err != nil || id < 1 {
		return ReleaseView{}, ErrInvalidCommand
	}
	result, _, err := service.mutate(ctx, "release.rollback", actor, requestID, struct{ ID int64 }{id}, func(tx context.Context, now time.Time) (json.RawMessage, error) {
		current, itemErr := service.store.GetRelease(tx, id)
		if itemErr != nil {
			return nil, itemErr
		}
		if itemErr = validateStoredReleaseChanges(current.Changes); itemErr != nil {
			return nil, itemErr
		}
		item, itemErr := service.store.RollbackRelease(tx, id, actor, now)
		if itemErr != nil {
			return nil, itemErr
		}
		return json.Marshal(projectRelease(item))
	})
	return decodeReleaseResult(result, err)
}

func (service *Service) GetRelease(ctx context.Context, id int64) (ReleaseView, error) {
	if !service.readyRead() || id < 1 {
		return ReleaseView{}, ErrInvalidCommand
	}
	var result adminopsport.Release
	err := service.uow.Within(ctx, func(tx context.Context) error {
		var itemErr error
		result, itemErr = service.store.GetRelease(tx, id)
		return itemErr
	})
	if err != nil {
		return ReleaseView{}, classify(err)
	}
	return projectRelease(result), nil
}

func (service *Service) ListReleases(ctx context.Context, limit int32) ([]ReleaseView, error) {
	if !service.readyRead() || limit < 1 || limit > 100 {
		return nil, ErrInvalidCommand
	}
	var stored []adminopsport.Release
	err := service.uow.Within(ctx, func(tx context.Context) error {
		var itemErr error
		stored, itemErr = service.store.ListReleases(tx, limit)
		return itemErr
	})
	if err != nil {
		return nil, classify(err)
	}
	result := make([]ReleaseView, len(stored))
	for index, item := range stored {
		result[index] = projectRelease(item)
	}
	return result, nil
}

type JobCommand struct {
	Kind, TargetRef, Actor, RequestID string
	Summary                           map[string]any
}

func (service *Service) EnqueueJob(ctx context.Context, command JobCommand) (adminopsport.Job, error) {
	if err := service.ready(command.Actor, command.RequestID); err != nil || !validJobKind(command.Kind) || !validTargetRef(command.TargetRef) {
		return adminopsport.Job{}, ErrInvalidCommand
	}
	summary, err := safeJSON(command.Summary)
	if err != nil {
		return adminopsport.Job{}, err
	}
	key := jobKey(command.Kind, command.Actor, command.RequestID)
	result, _, err := service.mutate(ctx, "job.enqueue."+command.Kind, command.Actor, command.RequestID, command, func(tx context.Context, now time.Time) (json.RawMessage, error) {
		item, itemErr := service.store.CreateJob(tx, adminopsport.Job{Key: key, Kind: command.Kind, State: "queued", TargetRef: command.TargetRef, Request: summary, RequestedBy: command.Actor, CreatedAt: now})
		if itemErr != nil {
			return nil, itemErr
		}
		return json.Marshal(item)
	})
	var job adminopsport.Job
	if err != nil {
		return job, classify(err)
	}
	return job, classify(json.Unmarshal(result, &job))
}

func (service *Service) GetJob(ctx context.Context, key string) (adminopsport.Job, error) {
	if !service.readyRead() || !strings.HasPrefix(key, "admjob_") {
		return adminopsport.Job{}, ErrInvalidCommand
	}
	var result adminopsport.Job
	err := service.uow.Within(ctx, func(tx context.Context) error {
		var itemErr error
		result, itemErr = service.store.GetJob(tx, key)
		return itemErr
	})
	return result, classify(err)
}

func (service *Service) ListJobs(ctx context.Context, kind, state string, limit int32) ([]adminopsport.Job, error) {
	if !service.readyRead() || (kind != "" && !validJobKind(kind)) || (state != "" && !validJobState(state)) || limit < 1 || limit > 100 {
		return nil, ErrInvalidCommand
	}
	var result []adminopsport.Job
	err := service.uow.Within(ctx, func(tx context.Context) error {
		var itemErr error
		result, itemErr = service.store.ListJobs(tx, kind, state, limit)
		return itemErr
	})
	return result, classify(err)
}

func (service *Service) CancelJob(ctx context.Context, key string, expectedVersion int64, actor, requestID string) (adminopsport.Job, error) {
	if err := service.ready(actor, requestID); err != nil || expectedVersion < 1 || !strings.HasPrefix(key, "admjob_") {
		return adminopsport.Job{}, ErrInvalidCommand
	}
	result, _, err := service.mutate(ctx, "job.cancel", actor, requestID, struct {
		Key     string
		Version int64
	}{key, expectedVersion}, func(tx context.Context, now time.Time) (json.RawMessage, error) {
		current, itemErr := service.store.GetJob(tx, key)
		if itemErr != nil {
			return nil, itemErr
		}
		if current.Version != expectedVersion {
			return nil, ErrVersionConflict
		}
		if current.State != "queued" && current.State != "running" {
			return nil, ErrInvalidTransition
		}
		current.State, current.UpdatedAt = "cancelled", now
		current.CompletedAt = &now
		item, itemErr := service.store.TransitionJob(tx, current)
		if itemErr != nil {
			return nil, itemErr
		}
		return json.Marshal(item)
	})
	var job adminopsport.Job
	if err != nil {
		return job, classify(err)
	}
	return job, classify(json.Unmarshal(result, &job))
}

// MarkOutcomeUnknown is worker-only and never retries an external outcome.
func (service *Service) MarkOutcomeUnknown(ctx context.Context, key, code string, expectedVersion int64) (adminopsport.Job, error) {
	if !service.readyRead() || expectedVersion < 1 || !strings.HasPrefix(key, "admjob_") || !validText(code, 120) {
		return adminopsport.Job{}, ErrInvalidCommand
	}
	var result adminopsport.Job
	err := service.uow.Within(ctx, func(tx context.Context) error {
		current, itemErr := service.store.GetJob(tx, key)
		if itemErr != nil {
			return itemErr
		}
		if current.Version != expectedVersion || current.State != "queued" {
			return ErrVersionConflict
		}
		now := service.now().UTC()
		current.State, current.FailureCode, current.UpdatedAt, current.CompletedAt = "outcome_unknown", code, now, &now
		result, itemErr = service.store.TransitionJob(tx, current)
		return itemErr
	})
	return result, classify(err)
}

func (service *Service) SaveFeishuNotification(ctx context.Context, enabled bool, secretRef, actor, requestID string) (NotificationSetting, error) {
	if err := service.ready(actor, requestID); err != nil || !validSecretRef(secretRef) {
		return NotificationSetting{}, ErrInvalidCommand
	}
	result, _, err := service.mutate(ctx, "notification.feishu.save", actor, requestID, struct {
		Enabled   bool
		SecretRef string
	}{enabled, secretRef}, func(tx context.Context, now time.Time) (json.RawMessage, error) {
		row, itemErr := service.store.UpsertNotification(tx, enabled, secretRef, maskSecretRef(secretRef), "unverified", actor, now)
		if itemErr != nil {
			return nil, itemErr
		}
		return json.Marshal(notificationFromRow(row))
	})
	var setting NotificationSetting
	if err != nil {
		return setting, classify(err)
	}
	return setting, classify(json.Unmarshal(result, &setting))
}

func (service *Service) GetFeishuNotification(ctx context.Context) (NotificationSetting, error) {
	if !service.readyRead() {
		return NotificationSetting{}, ErrUnavailable
	}
	var result NotificationSetting
	err := service.uow.Within(ctx, func(tx context.Context) error {
		row, itemErr := service.store.GetNotification(tx)
		if errors.Is(itemErr, adminopsport.ErrNotFound) {
			result = NotificationSetting{Channel: "feishu", ValidationState: "unconfigured"}
			return nil
		}
		if itemErr != nil {
			return itemErr
		}
		result = notificationFromRow(row)
		return nil
	})
	return result, classify(err)
}

func (service *Service) mutate(ctx context.Context, action, actor, requestID string, input any, work func(context.Context, time.Time) (json.RawMessage, error)) (json.RawMessage, bool, error) {
	if work == nil {
		return nil, false, ErrInvalidCommand
	}
	payload, err := safeJSON(input)
	if err != nil {
		return nil, false, err
	}
	keyDigest, payloadDigest := sha256.Sum256([]byte(requestID)), sha256.Sum256(payload)
	var result json.RawMessage
	replayed := false
	err = service.uow.Within(ctx, func(tx context.Context) error {
		now := service.now().UTC()
		receipt, owned, itemErr := service.store.ReserveReceipt(tx, action, actor, keyDigest[:], payloadDigest[:], now)
		if itemErr != nil {
			return itemErr
		}
		if !owned {
			if subtle.ConstantTimeCompare(receipt.PayloadDigest, payloadDigest[:]) != 1 {
				return ErrConflict
			}
			if receipt.State != "completed" || !json.Valid(receipt.Result) {
				return ErrUnavailable
			}
			result, replayed = append(json.RawMessage(nil), receipt.Result...), true
			return nil
		}
		result, itemErr = work(tx, now)
		if itemErr != nil {
			return itemErr
		}
		if !json.Valid(result) {
			return ErrUnavailable
		}
		_, itemErr = service.store.CompleteReceipt(tx, receipt.ID, result, now)
		return itemErr
	})
	return result, replayed, classify(err)
}

func (service *Service) ready(actor, requestID string) error {
	if !service.readyRead() || !validText(actor, 200) || !validText(requestID, 200) {
		return ErrInvalidCommand
	}
	return nil
}

func (service *Service) readyRead() bool {
	return service != nil && service.uow != nil && service.store != nil && service.now != nil
}

func stateForCreate(kind adminopsport.CredentialKind) string {
	if kind == adminopsport.CredentialDirectAPIKey {
		return "active"
	}
	return "pending_activation"
}

func validKind(kind adminopsport.CredentialKind) bool {
	return kind == adminopsport.CredentialDirectAPIKey || kind == adminopsport.CredentialAPIClient
}

func validCredentialCommand(command CredentialCommand) bool {
	return validKind(command.Kind) && validCredentialIdentifier(command.ClientID) && validText(command.DisplayName, 200)
}

func validCredentialIdentifier(value string) bool {
	return value != "." && value != ".." && validIdentifier(value, 120)
}

func validIdentifier(value string, limit int) bool {
	if !validText(value, limit) {
		return false
	}
	for _, item := range value {
		if !(item == '-' || item == '_' || item == '.' || item >= 'a' && item <= 'z' || item >= 'A' && item <= 'Z' || item >= '0' && item <= '9') {
			return false
		}
	}
	return true
}

func validText(value string, limit int) bool {
	return value != "" && strings.TrimSpace(value) == value && len(value) <= limit
}

func validCategory(value string) bool {
	return validIdentifier(value, 80) && value[0] >= 'a' && value[0] <= 'z'
}

func validJobKind(value string) bool {
	switch value {
	case "archive_sync", "message_batch_ack", "feishu_webhook_validate", "feishu_hourly_report":
		return true
	}
	return false
}

func validJobState(value string) bool {
	switch value {
	case "queued", "running", "completed", "failed", "cancelled", "outcome_unknown", "retired":
		return true
	}
	return false
}

func validTargetRef(value string) bool {
	return validText(value, 240) && !strings.Contains(value, "\n") && !strings.Contains(strings.ToLower(value), "password")
}

func validSecretRef(value string) bool {
	secretStore := strings.HasPrefix(value, "secret://") && len(value) > len("secret://")
	legacyReference := strings.HasPrefix(value, "secretref:") && len(value) > len("secretref:")
	return (secretStore || legacyReference) && validText(value, 250) && !strings.ContainsAny(value, "?&# ")
}

func newSecretRef(kind adminopsport.CredentialKind, clientID, requestID string) string {
	digest := sha256.Sum256([]byte(string(kind) + ":" + clientID + ":" + requestID))
	return "secret://adminops/" + string(kind) + "/" + clientID + "/" + hex.EncodeToString(digest[:8])
}

func maskSecretRef(value string) string {
	if len(value) < 8 {
		return "masked"
	}
	return "masked:…" + value[len(value)-6:]
}

func jobKey(kind, actor, requestID string) string {
	digest := sha256.Sum256([]byte(kind + ":" + actor + ":" + requestID))
	return "admjob_" + hex.EncodeToString(digest[:16])
}

func safeJSON(value any) (json.RawMessage, error) {
	if value == nil {
		return json.RawMessage("{}"), nil
	}
	encoded, err := json.Marshal(value)
	if err != nil || !json.Valid(encoded) || len(encoded) > 64<<10 {
		return nil, ErrInvalidCommand
	}
	var decoded any
	if err := json.Unmarshal(encoded, &decoded); err != nil || containsSecretMaterial(decoded) {
		return nil, ErrSecretMaterial
	}
	return encoded, nil
}

func containsSecretMaterial(value any) bool {
	switch item := value.(type) {
	case map[string]any:
		for key, nested := range item {
			lower := strings.ToLower(key)
			isReference := strings.HasSuffix(lower, "_ref") || strings.HasSuffix(lower, "ref")
			isSensitive := strings.Contains(lower, "secret") || strings.Contains(lower, "password") || strings.Contains(lower, "webhook") || lower == "token" || strings.HasSuffix(lower, "_token")
			if isSensitive {
				if !isReference {
					return true
				}
				ref, ok := nested.(string)
				if !ok || !validSecretRef(ref) {
					return true
				}
			}
			if containsSecretMaterial(nested) {
				return true
			}
		}
	case []any:
		for _, nested := range item {
			if containsSecretMaterial(nested) {
				return true
			}
		}
	}
	return false
}

func decodeCredential(raw json.RawMessage, err error) (adminopsport.Credential, error) {
	var item adminopsport.Credential
	if err != nil {
		return item, classify(err)
	}
	return item, classify(json.Unmarshal(raw, &item))
}

func notificationFromRow(row adminopsport.NotificationSetting) NotificationSetting {
	return NotificationSetting{Enabled: row.Enabled, Channel: row.Channel, SecretRef: row.SecretRef, SecretMask: row.SecretMask, ValidationState: row.ValidationState, UpdatedAt: row.UpdatedAt.UTC()}
}

func classify(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, adminopsport.ErrNotFound) {
		return adminopsport.ErrNotFound
	}
	if errors.Is(err, ErrInvalidCommand) || errors.Is(err, ErrConflict) || errors.Is(err, ErrSecretMaterial) || errors.Is(err, ErrVersionConflict) || errors.Is(err, ErrInvalidTransition) {
		return err
	}
	return fmt.Errorf("%w: %v", ErrUnavailable, err)
}
