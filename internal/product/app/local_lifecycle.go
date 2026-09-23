package app

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"reflect"
	"strings"
	"time"

	distributiondomain "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/domain"
	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

const (
	// These values are the existing generic Product projection vocabulary used
	// by the old WeChat-pay product view. They remain local CRM facts only.
	LocalProductProjectionDraftStatus    = "draft"
	LocalProductProjectionEnabledStatus  = "active"
	LocalProductProjectionDisabledStatus = "disabled"
	LocalProductProjectionArchivedStatus = "archived"
)

var ErrLocalProductDeleteNotAllowed = errors.New("local product can only delete an unreferenced draft")
var ErrLocalProductNotEnabled = errors.New("product is not enabled")

// LocalProductLifecycleStore is the phase-1 repository contract. The
// projection-aware update and safe delete methods are deliberately separate
// from the existing Product Store interface so current catalog fakes and
// generated code remain untouched until the central integration lane
// materializes their SQL methods.
type LocalProductLifecycleStore interface {
	Get(context.Context, productport.ID) (productport.Product, error)
	GetForUpdate(context.Context, productport.ID) (productport.Product, error)
	Create(context.Context, productport.CreateCommand, time.Time) (productport.Product, error)
	UpdateLocalProductLifecycle(context.Context, LocalProductLifecycleStoreUpdate, time.Time) (productport.Product, error)
	DeleteLocalProductIfSafe(context.Context, productport.ID, int64) (bool, error)
	Reserve(context.Context, Reservation) (Receipt, bool, error)
	Complete(context.Context, int64, json.RawMessage, time.Time) (Receipt, error)
}

type LocalProductLifecycleStoreUpdate struct {
	ID                    productport.ID
	ExpectedVersion       int64
	LocalLifecycle        productport.LocalProductLifecycle
	LegacyAdminProjection json.RawMessage
}

type LocalProductLifecycleService struct {
	uow    platformport.UnitOfWork
	store  LocalProductLifecycleStore
	events productport.EventAppender
	policy productPolicyWriter
	now    func() time.Time
}

var _ productport.LocalProductLifecycleApplication = (*LocalProductLifecycleService)(nil)

func NewLocalProductLifecycleService(uow platformport.UnitOfWork, store LocalProductLifecycleStore, events productport.EventAppender) *LocalProductLifecycleService {
	return &LocalProductLifecycleService{uow: uow, store: store, events: events, now: time.Now}
}

func (service *LocalProductLifecycleService) SetDistributionPolicyWriter(writer distributionport.ProductPolicyService) error {
	if service == nil || writer == nil {
		return ErrUnavailable
	}
	service.policy = writer
	return nil
}

