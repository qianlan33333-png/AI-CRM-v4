package app

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"

	distributiondomain "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/domain"
	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

const (
	ServicePeriodProjectionDraftStatus    = "service_period_draft"
	ServicePeriodProjectionEnabledStatus  = "service_period_enabled"
	ServicePeriodProjectionDisabledStatus = "service_period_disabled"
	ServicePeriodProjectionArchivedStatus = "service_period_archived"
)

func IsServicePeriodProjection(raw json.RawMessage) bool {
	canonical, err := CanonicalLegacyAdminProjection(raw)
	if err != nil {
		return false
	}
	var projection struct {
		Status  string `json:"status"`
		Enabled bool   `json:"enabled"`
	}
	if json.Unmarshal(canonical, &projection) != nil {
		return false
	}
	switch projection.Status {
	case ServicePeriodProjectionDraftStatus,
		ServicePeriodProjectionDisabledStatus,
		ServicePeriodProjectionArchivedStatus:
		return !projection.Enabled
	case ServicePeriodProjectionEnabledStatus:
		return projection.Enabled
	default:
		return false
	}
}

// ServicePeriodStoreUpdate is the CAS write handed to the Product repository.
// Projection remains the existing products.legacy_admin_projection; no second
// product model or table is introduced.
type ServicePeriodStoreUpdate struct {
	ID                    productport.ID
	ExpectedVersion       int64
	Name                  string
	Description           string
	PriceMinor            int64
	Currency              string
	DurationDays          int32
	StockQuantity         int32
	Images                []string
	LegacyAdminProjection json.RawMessage
}

// ServicePeriodStore extends the existing Product repository only with
// service-period filtering and a projection-aware CAS update. Existing Product
// receipts remain authoritative for every operation.
type ServicePeriodStore interface {
	ListServicePeriodProducts(context.Context, int32, int32) ([]productport.Product, int64, error)
	GetServicePeriodProduct(context.Context, productport.ID) (productport.Product, error)
	GetServicePeriodProductByCode(context.Context, string) (productport.Product, error)
	GetServicePeriodProductForUpdate(context.Context, productport.ID) (productport.Product, error)
	Create(context.Context, productport.CreateCommand, time.Time) (productport.Product, error)
	UpdateServicePeriodProduct(context.Context, ServicePeriodStoreUpdate, time.Time) (productport.Product, error)
	ReadServicePeriodDuration(context.Context, productport.ID) (int32, error)
	SetServicePeriodDuration(context.Context, productport.ID, int32) error
	Reserve(context.Context, Reservation) (Receipt, bool, error)
	Complete(context.Context, int64, json.RawMessage, time.Time) (Receipt, error)
}

// ServicePeriodService restores the legacy local Product lifecycle. Its only
// collaborators are a local UnitOfWork, the Product repository, and the local
// transactional event appender; no provider or external-effect adapter can be
// supplied to this service.
type ServicePeriodService struct {
	uow    platformport.UnitOfWork
	store  ServicePeriodStore
	events productport.EventAppender
	policy productPolicyWriter
	now    func() time.Time
}

func NewServicePeriodService(uow platformport.UnitOfWork, store ServicePeriodStore, events productport.EventAppender) *ServicePeriodService {
	return &ServicePeriodService{uow: uow, store: store, events: events, now: time.Now}
}

func (service *ServicePeriodService) SetDistributionPolicyWriter(writer distributionport.ProductPolicyService) error {
	if service == nil || writer == nil {
		return ErrUnavailable
	}
	service.policy = writer
	return nil
}

func (service *ServicePeriodService) ListServicePeriodProducts(ctx context.Context, limit, offset int32) (productport.ServicePeriodPage, error) {
	if !servicePeriodReady(service) {
		return productport.ServicePeriodPage{}, ErrUnavailable
	}
	if limit == 0 {
		limit = DefaultLimit
	}
	if limit < 1 || limit > MaximumLimit || offset < 0 || offset > MaximumLegacyOffset {
		return productport.ServicePeriodPage{}, ErrInvalidCursor
	}

	page := productport.ServicePeriodPage{
		OK:     true,
		Items:  make([]productport.ServicePeriodProduct, 0),
		Limit:  limit,
		Offset: offset,
	}
	var rows []productport.Product
	durations := make(map[productport.ID]int32)
	if err := service.uow.Within(ctx, func(tx context.Context) error {
		var err error
		rows, page.Total, err = service.store.ListServicePeriodProducts(tx, limit, offset)
		if err != nil {
			return err
		}
		for _, row := range rows {
			durations[row.ID], err = service.store.ReadServicePeriodDuration(tx, row.ID)
			if err != nil {
				return err
			}
		}
		return err
	}); err != nil {
		return productport.ServicePeriodPage{}, classify(err)
	}
	if page.Total < 0 || len(rows) > int(limit) {
		return productport.ServicePeriodPage{}, ErrUnavailable
	}
	expectedItems := int64(0)
	if int64(offset) < page.Total {
		expectedItems = page.Total - int64(offset)
		if expectedItems > int64(limit) {
			expectedItems = int64(limit)
		}
	}
	if int64(len(rows)) != expectedItems {
		return productport.ServicePeriodPage{}, ErrUnavailable
	}

	var previous productport.ID
	for _, row := range rows {
		if row.ID <= previous {
			return productport.ServicePeriodPage{}, ErrUnavailable
		}
		projected, err := projectServicePeriodProduct(row, durations[row.ID])
		if err != nil {
			return productport.ServicePeriodPage{}, err
		}
		page.Items = append(page.Items, projected)
		previous = row.ID
	}
	return page, nil
}