func (service *LocalProductLifecycleService) SetLocalProductEnabled(ctx context.Context, command productport.SetLocalProductEnabledCommand) (productport.LocalProduct, error) {
	normalized, digest, err := normalizeLocalProductEnabled(command)
	if err != nil {
		return productport.LocalProduct{}, err
	}
	if !localProductLifecycleReady(service) {
		return productport.LocalProduct{}, ErrUnavailable
	}
	now := service.now().UTC()
	if now.IsZero() {
		return productport.LocalProduct{}, ErrUnavailable
	}

	action := "disable"
	target := productport.LocalProductDisabled
	if normalized.Enabled {
		action = "enable"
		target = productport.LocalProductEnabled
	}
	reservation := localProductLifecycleReservation(normalized.Actor, normalized.IdempotencyKey, digest, now)
	reservation.Operation = "lifecycle"
	var result productport.LocalProduct
	err = service.uow.Within(ctx, func(tx context.Context) error {
		receipt, owned, replayErr := service.reserveLocalProductLifecycle(tx, reservation)
		if replayErr != nil {
			return replayErr
		}
		if !owned {
			replayed, decodeErr := decodeLocalProductSnapshot(receipt.ResultSnapshot)
			if decodeErr != nil {
				return decodeErr
			}
			result = replayed
			return nil
		}

		current, currentErr := service.store.GetForUpdate(tx, normalized.ID)
		if currentErr != nil {
			return currentErr
		}
		projected, currentErr := projectLocalProduct(current)
		if currentErr != nil {
			return currentErr
		}
		if projected.Version != normalized.ExpectedVersion {
			return ErrConflict
		}
		if projected.Lifecycle == productport.LocalProductArchived {
			return ErrNotFound
		}
		if projected.Lifecycle == target {
			result = projected
			return service.completeLocalProductSnapshot(tx, receipt.ID, result, now)
		}

		projection, projectionErr := localProductProjectionForLifecycle(current.LegacyAdminProjection, target)
		if projectionErr != nil {
			return projectionErr
		}
		updated, updateErr := service.store.UpdateLocalProductLifecycle(tx, LocalProductLifecycleStoreUpdate{
			ID: normalized.ID, ExpectedVersion: normalized.ExpectedVersion, LocalLifecycle: target, LegacyAdminProjection: projection,
		}, now)
		if updateErr != nil {
			return updateErr
		}
		result, updateErr = projectLocalProduct(updated)
		if updateErr != nil || !sameLocalProductBody(result, projected) || result.Version != projected.Version+1 || result.Lifecycle != target {
			return ErrUnavailable
		}
		if eventErr := appendLocalProductLifecycleEvent(tx, service.events, action, result, 0, normalized.Actor, reservation, now, productport.EventProductUpdated); eventErr != nil {
			return eventErr
		}
		return service.completeLocalProductSnapshot(tx, receipt.ID, result, now)
	})
	if err != nil {
		return productport.LocalProduct{}, classifyLocalProductLifecycle(err)
	}
	return result, nil
}

func (service *LocalProductLifecycleService) CopyLocalProduct(ctx context.Context, command productport.CopyLocalProductCommand) (productport.LocalProduct, error) {
	normalized, digest, err := normalizeLocalProductCopy(command)
	if err != nil {
		return productport.LocalProduct{}, err
	}
	if !localProductLifecycleReady(service) {
		return productport.LocalProduct{}, ErrUnavailable
	}
	now := service.now().UTC()
	if now.IsZero() {
		return productport.LocalProduct{}, ErrUnavailable
	}

	policy := productport.DefaultDistributionPolicy()
	actorScope := localProductActorScope(normalized.Actor)
	reservation := localProductLifecycleReservation(normalized.Actor, normalized.IdempotencyKey, digest, now)
	reservation.Operation = "copy"
	var result productport.LocalProduct
	err = service.uow.Within(ctx, func(tx context.Context) error {
		receipt, owned, replayErr := service.reserveLocalProductLifecycle(tx, reservation)
		if replayErr != nil {
			return replayErr
		}
		if !owned {
			replayed, decodeErr := decodeLocalProductSnapshot(receipt.ResultSnapshot)
			if decodeErr != nil {
				return decodeErr
			}
			result = replayed
			return nil
		}

		source, sourceErr := service.store.GetForUpdate(tx, normalized.ID)
		if sourceErr != nil {
			return sourceErr
		}
		sourceProjected, sourceErr := projectLocalProduct(source)
		if sourceErr != nil {
			return sourceErr
		}
		if sourceProjected.Version != normalized.ExpectedVersion {
			return ErrConflict
		}
		if sourceProjected.Lifecycle == productport.LocalProductArchived {
			return ErrNotFound
		}
		projection, projectionErr := localProductProjectionForLifecycle(nil, productport.LocalProductDraft)
		if projectionErr != nil {
			return projectionErr
		}
		copiedCode := copiedLocalProductCode(source.ProductCode, actorScope, normalized.IdempotencyKey)
		copiedName := copiedLocalProductName(source.Name)
		created, createErr := service.store.Create(tx, productport.CreateCommand{
			ProductCode:           copiedCode,
			Name:                  copiedName,
			Description:           source.Description,
			PriceMinor:            source.PriceMinor,
			Currency:              source.Currency,
			StockQuantity:         source.StockQuantity,
			Images:                append([]string(nil), source.Images...),
			LegacyAdminProjection: projection,
			Actor:                 normalized.Actor,
			IdempotencyKey:        normalized.IdempotencyKey,
		}, now)
		if createErr != nil {
			return createErr
		}
		if service.policy != nil {
			if createErr = saveDistributionPolicyWithin(tx, service.policy, created.ID, distributiondomain.ProductTypeStandard, &policy, normalized.Actor, normalized.IdempotencyKey); createErr != nil {
				return createErr
			}
		}
		result, createErr = projectLocalProduct(created)
		if createErr != nil || result.Version != 1 || result.Lifecycle != productport.LocalProductDraft || result.Enabled || result.ID == sourceProjected.ID ||
			result.ProductCode != copiedCode || result.Name != copiedName || result.Description != sourceProjected.Description || result.PriceMinor != sourceProjected.PriceMinor ||
			result.Currency != sourceProjected.Currency || result.StockQuantity != sourceProjected.StockQuantity || result.CreatedBy != normalized.Actor || !reflect.DeepEqual(result.Images, sourceProjected.Images) {
			return ErrUnavailable
		}
		if eventErr := appendLocalProductLifecycleEvent(tx, service.events, "copy", result, sourceProjected.ID, normalized.Actor, reservation, now, productport.EventProductCreated); eventErr != nil {
			return eventErr
		}
		return service.completeLocalProductSnapshot(tx, receipt.ID, result, now)
	})
	if err != nil {
		return productport.LocalProduct{}, classifyLocalProductLifecycle(err)
	}
	return result, nil
}