func (service *ServicePeriodService) GetServicePeriodProduct(ctx context.Context, id productport.ID) (productport.ServicePeriodProduct, error) {
	if !servicePeriodReady(service) || id < 1 {
		return productport.ServicePeriodProduct{}, ErrNotFound
	}
	var row productport.Product
	var duration int32
	if err := service.uow.Within(ctx, func(tx context.Context) error {
		var err error
		row, err = service.store.GetServicePeriodProduct(tx, id)
		if err != nil {
			return err
		}
		duration, err = service.store.ReadServicePeriodDuration(tx, id)
		return err
	}); err != nil {
		return productport.ServicePeriodProduct{}, classify(err)
	}
	return projectServicePeriodProduct(row, duration)
}

// ReadPublicServicePeriodByCode provides the leaf reader for the separate
// period-product public route. The ordinary public catalog intentionally never
// calls it. Code comparison is exact and has no legacy numeric-ID fallback.
func (service *ServicePeriodService) ReadPublicServicePeriodByCode(ctx context.Context, code string) (productport.CheckoutProduct, error) {
	presentation, available, err := service.ReadServicePeriodPublicPresentationByCode(ctx, code)
	if err != nil || !available {
		return productport.CheckoutProduct{}, ErrNotFound
	}
	return presentation, nil
}

func (service *ServicePeriodService) ReadServicePeriodPublicPresentationByCode(ctx context.Context, code string) (productport.CheckoutProduct, bool, error) {
	code = strings.TrimSpace(code)
	if !servicePeriodReady(service) || code == "" || len(code) > 200 {
		return productport.CheckoutProduct{}, false, ErrNotFound
	}
	var row productport.Product
	var duration int32
	if err := service.uow.Within(ctx, func(tx context.Context) error {
		var err error
		row, err = service.store.GetServicePeriodProductByCode(tx, code)
		if err != nil {
			return err
		}
		duration, err = service.store.ReadServicePeriodDuration(tx, row.ID)
		return err
	}); err != nil {
		return productport.CheckoutProduct{}, false, classify(err)
	}
	projected, err := projectServicePeriodProduct(row, duration)
	if err != nil {
		return productport.CheckoutProduct{}, false, ErrNotFound
	}
	presentation, presentationErr := publicServicePeriodPresentation(projected.AdminProjection)
	if presentationErr != nil {
		return productport.CheckoutProduct{}, false, ErrUnavailable
	}
	checkout := productport.CheckoutProduct{ID: projected.ServiceProductID, ProductType: productport.ProductOptionServicePeriod, Code: projected.ProductCode, Name: projected.Name, PriceMinor: projected.PriceMinor, Currency: projected.Currency, Version: projected.Version, Images: append([]string(nil), projected.Images...), DetailMedia: presentation.Media, LeadChannelID: presentation.LeadChannelID, LeadQRTitle: presentation.LeadQRTitle, LeadQRSubtitle: presentation.LeadQRSubtitle, CompletionBlocksLeadQR: presentation.CompletionBlocksLeadQR, ServicePeriodDurationDays: duration}
	return checkout, projected.Enabled && projected.Lifecycle == productport.ServicePeriodEnabled, nil
}

func (service *ServicePeriodService) CreateServicePeriodProduct(ctx context.Context, command productport.CreateServicePeriodProductCommand) (productport.ServicePeriodProduct, error) {
	normalized, digest, err := normalizeServicePeriodCreate(command)
	if err != nil {
		return productport.ServicePeriodProduct{}, err
	}
	policy, err := normalizedDistributionPolicy(command.DistributionPolicy, true)
	if err != nil {
		return productport.ServicePeriodProduct{}, err
	}
	if !servicePeriodReady(service) {
		return productport.ServicePeriodProduct{}, ErrUnavailable
	}
	now := service.now().UTC()
	if now.IsZero() {
		return productport.ServicePeriodProduct{}, ErrUnavailable
	}

	actorScope := servicePeriodActorScope(normalized.Actor)
	reservation := Reservation{
		Operation:     "create",
		ActorScope:    actorScope,
		KeyDigest:     sha256.Sum256([]byte(normalized.IdempotencyKey)),
		PayloadDigest: digest,
		CreatedAt:     now,
	}
	var result productport.ServicePeriodProduct
	err = service.uow.Within(ctx, func(tx context.Context) error {
		receipt, replay, snapshot, reserveErr := service.reserveServicePeriod(tx, reservation)
		if reserveErr != nil {
			return reserveErr
		}
		if replay {
			result = snapshot
			return nil
		}

		projection, projectionErr := servicePeriodProjectionForLifecycle(normalized.AdminProjection, productport.ServicePeriodDraft)
		if projectionErr != nil {
			return projectionErr
		}
		created, createErr := service.store.Create(tx, productport.CreateCommand{
			ProductCode:           normalized.ProductCode,
			Name:                  normalized.Name,
			Description:           normalized.Description,
			PriceMinor:            normalized.PriceMinor,
			Currency:              normalized.Currency,
			StockQuantity:         normalized.StockQuantity,
			Images:                append([]string{}, normalized.Images...),
			LegacyAdminProjection: projection,
			Actor:                 normalized.Actor,
			IdempotencyKey:        normalized.IdempotencyKey,
		}, now)
		if createErr != nil {
			return createErr
		}
		if createErr = service.store.SetServicePeriodDuration(tx, created.ID, normalized.DurationDays); createErr != nil {
			return createErr
		}
		if service.policy != nil {
			if createErr = saveDistributionPolicyWithin(tx, service.policy, created.ID, distributiondomain.ProductTypeServicePeriod, policy, normalized.Actor, normalized.IdempotencyKey); createErr != nil {
				return createErr
			}
		}
		result, createErr = projectServicePeriodProduct(created, normalized.DurationDays)
		if createErr != nil || result.Version != 1 || result.Lifecycle != productport.ServicePeriodDraft {
			return ErrUnavailable
		}
		if appendErr := service.appendServicePeriodEvent(tx, productport.EventProductCreated, "create", result, 0, normalized.Actor, actorScope, normalized.IdempotencyKey, now); appendErr != nil {
			return appendErr
		}
		return service.completeServicePeriod(tx, receipt.ID, result, now)
	})
	if err != nil {
		return productport.ServicePeriodProduct{}, classify(err)
	}
	return result, nil
}