// ArchiveLocalProduct preserves the product row and all downstream historical
// facts, but makes the local product terminal for ordinary admin discovery,
// new selection, public presentation, and checkout.
func (service *LocalProductLifecycleService) ArchiveLocalProduct(ctx context.Context, command productport.ArchiveLocalProductCommand) (productport.LocalProduct, error) {
	normalized, digest, err := normalizeLocalProductArchive(command)
	if err != nil {
		return productport.LocalProduct{}, err
	}
	if !localProductLifecycleReady(service) {
		return productport.LocalProduct{}, ErrUnavailable
	}
	now := service.now().UTC()
	if now.IsZero() {
		return productport.LocalProduct{}, ErrUnavailable
	}

	reservation := localProductLifecycleReservation(normalized.Actor, normalized.IdempotencyKey, digest, now)
	reservation.Operation = "archive"
	var result productport.LocalProduct
	err = service.uow.Within(ctx, func(tx context.Context) error {
		receipt, owned, reserveErr := service.reserveLocalProductLifecycle(tx, reservation)
		if reserveErr != nil {
			return reserveErr
		}
		if !owned {
			replayed, decodeErr := decodeLocalProductSnapshot(receipt.ResultSnapshot)
			if decodeErr != nil {
				return decodeErr
			}
			result = replayed
			return nil
		}

		current, currentErr := service.store.GetForUpdate(tx, normalized.ID)
		if currentErr != nil {
			return currentErr
		}
		projected, currentErr := projectLocalProduct(current)
		if currentErr != nil {
			return currentErr
		}
		if projected.Version != normalized.ExpectedVersion {
			return ErrConflict
		}
		if projected.Lifecycle == productport.LocalProductArchived {
			result = projected
			return service.completeLocalProductSnapshot(tx, receipt.ID, result, now)
		}
		projection, projectionErr := localProductProjectionForLifecycle(current.LegacyAdminProjection, productport.LocalProductArchived)
		if projectionErr != nil {
			return projectionErr
		}
		updated, updateErr := service.store.UpdateLocalProductLifecycle(tx, LocalProductLifecycleStoreUpdate{
			ID: normalized.ID, ExpectedVersion: normalized.ExpectedVersion, LocalLifecycle: productport.LocalProductArchived, LegacyAdminProjection: projection,
		}, now)
		if updateErr != nil {
			return updateErr
		}
		result, updateErr = projectLocalProduct(updated)
		if updateErr != nil || !sameLocalProductBody(result, projected) || result.Version != projected.Version+1 || result.Lifecycle != productport.LocalProductArchived || result.Enabled {
			return ErrUnavailable
		}
		if eventErr := appendLocalProductLifecycleEvent(tx, service.events, "archive", result, 0, normalized.Actor, reservation, now, productport.EventProductUpdated); eventErr != nil {
			return eventErr
		}
		return service.completeLocalProductSnapshot(tx, receipt.ID, result, now)
	})
	if err != nil {
		return productport.LocalProduct{}, classifyLocalProductLifecycle(err)
	}
	return result, nil
}