func (service *ServicePeriodService) UpdateServicePeriodProduct(ctx context.Context, command productport.UpdateServicePeriodProductCommand) (productport.ServicePeriodProduct, error) {
	normalized, digest, err := normalizeServicePeriodUpdate(command)
	if err != nil {
		return productport.ServicePeriodProduct{}, err
	}
	policy, err := normalizedDistributionPolicy(command.DistributionPolicy, false)
	if err != nil {
		return productport.ServicePeriodProduct{}, err
	}
	return service.updateServicePeriod(ctx, servicePeriodWrite{
		action:          "update",
		actor:           normalized.Actor,
		idempotencyKey:  normalized.IdempotencyKey,
		id:              normalized.ID,
		expectedVersion: normalized.ExpectedVersion,
		digest:          digest,
		policy:          policy,
		mutate: func(current productport.Product, projected productport.ServicePeriodProduct) (ServicePeriodStoreUpdate, productport.ServicePeriodLifecycle, bool, error) {
			if projected.Archived {
				return ServicePeriodStoreUpdate{}, "", false, ErrConflict
			}
			projectionSource := normalized.AdminProjection
			if len(projectionSource) == 0 {
				projectionSource = current.LegacyAdminProjection
			}
			images := normalized.Images
			if images == nil {
				images = current.Images
			}
			projection, projectionErr := servicePeriodProjectionForLifecycle(projectionSource, projected.Lifecycle)
			if projectionErr != nil {
				return ServicePeriodStoreUpdate{}, "", false, projectionErr
			}
			return ServicePeriodStoreUpdate{
				ID:                    current.ID,
				ExpectedVersion:       normalized.ExpectedVersion,
				Name:                  normalized.Name,
				Description:           normalized.Description,
				PriceMinor:            normalized.PriceMinor,
				Currency:              normalized.Currency,
				DurationDays:          normalized.DurationDays,
				StockQuantity:         normalized.StockQuantity,
				Images:                append([]string(nil), images...),
				LegacyAdminProjection: projection,
			}, projected.Lifecycle, true, nil
		},
	})
}

func (service *ServicePeriodService) SetServicePeriodProductEnabled(ctx context.Context, command productport.SetServicePeriodProductEnabledCommand) (productport.ServicePeriodProduct, error) {
	normalized, digest, err := normalizeServicePeriodEnabled(command)
	if err != nil {
		return productport.ServicePeriodProduct{}, err
	}
	action := "disable"
	target := productport.ServicePeriodDisabled
	if normalized.Enabled {
		action = "enable"
		target = productport.ServicePeriodEnabled
	}
	return service.updateServicePeriod(ctx, servicePeriodWrite{
		action:          action,
		actor:           normalized.Actor,
		idempotencyKey:  normalized.IdempotencyKey,
		id:              normalized.ID,
		expectedVersion: normalized.ExpectedVersion,
		digest:          digest,
		mutate: func(current productport.Product, projected productport.ServicePeriodProduct) (ServicePeriodStoreUpdate, productport.ServicePeriodLifecycle, bool, error) {
			if projected.Archived {
				return ServicePeriodStoreUpdate{}, "", false, ErrConflict
			}
			if projected.Lifecycle == target {
				return ServicePeriodStoreUpdate{}, target, false, nil
			}
			projection, projectionErr := servicePeriodProjectionForLifecycle(current.LegacyAdminProjection, target)
			if projectionErr != nil {
				return ServicePeriodStoreUpdate{}, "", false, projectionErr
			}
			return unchangedServicePeriodUpdate(current, normalized.ExpectedVersion, projection), target, true, nil
		},
	})
}