func (service *LocalProductLifecycleService) DeleteLocalProduct(ctx context.Context, command productport.DeleteLocalProductCommand) (productport.DeleteLocalProductResult, error) {
	normalized, digest, err := normalizeLocalProductDelete(command)
	if err != nil {
		return productport.DeleteLocalProductResult{}, err
	}
	if !localProductLifecycleReady(service) {
		return productport.DeleteLocalProductResult{}, ErrUnavailable
	}
	now := service.now().UTC()
	if now.IsZero() {
		return productport.DeleteLocalProductResult{}, ErrUnavailable
	}

	reservation := localProductLifecycleReservation(normalized.Actor, normalized.IdempotencyKey, digest, now)
	reservation.Operation = "delete"
	var result productport.DeleteLocalProductResult
	err = service.uow.Within(ctx, func(tx context.Context) error {
		receipt, owned, replayErr := service.reserveLocalProductLifecycle(tx, reservation)
		if replayErr != nil {
			return replayErr
		}
		if !owned {
			if json.Unmarshal(receipt.ResultSnapshot, &result) != nil || result.ProductID != normalized.ID || !result.Deleted {
				return ErrUnavailable
			}
			canonical, marshalErr := json.Marshal(result)
			if marshalErr != nil || !jsonEquivalent(canonical, receipt.ResultSnapshot) {
				return ErrUnavailable
			}
			return nil
		}

		current, currentErr := service.store.GetForUpdate(tx, normalized.ID)
		if currentErr != nil {
			return currentErr
		}
		projected, currentErr := projectLocalProduct(current)
		if currentErr != nil {
			return currentErr
		}
		if projected.Version != normalized.ExpectedVersion {
			return ErrConflict
		}
		// Physical deletion is intentionally narrower than disable/archive: only
		// an untouched draft may be removed, and the repository must prove that
		// no other Product fact references it.
		if projected.Lifecycle != productport.LocalProductDraft {
			return localProductDeleteConflict()
		}
		deleted, deleteErr := service.store.DeleteLocalProductIfSafe(tx, normalized.ID, normalized.ExpectedVersion)
		if deleteErr != nil {
			return deleteErr
		}
		if !deleted {
			return localProductDeleteConflict()
		}
		result = productport.DeleteLocalProductResult{ProductID: normalized.ID, Deleted: true}
		if eventErr := appendLocalProductLifecycleEvent(tx, service.events, "delete", projected, 0, normalized.Actor, reservation, now, productport.EventProductUpdated); eventErr != nil {
			return eventErr
		}
		snapshot, marshalErr := json.Marshal(result)
		if marshalErr != nil {
			return ErrUnavailable
		}
		completed, completeErr := service.store.Complete(tx, receipt.ID, snapshot, now)
		if completeErr != nil || completed.State != "completed" || !jsonEquivalent(snapshot, completed.ResultSnapshot) {
			return ErrUnavailable
		}
		return nil
	})
	if err != nil {
		return productport.DeleteLocalProductResult{}, classifyLocalProductLifecycle(err)
	}
	return result, nil
}

func (service *LocalProductLifecycleService) ShareLocalProduct(ctx context.Context, id productport.ID) (productport.LocalProductShare, error) {
	if !localProductLifecycleReady(service) || id < 1 {
		return productport.LocalProductShare{}, ErrNotFound
	}
	var result productport.LocalProductShare
	err := service.uow.Within(ctx, func(tx context.Context) error {
		product, getErr := service.store.Get(tx, id)
		if getErr != nil {
			return getErr
		}
		projected, projectErr := projectLocalProduct(product)
		if projectErr != nil {
			return projectErr
		}
		if !projected.Enabled || projected.Lifecycle != productport.LocalProductEnabled {
			return ErrLocalProductNotEnabled
		}
		result = productport.LocalProductShare{
			ProductID: projected.ID, ProductCode: projected.ProductCode, Lifecycle: projected.Lifecycle,
			Available: true, PurchaseURL: "/p/" + url.PathEscape(projected.ProductCode),
		}
		return nil
	})
	if err != nil {
		return productport.LocalProductShare{}, classifyLocalProductLifecycle(err)
	}
	return result, nil
}

func (service *LocalProductLifecycleService) reserveLocalProductLifecycle(ctx context.Context, reservation Reservation) (Receipt, bool, error) {
	receipt, owned, err := service.store.Reserve(ctx, reservation)
	if err != nil {
		return Receipt{}, false, err
	}
	if !validReceipt(receipt, reservation) {
		return Receipt{}, false, ErrUnavailable
	}
	if subtle.ConstantTimeCompare(receipt.PayloadDigest[:], reservation.PayloadDigest[:]) != 1 {
		return Receipt{}, false, ErrConflict
	}
	if owned {
		return receipt, true, nil
	}
	if receipt.State != "completed" || len(receipt.ResultSnapshot) == 0 {
		return Receipt{}, false, ErrUnavailable
	}
	return receipt, false, nil
}

func (service *LocalProductLifecycleService) completeLocalProductSnapshot(ctx context.Context, receiptID int64, result productport.LocalProduct, now time.Time) error {
	if !validLocalProductSnapshot(result) {
		return ErrUnavailable
	}
	snapshot, err := json.Marshal(result)
	if err != nil {
		return ErrUnavailable
	}
	completed, err := service.store.Complete(ctx, receiptID, snapshot, now)
	if err != nil || completed.State != "completed" || !jsonEquivalent(snapshot, completed.ResultSnapshot) {
		return ErrUnavailable
	}
	return nil
}

func decodeLocalProductSnapshot(raw []byte) (productport.LocalProduct, error) {
	var result productport.LocalProduct
	if json.Unmarshal(raw, &result) != nil || !validLocalProductSnapshot(result) {
		return productport.LocalProduct{}, ErrUnavailable
	}
	canonical, err := json.Marshal(result)
	if err != nil || !jsonEquivalent(canonical, raw) {
		return productport.LocalProduct{}, ErrUnavailable
	}
	return result, nil
}

func appendLocalProductLifecycleEvent(ctx context.Context, events productport.EventAppender, action string, product productport.LocalProduct, sourceID productport.ID, actor int64, reservation Reservation, now time.Time, eventType string) error {
	payload, err := json.Marshal(struct {
		Kind            string         `json:"kind"`
		Action          string         `json:"action"`
		ProductID       productport.ID `json:"product_id"`
		SourceProductID productport.ID `json:"source_product_id,omitempty"`
		Version         int64          `json:"version"`
		Actor           int64          `json:"actor"`
	}{"wechat_pay_product", action, product.ID, sourceID, product.Version, actor})
	if err != nil {
		return ErrUnavailable
	}
	digest := sha256.Sum256([]byte(reservation.ActorScope + "\x00" + action + "\x00" + hex.EncodeToString(reservation.KeyDigest[:])))
	if events == nil {
		return ErrUnavailable
	}
	_, err = events.Append(ctx, productport.Event{
		Type: eventType, Payload: payload, OccurredAt: now,
		IdempotencyKey: "product.wechat_pay." + action + ":" + hex.EncodeToString(digest[:]),
	})
	return err
}