func (service *ServicePeriodService) ArchiveServicePeriodProduct(ctx context.Context, command productport.ArchiveServicePeriodProductCommand) (productport.ServicePeriodProduct, error) {
	normalized, digest, err := normalizeServicePeriodArchive(command)
	if err != nil {
		return productport.ServicePeriodProduct{}, err
	}
	return service.updateServicePeriod(ctx, servicePeriodWrite{
		action:          "archive",
		actor:           normalized.Actor,
		idempotencyKey:  normalized.IdempotencyKey,
		id:              normalized.ID,
		expectedVersion: normalized.ExpectedVersion,
		digest:          digest,
		mutate: func(current productport.Product, projected productport.ServicePeriodProduct) (ServicePeriodStoreUpdate, productport.ServicePeriodLifecycle, bool, error) {
			if projected.Archived {
				return ServicePeriodStoreUpdate{}, productport.ServicePeriodArchived, false, nil
			}
			projection, projectionErr := servicePeriodProjectionForLifecycle(current.LegacyAdminProjection, productport.ServicePeriodArchived)
			if projectionErr != nil {
				return ServicePeriodStoreUpdate{}, "", false, projectionErr
			}
			return unchangedServicePeriodUpdate(current, normalized.ExpectedVersion, projection), productport.ServicePeriodArchived, true, nil
		},
	})
}