func localProductLifecycleReservation(actor int64, key string, digest [32]byte, now time.Time) Reservation {
	return Reservation{Operation: "update", ActorScope: localProductActorScope(actor), KeyDigest: sha256.Sum256([]byte(key)), PayloadDigest: digest, CreatedAt: now}
}

func normalizeLocalProductEnabled(command productport.SetLocalProductEnabledCommand) (productport.SetLocalProductEnabledCommand, [32]byte, error) {
	if !validLocalProductWriteIdentity(command.ID, command.ExpectedVersion, command.Actor, command.IdempotencyKey) {
		return productport.SetLocalProductEnabledCommand{}, [32]byte{}, ErrInvalidProduct
	}
	raw, err := json.Marshal(struct {
		ID              productport.ID `json:"product_id"`
		ExpectedVersion int64          `json:"expected_version"`
		Enabled         bool           `json:"enabled"`
	}{command.ID, command.ExpectedVersion, command.Enabled})
	if err != nil {
		return productport.SetLocalProductEnabledCommand{}, [32]byte{}, ErrInvalidProduct
	}
	return command, sha256.Sum256(raw), nil
}

func normalizeLocalProductCopy(command productport.CopyLocalProductCommand) (productport.CopyLocalProductCommand, [32]byte, error) {
	if !validLocalProductWriteIdentity(command.ID, command.ExpectedVersion, command.Actor, command.IdempotencyKey) {
		return productport.CopyLocalProductCommand{}, [32]byte{}, ErrInvalidProduct
	}
	raw, err := json.Marshal(struct {
		ID              productport.ID `json:"product_id"`
		ExpectedVersion int64          `json:"expected_version"`
	}{command.ID, command.ExpectedVersion})
	if err != nil {
		return productport.CopyLocalProductCommand{}, [32]byte{}, ErrInvalidProduct
	}
	return command, sha256.Sum256(raw), nil
}

func normalizeLocalProductDelete(command productport.DeleteLocalProductCommand) (productport.DeleteLocalProductCommand, [32]byte, error) {
	if !validLocalProductWriteIdentity(command.ID, command.ExpectedVersion, command.Actor, command.IdempotencyKey) {
		return productport.DeleteLocalProductCommand{}, [32]byte{}, ErrInvalidProduct
	}
	raw, err := json.Marshal(struct {
		ID              productport.ID `json:"product_id"`
		ExpectedVersion int64          `json:"expected_version"`
	}{command.ID, command.ExpectedVersion})
	if err != nil {
		return productport.DeleteLocalProductCommand{}, [32]byte{}, ErrInvalidProduct
	}
	return command, sha256.Sum256(raw), nil
}

func normalizeLocalProductArchive(command productport.ArchiveLocalProductCommand) (productport.ArchiveLocalProductCommand, [32]byte, error) {
	if !validLocalProductWriteIdentity(command.ID, command.ExpectedVersion, command.Actor, command.IdempotencyKey) {
		return productport.ArchiveLocalProductCommand{}, [32]byte{}, ErrInvalidProduct
	}
	raw, err := json.Marshal(struct {
		ID              productport.ID `json:"product_id"`
		ExpectedVersion int64          `json:"expected_version"`
	}{command.ID, command.ExpectedVersion})
	if err != nil {
		return productport.ArchiveLocalProductCommand{}, [32]byte{}, ErrInvalidProduct
	}
	return command, sha256.Sum256(raw), nil
}

func projectLocalProduct(product productport.Product) (productport.LocalProduct, error) {
	if !validProduct(product) {
		return productport.LocalProduct{}, ErrUnavailable
	}
	lifecycle, enabled, err := localProductLifecycleFromProjection(product.LegacyAdminProjection)
	if err != nil {
		return productport.LocalProduct{}, err
	}
	if product.LocalLifecycle != "" {
		switch product.LocalLifecycle {
		case productport.LocalProductDraft:
			lifecycle, enabled = productport.LocalProductDraft, false
		case productport.LocalProductDisabled:
			lifecycle, enabled = productport.LocalProductDisabled, false
		case productport.LocalProductArchived:
			lifecycle, enabled = productport.LocalProductArchived, false
		case productport.LocalProductEnabled:
			lifecycle, enabled = productport.LocalProductEnabled, true
		default:
			return productport.LocalProduct{}, ErrUnavailable
		}
		projectionLifecycle, projectionEnabled, projectionErr := localProductLifecycleFromProjection(product.LegacyAdminProjection)
		if projectionErr != nil || projectionLifecycle != lifecycle || projectionEnabled != enabled {
			return productport.LocalProduct{}, ErrUnavailable
		}
	}
	result := productport.LocalProduct{
		ID: product.ID, ProductCode: product.ProductCode, Name: product.Name, Description: product.Description,
		PriceMinor: product.PriceMinor, Currency: product.Currency, StockQuantity: product.StockQuantity,
		Images: append([]string(nil), product.Images...), CreatedBy: product.CreatedBy,
		CreatedAt: product.CreatedAt, UpdatedAt: product.UpdatedAt, Lifecycle: lifecycle, Enabled: enabled, Version: product.Version,
	}
	if !validLocalProductSnapshot(result) {
		return productport.LocalProduct{}, ErrUnavailable
	}
	return result, nil
}

// ProjectLocalProduct is the typed read projection used by other local
// application boundaries. It does not make the product purchasable or shared.
func ProjectLocalProduct(product productport.Product) (productport.LocalProduct, error) {
	return projectLocalProduct(product)
}

func localProductLifecycleFromProjection(raw json.RawMessage) (productport.LocalProductLifecycle, bool, error) {
	canonical, err := CanonicalLegacyAdminProjection(raw)
	// Canonicalization already rejects unknown or malformed data. It may add
	// disabled defaults introduced after this Product was saved, which must not
	// turn an otherwise valid old record into an unavailable public product.
	if err != nil {
		return "", false, ErrUnavailable
	}
	var projection struct {
		Status  string `json:"status"`
		Enabled bool   `json:"enabled"`
	}
	if json.Unmarshal(canonical, &projection) != nil {
		return "", false, ErrUnavailable
	}
	switch projection.Status {
	case LocalProductProjectionDraftStatus:
		if projection.Enabled {
			return "", false, ErrUnavailable
		}
		return productport.LocalProductDraft, false, nil
	case LocalProductProjectionEnabledStatus, "enabled":
		if !projection.Enabled {
			return "", false, ErrUnavailable
		}
		return productport.LocalProductEnabled, true, nil
	case LocalProductProjectionDisabledStatus, "inactive":
		if projection.Enabled {
			return "", false, ErrUnavailable
		}
		return productport.LocalProductDisabled, false, nil
	case LocalProductProjectionArchivedStatus:
		if projection.Enabled {
			return "", false, ErrUnavailable
		}
		return productport.LocalProductArchived, false, nil
	default:
		return "", false, ErrUnavailable
	}
}