func (service *ServicePeriodService) CopyServicePeriodProduct(ctx context.Context, command productport.CopyServicePeriodProductCommand) (productport.ServicePeriodProduct, error) {
	normalized, digest, err := normalizeServicePeriodCopy(command)
	if err != nil {
		return productport.ServicePeriodProduct{}, err
	}
	if !servicePeriodReady(service) {
		return productport.ServicePeriodProduct{}, ErrUnavailable
	}
	now := service.now().UTC()
	if now.IsZero() {
		return productport.ServicePeriodProduct{}, ErrUnavailable
	}

	policy := productport.DefaultDistributionPolicy()
	actorScope := servicePeriodActorScope(normalized.Actor)
	reservation := Reservation{
		Operation:     "create",
		ActorScope:    actorScope,
		KeyDigest:     sha256.Sum256([]byte(normalized.IdempotencyKey)),
		PayloadDigest: digest,
		CreatedAt:     now,
	}
	var result productport.ServicePeriodProduct
	err = service.uow.Within(ctx, func(tx context.Context) error {
		receipt, replay, snapshot, reserveErr := service.reserveServicePeriod(tx, reservation)
		if reserveErr != nil {
			return reserveErr
		}
		if replay {
			result = snapshot
			return nil
		}

		source, sourceErr := service.store.GetServicePeriodProductForUpdate(tx, normalized.ID)
		if sourceErr != nil {
			return sourceErr
		}
		sourceDuration, sourceErr := service.store.ReadServicePeriodDuration(tx, source.ID)
		if sourceErr != nil {
			return sourceErr
		}
		sourceProjection, sourceErr := projectServicePeriodProduct(source, sourceDuration)
		if sourceErr != nil {
			return sourceErr
		}
		if sourceProjection.Version != normalized.ExpectedVersion {
			return ErrConflict
		}
		if sourceProjection.Archived || sourceProjection.Lifecycle == productport.ServicePeriodArchived {
			return ErrNotFound
		}
		projection, projectionErr := servicePeriodProjectionForLifecycle(source.LegacyAdminProjection, productport.ServicePeriodDraft)
		if projectionErr != nil {
			return projectionErr
		}
		created, createErr := service.store.Create(tx, productport.CreateCommand{
			ProductCode:           copiedServicePeriodCode(source.ProductCode, actorScope, normalized.IdempotencyKey),
			Name:                  copiedServicePeriodName(source.Name),
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
		if createErr = service.store.SetServicePeriodDuration(tx, created.ID, sourceDuration); createErr != nil {
			return createErr
		}
		if service.policy != nil {
			if createErr = saveDistributionPolicyWithin(tx, service.policy, created.ID, distributiondomain.ProductTypeServicePeriod, &policy, normalized.Actor, normalized.IdempotencyKey); createErr != nil {
				return createErr
			}
		}
		result, createErr = projectServicePeriodProduct(created, sourceDuration)
		if createErr != nil || result.Version != 1 || result.Lifecycle != productport.ServicePeriodDraft || result.ServiceProductID == source.ID {
			return ErrUnavailable
		}
		if appendErr := service.appendServicePeriodEvent(tx, productport.EventProductCreated, "copy", result, source.ID, normalized.Actor, actorScope, normalized.IdempotencyKey, now); appendErr != nil {
			return appendErr
		}
		return service.completeServicePeriod(tx, receipt.ID, result, now)
	})
	if err != nil {
		return productport.ServicePeriodProduct{}, classify(err)
	}
	return result, nil
}

type servicePeriodWrite struct {
	action          string
	actor           int64
	idempotencyKey  string
	id              productport.ID
	expectedVersion int64
	digest          [32]byte
	policy          *productport.DistributionPolicy
	mutate          func(productport.Product, productport.ServicePeriodProduct) (ServicePeriodStoreUpdate, productport.ServicePeriodLifecycle, bool, error)
}

func (service *ServicePeriodService) updateServicePeriod(ctx context.Context, write servicePeriodWrite) (productport.ServicePeriodProduct, error) {
	if !servicePeriodReady(service) || write.mutate == nil {
		return productport.ServicePeriodProduct{}, ErrUnavailable
	}
	now := service.now().UTC()
	if now.IsZero() {
		return productport.ServicePeriodProduct{}, ErrUnavailable
	}
	actorScope := servicePeriodActorScope(write.actor)
	reservation := Reservation{
		Operation:     "update",
		ActorScope:    actorScope,
		KeyDigest:     sha256.Sum256([]byte(write.idempotencyKey)),
		PayloadDigest: write.digest,
		CreatedAt:     now,
	}
	var result productport.ServicePeriodProduct
	err := service.uow.Within(ctx, func(tx context.Context) error {
		receipt, replay, snapshot, reserveErr := service.reserveServicePeriod(tx, reservation)
		if reserveErr != nil {
			return reserveErr
		}
		if replay {
			result = snapshot
			return nil
		}

		current, currentErr := service.store.GetServicePeriodProductForUpdate(tx, write.id)
		if currentErr != nil {
			return currentErr
		}
		currentDuration, currentErr := service.store.ReadServicePeriodDuration(tx, current.ID)
		if currentErr != nil {
			return currentErr
		}
		projected, currentErr := projectServicePeriodProduct(current, currentDuration)
		if currentErr != nil {
			return currentErr
		}
		if projected.Version != write.expectedVersion {
			return ErrConflict
		}
		storeUpdate, target, changed, mutateErr := write.mutate(current, projected)
		if mutateErr != nil {
			return mutateErr
		}
		result = projected
		if changed {
			if storeUpdate.DurationDays < 1 {
				storeUpdate.DurationDays = currentDuration
			}
			updated, updateErr := service.store.UpdateServicePeriodProduct(tx, storeUpdate, now)
			if updateErr != nil {
				return updateErr
			}
			if updateErr = service.store.SetServicePeriodDuration(tx, updated.ID, storeUpdate.DurationDays); updateErr != nil {
				return updateErr
			}
			result, updateErr = projectServicePeriodProduct(updated, storeUpdate.DurationDays)
			if updateErr != nil || result.Version != projected.Version+1 || result.ServiceProductID != projected.ServiceProductID || result.ProductCode != projected.ProductCode || result.DurationDays != storeUpdate.DurationDays || !result.CreatedAt.Equal(projected.CreatedAt) || result.Lifecycle != target || !reflect.DeepEqual(result.Images, storeUpdate.Images) || !jsonEquivalent(result.AdminProjection, storeUpdate.LegacyAdminProjection) {
				return ErrUnavailable
			}
			if write.policy != nil && service.policy != nil {
				if updateErr = saveDistributionPolicyWithin(tx, service.policy, updated.ID, distributiondomain.ProductTypeServicePeriod, write.policy, write.actor, write.idempotencyKey); updateErr != nil {
					return updateErr
				}
			}
			if appendErr := service.appendServicePeriodEvent(tx, productport.EventProductUpdated, write.action, result, 0, write.actor, actorScope, write.idempotencyKey, now); appendErr != nil {
				return appendErr
			}
		}
		return service.completeServicePeriod(tx, receipt.ID, result, now)
	})
	if err != nil {
		return productport.ServicePeriodProduct{}, classify(err)
	}
	return result, nil
}

func (service *ServicePeriodService) reserveServicePeriod(ctx context.Context, reservation Reservation) (Receipt, bool, productport.ServicePeriodProduct, error) {
	receipt, owned, err := service.store.Reserve(ctx, reservation)
	if err != nil {
		return Receipt{}, false, productport.ServicePeriodProduct{}, err
	}
	if !validReceipt(receipt, reservation) {
		return Receipt{}, false, productport.ServicePeriodProduct{}, ErrUnavailable
	}
	if subtle.ConstantTimeCompare(receipt.PayloadDigest[:], reservation.PayloadDigest[:]) != 1 {
		return Receipt{}, false, productport.ServicePeriodProduct{}, ErrConflict
	}
	if owned {
		return receipt, false, productport.ServicePeriodProduct{}, nil
	}
	if receipt.State != "completed" {
		return Receipt{}, false, productport.ServicePeriodProduct{}, ErrUnavailable
	}
	var snapshot productport.ServicePeriodProduct
	if json.Unmarshal(receipt.ResultSnapshot, &snapshot) != nil || !validServicePeriodSnapshot(snapshot) {
		return Receipt{}, false, productport.ServicePeriodProduct{}, ErrUnavailable
	}
	canonical, err := json.Marshal(snapshot)
	if err != nil || !jsonEquivalent(canonical, receipt.ResultSnapshot) {
		return Receipt{}, false, productport.ServicePeriodProduct{}, ErrUnavailable
	}
	return receipt, true, snapshot, nil
}

func (service *ServicePeriodService) completeServicePeriod(ctx context.Context, receiptID int64, result productport.ServicePeriodProduct, now time.Time) error {
	if !validServicePeriodSnapshot(result) {
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

func (service *ServicePeriodService) appendServicePeriodEvent(ctx context.Context, eventType, action string, product productport.ServicePeriodProduct, sourceID productport.ID, actor int64, actorScope, key string, now time.Time) error {
	payload, err := json.Marshal(struct {
		Kind            string         `json:"kind"`
		Action          string         `json:"action"`
		ProductID       productport.ID `json:"product_id"`
		SourceProductID productport.ID `json:"source_product_id,omitempty"`
		Version         int64          `json:"version"`
		Actor           int64          `json:"actor"`
	}{
		Kind:            "service_period",
		Action:          action,
		ProductID:       product.ServiceProductID,
		SourceProductID: sourceID,
		Version:         product.Version,
		Actor:           actor,
	})
	if err != nil {
		return ErrUnavailable
	}
	digest := sha256.Sum256([]byte(actorScope + "\x00" + key))
	_, err = service.events.Append(ctx, productport.Event{
		Type:           eventType,
		Payload:        payload,
		OccurredAt:     now,
		IdempotencyKey: "product.service_period." + action + ":" + hex.EncodeToString(digest[:]),
	})
	return err
}

func projectServicePeriodProduct(product productport.Product, durationDays int32) (productport.ServicePeriodProduct, error) {
	if !validProduct(product) {
		return productport.ServicePeriodProduct{}, ErrUnavailable
	}
	lifecycle, enabled, err := servicePeriodLifecycleFromProjection(product.LegacyAdminProjection)
	if err != nil {
		return productport.ServicePeriodProduct{}, err
	}
	projected := productport.ServicePeriodProduct{
		ServiceProductID: product.ID,
		ProductCode:      product.ProductCode,
		Name:             product.Name,
		Description:      product.Description,
		PriceMinor:       product.PriceMinor,
		Currency:         product.Currency,
		DurationDays:     durationDays,
		StockQuantity:    product.StockQuantity,
		Images:           append([]string(nil), product.Images...),
		AdminProjection:  append(json.RawMessage(nil), product.LegacyAdminProjection...),
		Lifecycle:        lifecycle,
		Enabled:          enabled,
		Archived:         lifecycle == productport.ServicePeriodArchived,
		Version:          product.Version,
		CreatedAt:        product.CreatedAt,
		UpdatedAt:        product.UpdatedAt,
	}
	if !validServicePeriodSnapshot(projected) {
		return productport.ServicePeriodProduct{}, ErrUnavailable
	}
	return projected, nil
}

func servicePeriodLifecycleFromProjection(raw json.RawMessage) (productport.ServicePeriodLifecycle, bool, error) {
	canonical, err := CanonicalLegacyAdminProjection(raw)
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
	case ServicePeriodProjectionDraftStatus:
		if projection.Enabled {
			return "", false, ErrUnavailable
		}
		return productport.ServicePeriodDraft, false, nil
	case ServicePeriodProjectionEnabledStatus:
		if !projection.Enabled {
			return "", false, ErrUnavailable
		}
		return productport.ServicePeriodEnabled, true, nil
	case ServicePeriodProjectionDisabledStatus:
		if projection.Enabled {
			return "", false, ErrUnavailable
		}
		return productport.ServicePeriodDisabled, false, nil
	case ServicePeriodProjectionArchivedStatus:
		if projection.Enabled {
			return "", false, ErrUnavailable
		}
		return productport.ServicePeriodArchived, false, nil
	default:
		return "", false, ErrNotFound
	}
}

func servicePeriodProjectionForLifecycle(raw json.RawMessage, lifecycle productport.ServicePeriodLifecycle) (json.RawMessage, error) {
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
	status := ""
	enabled := false
	switch lifecycle {
	case productport.ServicePeriodDraft:
		status = ServicePeriodProjectionDraftStatus
	case productport.ServicePeriodEnabled:
		status = ServicePeriodProjectionEnabledStatus
		enabled = true
	case productport.ServicePeriodDisabled:
		status = ServicePeriodProjectionDisabledStatus
	case productport.ServicePeriodArchived:
		status = ServicePeriodProjectionArchivedStatus
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

func validServicePeriodSnapshot(product productport.ServicePeriodProduct) bool {
	if product.ServiceProductID < 1 || product.ProductCode == "" || len(product.ProductCode) > 200 || strings.TrimSpace(product.ProductCode) != product.ProductCode ||
		product.Name == "" || len(product.Name) > 200 || strings.TrimSpace(product.Name) != product.Name || len(product.Description) > 10000 ||
		product.PriceMinor < 0 || product.StockQuantity < 0 || product.DurationDays < 1 || len(product.Currency) != 3 || product.Currency != strings.ToUpper(product.Currency) || len(product.Images) > 20 || !IsServicePeriodProjection(product.AdminProjection) ||
		product.Version < 1 || product.CreatedAt.IsZero() || product.UpdatedAt.IsZero() || product.UpdatedAt.Before(product.CreatedAt) {
		return false
	}
	for _, imageURL := range product.Images {
		if strings.TrimSpace(imageURL) != imageURL || imageURL == "" || len(imageURL) > 2048 {
			return false
		}
	}
	switch product.Lifecycle {
	case productport.ServicePeriodDraft, productport.ServicePeriodDisabled:
		return !product.Enabled && !product.Archived
	case productport.ServicePeriodEnabled:
		return product.Enabled && !product.Archived
	case productport.ServicePeriodArchived:
		return !product.Enabled && product.Archived
	default:
		return false
	}
}

func normalizeServicePeriodCreate(command productport.CreateServicePeriodProductCommand) (productport.CreateServicePeriodProductCommand, [32]byte, error) {
	// A decoded [] is meaningful to the Product store: it must persist as the
	// JSON array [], while nil would become JSON null and fail products_images_shape.
	// Keep the empty-array shape through the service-period command boundary.
	command.Images = append([]string{}, command.Images...)
	command.ProductCode = strings.TrimSpace(command.ProductCode)
	command.Name = strings.TrimSpace(command.Name)
	command.Description = strings.TrimSpace(command.Description)
	command.Currency = strings.ToUpper(strings.TrimSpace(command.Currency))
	if command.Actor < 1 || command.ProductCode == "" || len(command.ProductCode) > 200 || command.Name == "" || len(command.Name) > 200 ||
		len(command.Description) > 10000 || command.PriceMinor < 0 || command.StockQuantity < 0 || command.DurationDays < 1 || len(command.Currency) != 3 || len(command.Images) > 20 || !validIdempotencyKey(command.IdempotencyKey) {
		return productport.CreateServicePeriodProductCommand{}, [32]byte{}, ErrInvalidProduct
	}
	for index := range command.Images {
		command.Images[index] = strings.TrimSpace(command.Images[index])
		if command.Images[index] == "" || len(command.Images[index]) > 2048 {
			return productport.CreateServicePeriodProductCommand{}, [32]byte{}, ErrInvalidProduct
		}
	}
	var err error
	if len(command.AdminProjection) > 0 {
		command.AdminProjection, err = CanonicalLegacyAdminProjection(command.AdminProjection)
		if err != nil {
			return productport.CreateServicePeriodProductCommand{}, [32]byte{}, ErrInvalidProduct
		}
	}
	raw, err := json.Marshal(struct {
		ProductCode        string                          `json:"product_code"`
		Name               string                          `json:"name"`
		Description        string                          `json:"description"`
		PriceMinor         int64                           `json:"price_minor"`
		Currency           string                          `json:"currency"`
		DurationDays       int32                           `json:"duration_days"`
		StockQuantity      int32                           `json:"stock_quantity"`
		Images             []string                        `json:"images"`
		AdminProjection    json.RawMessage                 `json:"admin_projection"`
		DistributionPolicy *productport.DistributionPolicy `json:"distribution_policy"`
	}{command.ProductCode, command.Name, command.Description, command.PriceMinor, command.Currency, command.DurationDays, command.StockQuantity, command.Images, command.AdminProjection, command.DistributionPolicy})
	if err != nil {
		return productport.CreateServicePeriodProductCommand{}, [32]byte{}, ErrInvalidProduct
	}
	return command, sha256.Sum256(raw), nil
}

func normalizeServicePeriodUpdate(command productport.UpdateServicePeriodProductCommand) (productport.UpdateServicePeriodProductCommand, [32]byte, error) {
	command.Images = append([]string(nil), command.Images...)
	command.Name = strings.TrimSpace(command.Name)
	command.Description = strings.TrimSpace(command.Description)
	command.Currency = strings.ToUpper(strings.TrimSpace(command.Currency))
	if !validServicePeriodWriteIdentity(command.ID, command.ExpectedVersion, command.Actor, command.IdempotencyKey) ||
		command.Name == "" || len(command.Name) > 200 || len(command.Description) > 10000 || command.PriceMinor < 0 || command.StockQuantity < 0 || command.DurationDays < 1 || len(command.Currency) != 3 || len(command.Images) > 20 {
		return productport.UpdateServicePeriodProductCommand{}, [32]byte{}, ErrInvalidProduct
	}
	for index := range command.Images {
		command.Images[index] = strings.TrimSpace(command.Images[index])
		if command.Images[index] == "" || len(command.Images[index]) > 2048 {
			return productport.UpdateServicePeriodProductCommand{}, [32]byte{}, ErrInvalidProduct
		}
	}
	var err error
	if len(command.AdminProjection) > 0 {
		command.AdminProjection, err = CanonicalLegacyAdminProjection(command.AdminProjection)
		if err != nil {
			return productport.UpdateServicePeriodProductCommand{}, [32]byte{}, ErrInvalidProduct
		}
	}
	raw, err := json.Marshal(struct {
		ID                 productport.ID                  `json:"service_product_id"`
		ExpectedVersion    int64                           `json:"expected_version"`
		Name               string                          `json:"name"`
		Description        string                          `json:"description"`
		PriceMinor         int64                           `json:"price_minor"`
		Currency           string                          `json:"currency"`
		DurationDays       int32                           `json:"duration_days"`
		StockQuantity      int32                           `json:"stock_quantity"`
		Images             []string                        `json:"images"`
		AdminProjection    json.RawMessage                 `json:"admin_projection"`
		DistributionPolicy *productport.DistributionPolicy `json:"distribution_policy"`
	}{command.ID, command.ExpectedVersion, command.Name, command.Description, command.PriceMinor, command.Currency, command.DurationDays, command.StockQuantity, command.Images, command.AdminProjection, command.DistributionPolicy})
	if err != nil {
		return productport.UpdateServicePeriodProductCommand{}, [32]byte{}, ErrInvalidProduct
	}
	return command, sha256.Sum256(raw), nil
}

func normalizeServicePeriodEnabled(command productport.SetServicePeriodProductEnabledCommand) (productport.SetServicePeriodProductEnabledCommand, [32]byte, error) {
	if !validServicePeriodWriteIdentity(command.ID, command.ExpectedVersion, command.Actor, command.IdempotencyKey) {
		return productport.SetServicePeriodProductEnabledCommand{}, [32]byte{}, ErrInvalidProduct
	}
	raw, err := json.Marshal(struct {
		ID              productport.ID `json:"service_product_id"`
		ExpectedVersion int64          `json:"expected_version"`
		Enabled         bool           `json:"enabled"`
	}{command.ID, command.ExpectedVersion, command.Enabled})
	if err != nil {
		return productport.SetServicePeriodProductEnabledCommand{}, [32]byte{}, ErrInvalidProduct
	}
	return command, sha256.Sum256(raw), nil
}

func normalizeServicePeriodCopy(command productport.CopyServicePeriodProductCommand) (productport.CopyServicePeriodProductCommand, [32]byte, error) {
	if !validServicePeriodWriteIdentity(command.ID, command.ExpectedVersion, command.Actor, command.IdempotencyKey) {
		return productport.CopyServicePeriodProductCommand{}, [32]byte{}, ErrInvalidProduct
	}
	raw, err := json.Marshal(struct {
		ID              productport.ID `json:"service_product_id"`
		ExpectedVersion int64          `json:"expected_version"`
	}{command.ID, command.ExpectedVersion})
	if err != nil {
		return productport.CopyServicePeriodProductCommand{}, [32]byte{}, ErrInvalidProduct
	}
	return command, sha256.Sum256(raw), nil
}

func normalizeServicePeriodArchive(command productport.ArchiveServicePeriodProductCommand) (productport.ArchiveServicePeriodProductCommand, [32]byte, error) {
	if !validServicePeriodWriteIdentity(command.ID, command.ExpectedVersion, command.Actor, command.IdempotencyKey) {
		return productport.ArchiveServicePeriodProductCommand{}, [32]byte{}, ErrInvalidProduct
	}
	raw, err := json.Marshal(struct {
		ID              productport.ID `json:"service_product_id"`
		ExpectedVersion int64          `json:"expected_version"`
	}{command.ID, command.ExpectedVersion})
	if err != nil {
		return productport.ArchiveServicePeriodProductCommand{}, [32]byte{}, ErrInvalidProduct
	}
	return command, sha256.Sum256(raw), nil
}

func validServicePeriodWriteIdentity(id productport.ID, version, actor int64, key string) bool {
	return id > 0 && version > 0 && version != math.MaxInt64 && actor > 0 && validIdempotencyKey(key)
}

func unchangedServicePeriodUpdate(current productport.Product, expectedVersion int64, projection json.RawMessage) ServicePeriodStoreUpdate {
	return ServicePeriodStoreUpdate{
		ID:                    current.ID,
		ExpectedVersion:       expectedVersion,
		Name:                  current.Name,
		Description:           current.Description,
		PriceMinor:            current.PriceMinor,
		Currency:              current.Currency,
		StockQuantity:         current.StockQuantity,
		Images:                append([]string(nil), current.Images...),
		LegacyAdminProjection: projection,
	}
}

func servicePeriodActorScope(actor int64) string {
	return fmt.Sprintf("admin:%d", actor)
}

func copiedServicePeriodCode(source, actorScope, key string) string {
	digest := sha256.Sum256([]byte(actorScope + "\x00" + key))
	suffix := "-copy-" + hex.EncodeToString(digest[:8])
	return truncateUTF8Bytes(source, 200-len(suffix)) + suffix
}

func copiedServicePeriodName(source string) string {
	const suffix = " (copy)"
	return truncateUTF8Bytes(source, 200-len(suffix)) + suffix
}

func truncateUTF8Bytes(value string, maximum int) string {
	if maximum < 1 {
		return ""
	}
	value = strings.TrimSpace(value)
	if len(value) <= maximum {
		return value
	}
	for maximum > 0 && !utf8.RuneStart(value[maximum]) {
		maximum--
	}
	return strings.TrimSpace(value[:maximum])
}

func servicePeriodReady(service *ServicePeriodService) bool {
	return service != nil && !nilServicePeriodDependency(service.uow) && !nilServicePeriodDependency(service.store) && !nilServicePeriodDependency(service.events) && service.now != nil
}

func nilServicePeriodDependency(value any) bool {
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

type servicePeriodPublicPresentation struct {
	Media                       []productport.PublicDetailMedia
	LeadChannelID               int64
	LeadQRTitle, LeadQRSubtitle string
	CompletionBlocksLeadQR      bool
}

func publicServicePeriodPresentation(raw json.RawMessage) (servicePeriodPublicPresentation, error) {
	canonical, err := CanonicalLegacyAdminProjection(raw)
	if err != nil {
		return servicePeriodPublicPresentation{}, ErrUnavailable
	}
	var projection struct {
		Slices                    []map[string]any `json:"slices"`
		LeadChannelID             *int64           `json:"lead_channel_id"`
		LeadQRTitle               string           `json:"lead_qr_title"`
		LeadQRSubtitle            string           `json:"lead_qr_subtitle"`
		CompletionRedirectEnabled bool             `json:"completion_redirect_enabled"`
		CompletionTarget          map[string]any   `json:"completion_target"`
	}
	if json.Unmarshal(canonical, &projection) != nil {
		return servicePeriodPublicPresentation{}, ErrUnavailable
	}
	value := servicePeriodPublicPresentation{LeadQRTitle: projection.LeadQRTitle, LeadQRSubtitle: projection.LeadQRSubtitle, CompletionBlocksLeadQR: projection.CompletionRedirectEnabled || len(projection.CompletionTarget) > 0}
	if projection.LeadChannelID != nil {
		value.LeadChannelID = *projection.LeadChannelID
	}
	for _, slice := range projection.Slices {
		var id int64
		for _, key := range []string{"image_library_id", "library_image_id", "asset_id", "image_id"} {
			if number, ok := slice[key].(float64); ok && number >= 1 && number == float64(int64(number)) {
				id = int64(number)
				break
			}
		}
		if id < 1 {
			continue
		}
		media := productport.PublicDetailMedia{ImageID: id}
		if n, ok := slice["width"].(float64); ok && n > 0 && n <= 10000 {
			media.Width = int32(n)
		}
		if n, ok := slice["height"].(float64); ok && n > 0 && n <= 10000 {
			media.Height = int32(n)
		}
		value.Media = append(value.Media, media)
	}
	return value, nil
}