func localProductProjectionForLifecycle(raw json.RawMessage, lifecycle productport.LocalProductLifecycle) (json.RawMessage, error) {
	if len(raw) == 0 {
		raw = json.RawMessage(`{"schema_version":1}`)
	}
	canonical, err := CanonicalLegacyAdminProjection(raw)
	if err != nil {
		return nil, ErrInvalidProduct
	}
	var projection map[string]json.RawMessage
	if json.Unmarshal(canonical, &projection) != nil {
		return nil, ErrUnavailable
	}
	status := LocalProductProjectionDraftStatus
	enabled := false
	switch lifecycle {
	case productport.LocalProductDraft:
		status = LocalProductProjectionDraftStatus
	case productport.LocalProductEnabled:
		status, enabled = LocalProductProjectionEnabledStatus, true
	case productport.LocalProductDisabled:
		status = LocalProductProjectionDisabledStatus
	case productport.LocalProductArchived:
		status = LocalProductProjectionArchivedStatus
	default:
		return nil, ErrInvalidProduct
	}
	projection["status"], _ = json.Marshal(status)
	projection["enabled"], _ = json.Marshal(enabled)
	encoded, err := json.Marshal(projection)
	if err != nil {
		return nil, ErrUnavailable
	}
	return CanonicalLegacyAdminProjection(encoded)
}

func validLocalProductSnapshot(product productport.LocalProduct) bool {
	if product.ID < 1 || product.ProductCode == "" || strings.TrimSpace(product.ProductCode) != product.ProductCode || len(product.ProductCode) > 200 ||
		product.Name == "" || strings.TrimSpace(product.Name) != product.Name || len(product.Name) > 200 || strings.TrimSpace(product.Description) != product.Description || len(product.Description) > 10000 ||
		product.PriceMinor < 0 || product.StockQuantity < 0 || len(product.Currency) != 3 || product.Currency != strings.ToUpper(product.Currency) ||
		product.Version < 1 || product.CreatedBy < 1 || product.CreatedAt.IsZero() || product.UpdatedAt.IsZero() || product.UpdatedAt.Before(product.CreatedAt) {
		return false
	}
	for _, image := range product.Images {
		if image == "" || strings.TrimSpace(image) != image || len(image) > 2048 {
			return false
		}
	}
	if len(product.Images) > 20 {
		return false
	}
	switch product.Lifecycle {
	case productport.LocalProductDraft, productport.LocalProductDisabled, productport.LocalProductArchived:
		return !product.Enabled
	case productport.LocalProductEnabled:
		return product.Enabled
	default:
		return false
	}
}

func sameLocalProductBody(left, right productport.LocalProduct) bool {
	return left.ID == right.ID && left.ProductCode == right.ProductCode && left.Name == right.Name && left.Description == right.Description &&
		left.PriceMinor == right.PriceMinor && left.Currency == right.Currency && left.StockQuantity == right.StockQuantity &&
		left.CreatedBy == right.CreatedBy && left.CreatedAt.Equal(right.CreatedAt) && reflect.DeepEqual(left.Images, right.Images)
}

func validLocalProductWriteIdentity(id productport.ID, version, actor int64, key string) bool {
	return id > 0 && version > 0 && version != math.MaxInt64 && actor > 0 && validIdempotencyKey(key)
}

func localProductActorScope(actor int64) string { return fmt.Sprintf("admin:%d", actor) }

func copiedLocalProductCode(source, actorScope, key string) string {
	digest := sha256.Sum256([]byte(actorScope + "\x00" + key))
	suffix := "-copy-" + hex.EncodeToString(digest[:8])
	return truncateUTF8Bytes(source, 200-len(suffix)) + suffix
}

func copiedLocalProductName(source string) string {
	const suffix = " (copy)"
	return truncateUTF8Bytes(source, 200-len(suffix)) + suffix
}

func localProductDeleteConflict() error {
	return errors.Join(ErrConflict, ErrLocalProductDeleteNotAllowed)
}

func classifyLocalProductLifecycle(err error) error {
	switch {
	case errors.Is(err, ErrLocalProductDeleteNotAllowed), errors.Is(err, ErrLocalProductNotEnabled):
		return err
	default:
		return classify(err)
	}
}

func localProductLifecycleReady(service *LocalProductLifecycleService) bool {
	return service != nil && !nilLocalProductLifecycleDependency(service.uow) && !nilLocalProductLifecycleDependency(service.store) && !nilLocalProductLifecycleDependency(service.events) && service.now != nil
}

func nilLocalProductLifecycleDependency(value any) bool {
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
