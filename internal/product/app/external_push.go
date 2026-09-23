package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

const (
	commerceExternalPushSaveOperation = "external_push_save"
	commerceExternalPushTestOperation = "external_push_test"
)

var ErrExternalPushNotConfigured = errors.New("product external push is not locally configured")

// CommerceExternalPushStore owns Product-local configuration, command
// receipts and immutable test bindings. It carries no Provider adapter.
type CommerceExternalPushStore interface {
	ReadCommerceExternalPushConfiguration(context.Context, productport.ID, productport.ExternalPushProductKind) (productport.ExternalPushConfiguration, error)
	ReadCommerceExternalPushConfigurationForOrder(context.Context, productport.ID) (productport.ExternalPushConfiguration, error)
	LockCommerceExternalPushConfiguration(context.Context, productport.ID, productport.ExternalPushProductKind) (productport.ExternalPushConfiguration, error)
	SaveCommerceExternalPushConfiguration(context.Context, productport.ExternalPushConfiguration, time.Time) (productport.ExternalPushConfiguration, error)
	ReserveCommerceExternalPush(context.Context, Reservation) (Receipt, bool, error)
	CompleteCommerceExternalPush(context.Context, int64, json.RawMessage, time.Time) (Receipt, error)
	CreateCommerceExternalPushTest(context.Context, productport.ExternalPushTest, [32]byte, int64) (productport.ExternalPushTest, error)
	ListCommerceExternalPushTests(context.Context, productport.ID, productport.ExternalPushProductKind, int32) ([]productport.ExternalPushTest, error)
}

// Product uses this Port-only seam so its HTTP/application layer never
// imports an Outbound implementation. The concrete accepter is composed by
// cmd/aicrm and shares the Product transaction.
type ProductExternalPushEffectAccepter = productport.ExternalPushTestAccepter
type ProductExternalPushEffectCommand = productport.ExternalPushTestIntent

type CommerceExternalPushService struct {
	endpoints outboundport.CommercePushEndpointManager
	uow       platformport.UnitOfWork
	store     CommerceExternalPushStore
	effects   ProductExternalPushEffectAccepter
	statuses  productport.ExternalPushTestStatusReader
	events    productport.EventAppender
	now       func() time.Time
}

var _ productport.CommerceExternalPushApplication = (*CommerceExternalPushService)(nil)

func NewCommerceExternalPushService(
	uow platformport.UnitOfWork,
	store CommerceExternalPushStore,
	effects ProductExternalPushEffectAccepter,
	statuses productport.ExternalPushTestStatusReader,
	events productport.EventAppender,
) (*CommerceExternalPushService, error) {
	if events == nil || statuses == nil {
		return nil, errors.New("product external push dependencies are required")
	}
	return &CommerceExternalPushService{uow: uow, store: store, effects: effects, statuses: statuses, events: events, now: time.Now}, nil
}

func (service *CommerceExternalPushService) SetCommercePushEndpointManager(manager outboundport.CommercePushEndpointManager) {
	service.endpoints = manager
}

func (service *CommerceExternalPushService) GetExternalPushConfiguration(
	ctx context.Context,
	productID productport.ID,
	kind productport.ExternalPushProductKind,
) (productport.ExternalPushConfiguration, error) {
	if !commerceExternalPushReady(service) || ctx == nil || ctx.Err() != nil {
		return productport.ExternalPushConfiguration{}, ErrUnavailable
	}
	if productID < 1 || !validExternalPushKind(kind) {
		return productport.ExternalPushConfiguration{}, ErrInvalidProduct
	}
	var result productport.ExternalPushConfiguration
	err := service.uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		result, readErr = service.store.ReadCommerceExternalPushConfiguration(tx, productID, kind)
		if readErr == nil && service.endpoints != nil {
			result.URL, readErr = service.endpoints.ReadCommercePushEndpointWithin(tx, string(kind), int64(productID), result.ConfigurationReference)
		}
		return readErr
	})
	if err != nil {
		return productport.ExternalPushConfiguration{}, classifyCommerceExternalPush(err)
	}
	if !validExternalPushConfiguration(result, productID, kind) {
		return productport.ExternalPushConfiguration{}, ErrUnavailable
	}
	return result, nil
}

// ReadExternalPushConfigurationForOrder is the only Product read exposed to
// the Outbound Order-event consumer. It returns the current opaque ref and
// revision for the already frozen Product ID; product master data stays in the
// Order item snapshot and is not re-read here.
func (service *CommerceExternalPushService) ReadExternalPushConfigurationForOrder(ctx context.Context, productID productport.ID) (productport.ExternalPushConfiguration, error) {
	if !commerceExternalPushReady(service) || ctx == nil || ctx.Err() != nil || productID < 1 {
		return productport.ExternalPushConfiguration{}, ErrUnavailable
	}
	var result productport.ExternalPushConfiguration
	err := service.uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		result, readErr = service.store.ReadCommerceExternalPushConfigurationForOrder(tx, productID)
		return readErr
	})
	if err != nil {
		return productport.ExternalPushConfiguration{}, classifyCommerceExternalPush(err)
	}
	if !validExternalPushConfiguration(result, productID, result.ProductKind) {
		return productport.ExternalPushConfiguration{}, ErrUnavailable
	}
	return result, nil
}

func (service *CommerceExternalPushService) SaveExternalPushConfiguration(
	ctx context.Context,
	command productport.SaveExternalPushConfigurationCommand,
) (productport.ExternalPushConfiguration, error) {
	if !commerceExternalPushReady(service) || ctx == nil || ctx.Err() != nil {
		return productport.ExternalPushConfiguration{}, ErrUnavailable
	}
	if !validSaveCommerceExternalPush(command) {
		return productport.ExternalPushConfiguration{}, ErrInvalidProduct
	}
	now := service.now().UTC()
	if now.IsZero() {
		return productport.ExternalPushConfiguration{}, ErrUnavailable
	}
	payloadDigest := commerceExternalPushSaveDigest(command)
	legacyPayloadDigest := commerceExternalPushLegacySaveDigest(command)
	reservation := commerceExternalPushReservation(commerceExternalPushSaveOperation, command.Actor, command.IdempotencyKey, payloadDigest, now)
	var result productport.ExternalPushConfiguration
	err := service.uow.Within(ctx, func(tx context.Context) error {
		receipt, owned, reserveErr := service.store.ReserveCommerceExternalPush(tx, reservation)
		if reserveErr != nil {
			return reserveErr
		}
		if !validCommerceExternalPushReceipt(receipt, reservation) {
			return ErrUnavailable
		}
		if subtle.ConstantTimeCompare(receipt.PayloadDigest[:], reservation.PayloadDigest[:]) != 1 &&
			!commerceExternalPushLegacySaveReplay(command, receipt, legacyPayloadDigest) {
			return ErrConflict
		}
		if !owned {
			if err := decodeCommerceExternalPushSnapshot(receipt.ResultSnapshot, &result, command.ProductID, command.ProductKind); err != nil {
				return err
			}
			// Destination credentials remain Outbound-owned, never copied into Product receipts.
			if command.URL != nil {
				result.URL = *command.URL
			} else if service.endpoints != nil {
				var e error
				result.URL, e = service.endpoints.ReadCommercePushEndpointWithin(tx, string(command.ProductKind), int64(command.ProductID), result.ConfigurationReference)
				return e
			}
			return nil
		}
		value, readErr := service.store.LockCommerceExternalPushConfiguration(tx, command.ProductID, command.ProductKind)
		if readErr != nil {
			return readErr
		}
		if (command.BusinessParametersSet && value.Revision != command.ExpectedRevision) || (!command.BusinessParametersSet && command.ExpectedRevision > 0 && value.Revision != command.ExpectedRevision) {
			return ErrConflict
		}
		reference := command.ConfigurationReference
		if command.URL != nil {
			if service.endpoints == nil {
				return ErrUnavailable
			}
			var endpointErr error
			previousReference := value.ConfigurationReference
			if previousReference == "" {
				previousReference = command.ConfigurationReference
			}
			reference, endpointErr = service.endpoints.SaveCommercePushEndpointWithin(tx, string(command.ProductKind), int64(command.ProductID), previousReference, *command.URL)
			if endpointErr != nil {
				return endpointErr
			}
			if !command.Enabled {
				reference = ""
			}
		}
		value.Enabled, value.ConfigurationReference = command.Enabled, reference
		if command.FieldMappingSet {
			value.FieldMapping = command.FieldMapping
		}
		if command.BusinessParametersSet {
			value.PushType, value.Day, value.Frequency, value.ExpiresAtTS, value.Remark = command.PushType, command.Day, command.Frequency, command.ExpiresAtTS, command.Remark
			value.CustomParams = cloneCommerceExternalPushParams(command.CustomParams)
		}
		result, reserveErr = service.store.SaveCommerceExternalPushConfiguration(tx, value, now)
		if reserveErr != nil {
			return reserveErr
		}
		if !validExternalPushConfiguration(result, command.ProductID, command.ProductKind) ||
			result.Enabled != command.Enabled || result.ConfigurationReference != reference ||
			(command.BusinessParametersSet && !sameCommerceExternalPushBusiness(result, value)) {
			return ErrUnavailable
		}
		if service.endpoints != nil {
			var endpointErr error
			result.URL, endpointErr = service.endpoints.ReadCommercePushEndpointWithin(tx, string(command.ProductKind), int64(command.ProductID), result.ConfigurationReference)
			if endpointErr != nil {
				return endpointErr
			}
		}
		if eventErr := service.appendEvent(tx, productport.EventExternalPushConfigurationSaved, command.ProductID, command.ProductKind, command.Actor, reservation.KeyDigest, map[string]any{
			"enabled": result.Enabled,
		}); eventErr != nil {
			return eventErr
		}
		snapshotResult := result
		snapshotResult.URL = ""
		return service.completeCommerceExternalPush(tx, receipt.ID, snapshotResult, now)
	})
	if err != nil {
		return productport.ExternalPushConfiguration{}, classifyCommerceExternalPush(err)
	}
	return result, nil
}

func (service *CommerceExternalPushService) QueueExternalPushTest(
	ctx context.Context,
	command productport.QueueExternalPushTestCommand,
) (productport.ExternalPushTest, error) {
	if !commerceExternalPushReady(service) || service.effects == nil || ctx == nil || ctx.Err() != nil {
		return productport.ExternalPushTest{}, ErrUnavailable
	}
	if !validQueueCommerceExternalPushTest(command) {
		return productport.ExternalPushTest{}, ErrInvalidProduct
	}
	now := service.now().UTC()
	if now.IsZero() {
		return productport.ExternalPushTest{}, ErrUnavailable
	}
	payloadDigest := commerceExternalPushTestDigest(command)
	reservation := commerceExternalPushReservation(commerceExternalPushTestOperation, command.Actor, command.IdempotencyKey, payloadDigest, now)
	var result productport.ExternalPushTest
	err := service.uow.Within(ctx, func(tx context.Context) error {
		receipt, owned, reserveErr := service.store.ReserveCommerceExternalPush(tx, reservation)
		if reserveErr != nil {
			return reserveErr
		}
		if !validCommerceExternalPushReceipt(receipt, reservation) {
			return ErrUnavailable
		}
		if subtle.ConstantTimeCompare(receipt.PayloadDigest[:], reservation.PayloadDigest[:]) != 1 {
			return ErrConflict
		}
		if !owned {
			return decodeCommerceExternalPushTestSnapshot(receipt.ResultSnapshot, &result, command.ProductID, command.ProductKind)
		}
		configuration, readErr := service.store.LockCommerceExternalPushConfiguration(tx, command.ProductID, command.ProductKind)
		if readErr != nil {
			return readErr
		}
		if !validExternalPushConfiguration(configuration, command.ProductID, command.ProductKind) {
			return ErrUnavailable
		}
		if !configuration.Enabled {
			return ErrExternalPushNotConfigured
		}
		configurationDigest := commerceExternalPushConfigurationDigest(configuration)
		effect, acceptErr := service.effects.AcceptExternalPushTestWithin(tx, productport.ExternalPushTestIntent{
			ProductID: command.ProductID, ProductKind: command.ProductKind,
			ConfigurationReference: configuration.ConfigurationReference, ConfigurationRevision: configuration.Revision,
			ReceiptKeyDigest: reservation.KeyDigest,
		})
		if acceptErr != nil {
			return acceptErr
		}
		if !validExternalPushTest(effect, command.ProductID, command.ProductKind) || !validCommerceExternalPushTestDeliveryID(effect.DeliveryID) {
			return ErrUnavailable
		}
		result, readErr = service.store.CreateCommerceExternalPushTest(tx, effect, configurationDigest, receipt.ID)
		if readErr != nil {
			return readErr
		}
		if !validExternalPushTest(result, command.ProductID, command.ProductKind) || result.EffectID != effect.EffectID || result.DeliveryID != effect.DeliveryID || result.State != effect.State {
			return ErrUnavailable
		}
		if eventErr := service.appendEvent(tx, productport.EventExternalPushTestAccepted, command.ProductID, command.ProductKind, command.Actor, reservation.KeyDigest, map[string]any{
			"effect_id":                   result.EffectID,
			"state":                       result.State,
			"provider_accepted":           result.ProviderAccepted,
			"delivery_proven":             result.DeliveryProven,
			"real_external_call_executed": result.RealExternalCallExecuted,
		}); eventErr != nil {
			return eventErr
		}
		return service.completeCommerceExternalPush(tx, receipt.ID, result, now)
	})
	if err != nil {
		return productport.ExternalPushTest{}, classifyCommerceExternalPush(err)
	}
	return result, nil
}

// ListExternalPushTests returns Product-owned bindings enriched only with the
// Outbound-owned, digest-safe status projection. A completed HTTP request is
// still not claimed as delivery proof.
func (service *CommerceExternalPushService) ListExternalPushTests(
	ctx context.Context, productID productport.ID, kind productport.ExternalPushProductKind,
) ([]productport.ExternalPushTest, error) {
	if !commerceExternalPushReady(service) || service.statuses == nil || ctx == nil || ctx.Err() != nil {
		return nil, ErrUnavailable
	}
	if productID < 1 || !validExternalPushKind(kind) {
		return nil, ErrInvalidProduct
	}
	var values []productport.ExternalPushTest
	err := service.uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		values, readErr = service.store.ListCommerceExternalPushTests(tx, productID, kind, 20)
		return readErr
	})
	if err != nil {
		return nil, classifyCommerceExternalPush(err)
	}
	if len(values) > 20 {
		return nil, ErrUnavailable
	}
	for index := range values {
		value := &values[index]
		if !validStoredCommerceExternalPushTest(*value, productID, kind) {
			return nil, ErrUnavailable
		}
		status, readErr := service.statuses.ReadExternalPushTestStatus(ctx, productID, value.EffectID)
		if readErr != nil || !validCommerceExternalPushTestStatus(status, value.EffectID) {
			return nil, ErrUnavailable
		}
		value.State = status.State
		value.AttemptCount = status.AttemptCount
		value.RealExternalCallExecuted = status.RealExternalCallExecuted
		value.ProviderAccepted = status.State == "provider_accepted" && status.ProviderCallAttempted && status.RealExternalCallExecuted && status.ProviderResultReceived != nil && *status.ProviderResultReceived
		value.UpdatedAt = status.UpdatedAt
		// Delivery is intentionally never inferred from a transport response.
		value.DeliveryProven = false
		value.AutoRetryAllowed = false
	}
	return values, nil
}

func (service *CommerceExternalPushService) completeCommerceExternalPush(ctx context.Context, receiptID int64, result any, now time.Time) error {
	snapshot, err := json.Marshal(result)
	if err != nil {
		return ErrUnavailable
	}
	receipt, err := service.store.CompleteCommerceExternalPush(ctx, receiptID, snapshot, now)
	if err != nil || receipt.ID != receiptID || receipt.State != "completed" || !jsonEquivalent(receipt.ResultSnapshot, snapshot) {
		return ErrUnavailable
	}
	return nil
}

func (service *CommerceExternalPushService) appendEvent(ctx context.Context, eventType string, productID productport.ID, kind productport.ExternalPushProductKind, actor int64, keyDigest [32]byte, fields map[string]any) error {
	if service == nil || service.events == nil {
		return ErrUnavailable
	}
	if fields == nil {
		fields = map[string]any{}
	}
	fields["product_id"] = productID
	fields["product_kind"] = kind
	fields["actor"] = actor
	payload, err := json.Marshal(fields)
	if err != nil {
		return ErrUnavailable
	}
	_, err = service.events.Append(ctx, productport.Event{
		Type: eventType, Payload: payload, OccurredAt: service.now().UTC(),
		IdempotencyKey: eventType + ":" + hex.EncodeToString(keyDigest[:]),
	})
	return err
}

func commerceExternalPushReservation(operation string, actor int64, key string, payload [32]byte, now time.Time) Reservation {
	return Reservation{Operation: operation, ActorScope: fmt.Sprintf("admin:%d", actor), KeyDigest: sha256.Sum256([]byte(key)), PayloadDigest: payload, CreatedAt: now}
}

func commerceExternalPushSaveDigest(command productport.SaveExternalPushConfigurationCommand) [32]byte {
	payload, _ := json.Marshal(struct {
		ProductID              productport.ID                      `json:"product_id"`
		ProductKind            productport.ExternalPushProductKind `json:"product_kind"`
		Enabled                bool                                `json:"enabled"`
		ConfigurationReference string                              `json:"configuration_reference"`
		BusinessParametersSet  bool                                `json:"business_parameters_set"`
		PushType               string                              `json:"type"`
		Day                    *int64                              `json:"day"`
		Frequency              *int64                              `json:"frequency"`
		ExpiresAtTS            *int64                              `json:"expires_at_ts"`
		Remark                 string                              `json:"remark"`
		CustomParams           map[string]any                      `json:"custom_params"`
		ExpectedRevision       int64                               `json:"expected_revision"`
		URL                    *string                             `json:"url,omitempty"`
		FieldMappingUpdate     any                                 `json:"field_mapping_update,omitempty"`
	}{command.ProductID, command.ProductKind, command.Enabled, command.ConfigurationReference, command.BusinessParametersSet, command.PushType, command.Day, command.Frequency, command.ExpiresAtTS, command.Remark, command.CustomParams, command.ExpectedRevision, command.URL, fieldMappingUpdate(command)})
	return sha256.Sum256(payload)
}

// commerceExternalPushLegacySaveDigest preserves the exact main@8ec5072
// receipt contract. That released Host saved only the opaque binding; the V3
// business fields were added later and must not change an original-key replay.
func commerceExternalPushLegacySaveDigest(command productport.SaveExternalPushConfigurationCommand) [32]byte {
	payload, _ := json.Marshal(struct {
		ProductID              productport.ID                      `json:"product_id"`
		ProductKind            productport.ExternalPushProductKind `json:"product_kind"`
		Enabled                bool                                `json:"enabled"`
		ConfigurationReference string                              `json:"configuration_reference"`
	}{command.ProductID, command.ProductKind, command.Enabled, command.ConfigurationReference})
	return sha256.Sum256(payload)
}

func commerceExternalPushLegacySaveReplay(command productport.SaveExternalPushConfigurationCommand, receipt Receipt, legacyDigest [32]byte) bool {
	// A completed main@8ec receipt represents the old binding-only command. A
	// post-0095 business save is a different request even if a browser reuses
	// its key.
	return command.URL == nil && !command.FieldMappingSet && !command.BusinessParametersSet && command.ExpiresAtTS == nil &&
		subtle.ConstantTimeCompare(receipt.PayloadDigest[:], legacyDigest[:]) == 1
}

func commerceExternalPushTestDigest(command productport.QueueExternalPushTestCommand) [32]byte {
	payload, _ := json.Marshal(struct {
		ProductID   productport.ID                      `json:"product_id"`
		ProductKind productport.ExternalPushProductKind `json:"product_kind"`
	}{command.ProductID, command.ProductKind})
	return sha256.Sum256(payload)
}

func commerceExternalPushConfigurationDigest(value productport.ExternalPushConfiguration) [32]byte {
	payload, _ := json.Marshal(struct {
		ProductID              productport.ID                      `json:"product_id"`
		ProductKind            productport.ExternalPushProductKind `json:"product_kind"`
		Enabled                bool                                `json:"enabled"`
		ConfigurationReference string                              `json:"configuration_reference"`
		PushType               string                              `json:"type"`
		Day                    *int64                              `json:"day"`
		Frequency              *int64                              `json:"frequency"`
		ExpiresAtTS            *int64                              `json:"expires_at_ts"`
		Remark                 string                              `json:"remark"`
		CustomParams           map[string]any                      `json:"custom_params"`
		Revision               int64                               `json:"revision"`
		FieldMapping           *productport.FieldMapping           `json:"field_mapping,omitempty"`
	}{value.ProductID, value.ProductKind, value.Enabled, value.ConfigurationReference, value.PushType, value.Day, value.Frequency, value.ExpiresAtTS, value.Remark, value.CustomParams, value.Revision, value.FieldMapping})
	return sha256.Sum256(payload)
}

func validSaveCommerceExternalPush(command productport.SaveExternalPushConfigurationCommand) bool {
	if command.ProductID < 1 || !validExternalPushKind(command.ProductKind) || command.Actor < 1 || !validIdempotencyKey(command.IdempotencyKey) || command.ExpectedRevision < 0 {
		return false
	}
	if command.FieldMappingSet && (!command.BusinessParametersSet || (command.FieldMapping != nil && productport.ValidateFieldMapping(command.FieldMapping) != nil)) {
		return false
	}
	reference := command.ConfigurationReference
	if command.URL != nil {
		if !command.BusinessParametersSet || strings.TrimSpace(*command.URL) != *command.URL || len(*command.URL) > 4096 || (command.Enabled && *command.URL == "") {
			return false
		}
		if *command.URL != "" {
			destination, e := url.Parse(*command.URL)
			if e != nil || destination.Scheme != "https" || destination.Hostname() == "" || destination.User != nil || destination.Fragment != "" {
				return false
			}
		}
		if command.Enabled {
			reference = "url-managed"
		} else {
			reference = ""
		}
	}
	value := productport.ExternalPushConfiguration{ProductID: command.ProductID, ProductKind: command.ProductKind, Enabled: command.Enabled, ConfigurationReference: reference, Revision: 1, UpdatedAt: time.Unix(1, 0)}
	if command.BusinessParametersSet {
		value.PushType, value.Day, value.Frequency, value.ExpiresAtTS, value.Remark = command.PushType, command.Day, command.Frequency, command.ExpiresAtTS, command.Remark
		value.CustomParams = command.CustomParams
	}
	return validExternalPushConfiguration(value, command.ProductID, command.ProductKind)
}

func validQueueCommerceExternalPushTest(command productport.QueueExternalPushTestCommand) bool {
	return command.ProductID > 0 && validExternalPushKind(command.ProductKind) && command.Actor > 0 && validIdempotencyKey(command.IdempotencyKey)
}

func validExternalPushKind(value productport.ExternalPushProductKind) bool {
	return value == productport.ExternalPushWeChatPay || value == productport.ExternalPushServicePeriod
}

func validExternalPushConfiguration(value productport.ExternalPushConfiguration, productID productport.ID, kind productport.ExternalPushProductKind) bool {
	if value.ProductID != productID || value.ProductKind != kind || productID < 1 || !validExternalPushKind(kind) || value.Revision < 0 || value.UpdatedAt.IsZero() {
		return false
	}
	if value.FieldMapping != nil && productport.ValidateFieldMapping(value.FieldMapping) != nil {
		return false
	}
	if !validCommerceExternalPushBusiness(value) {
		return false
	}
	if !value.Enabled {
		return value.ConfigurationReference == ""
	}
	return validCommerceExternalPushReference(value.ConfigurationReference)
}

func validCommerceExternalPushBusiness(value productport.ExternalPushConfiguration) bool {
	if !utf8.ValidString(value.PushType) || !utf8.ValidString(value.Remark) || strings.TrimSpace(value.PushType) != value.PushType || strings.TrimSpace(value.Remark) != value.Remark || utf8.RuneCountInString(value.PushType) > 200 || utf8.RuneCountInString(value.Remark) > 2000 || (value.Day != nil && *value.Day < 0) || (value.Frequency != nil && *value.Frequency < 0) || (value.ExpiresAtTS != nil && *value.ExpiresAtTS < 0) || len(value.CustomParams) > 64 {
		return false
	}
	raw, err := json.Marshal(value.CustomParams)
	if err != nil || len(raw) > 32768 {
		return false
	}
	for key, custom := range value.CustomParams {
		if !utf8.ValidString(key) || strings.TrimSpace(key) != key || key == "" || utf8.RuneCountInString(key) > 128 || strings.ContainsFunc(key, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
			return false
		}
		raw, err := json.Marshal(custom)
		if err != nil || !json.Valid(raw) || len(raw) > 4096 {
			return false
		}
	}
	return true
}

func cloneCommerceExternalPushParams(source map[string]any) map[string]any {
	if source == nil {
		return map[string]any{}
	}
	raw, err := json.Marshal(source)
	if err != nil {
		return nil
	}
	var out map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&out) != nil {
		return nil
	}
	return out
}

func sameCommerceExternalPushBusiness(left, right productport.ExternalPushConfiguration) bool {
	if !productport.EqualFieldMappings(left.FieldMapping, right.FieldMapping) {
		return false
	}
	if left.PushType != right.PushType || left.Remark != right.Remark || !sameCommerceExternalPushInteger(left.Day, right.Day) || !sameCommerceExternalPushInteger(left.Frequency, right.Frequency) || !sameCommerceExternalPushInteger(left.ExpiresAtTS, right.ExpiresAtTS) {
		return false
	}
	leftRaw, leftErr := json.Marshal(left.CustomParams)
	rightRaw, rightErr := json.Marshal(right.CustomParams)
	return leftErr == nil && rightErr == nil && jsonEquivalent(leftRaw, rightRaw)
}

func sameCommerceExternalPushInteger(left, right *int64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func validCommerceExternalPushReference(value string) bool {
	if value == "" || !utf8.ValidString(value) || utf8.RuneCountInString(value) > 128 || strings.TrimSpace(value) != value || strings.Contains(value, "://") {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || strings.ContainsRune("-_.:", character) {
			continue
		}
		return false
	}
	return true
}

func validExternalPushTest(value productport.ExternalPushTest, productID productport.ID, kind productport.ExternalPushProductKind) bool {
	return validStoredCommerceExternalPushTest(value, productID, kind) &&
		(value.State == "accepted" || value.State == "queued") && value.AttemptCount == 0 && !value.ProviderAccepted && !value.DeliveryProven && !value.RealExternalCallExecuted && !value.AutoRetryAllowed
}

func validStoredCommerceExternalPushTest(value productport.ExternalPushTest, productID productport.ID, kind productport.ExternalPushProductKind) bool {
	return value.ProductID == productID && value.ProductKind == kind && validExternalPushKind(kind) && validCommerceExternalPushEffectID(value.EffectID) && !value.CreatedAt.IsZero()
}

func validCommerceExternalPushTestStatus(value productport.ExternalPushTestStatus, effectID string) bool {
	if value.EffectID != effectID || !validCommerceExternalPushEffectID(value.EffectID) || value.AttemptCount < 0 || value.UpdatedAt.IsZero() {
		return false
	}
	switch value.State {
	case "accepted", "queued", "attempted", "provider_accepted", "final_failed", "outcome_unknown", "reconciled":
		return !value.RealExternalCallExecuted || value.ProviderCallAttempted
	default:
		return false
	}
}

func validCommerceExternalPushTestDeliveryID(value string) bool {
	if !strings.HasPrefix(value, "commerce_test_") || len(value) != len("commerce_test_")+32 {
		return false
	}
	for _, character := range value[len("commerce_test_"):] {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func validCommerceExternalPushEffectID(value string) bool {
	if !strings.HasPrefix(value, "eer_") || len(value) < 5 {
		return false
	}
	for index, character := range value[4:] {
		if character < '0' || character > '9' || index == 0 && character == '0' {
			return false
		}
	}
	return true
}

func validCommerceExternalPushReceipt(receipt Receipt, reservation Reservation) bool {
	return receipt.ID > 0 && receipt.Operation == reservation.Operation && receipt.ActorScope == reservation.ActorScope &&
		subtle.ConstantTimeCompare(receipt.KeyDigest[:], reservation.KeyDigest[:]) == 1 &&
		(receipt.State == "in_progress" || receipt.State == "completed")
}

func decodeCommerceExternalPushSnapshot(raw json.RawMessage, target *productport.ExternalPushConfiguration, productID productport.ID, kind productport.ExternalPushProductKind) error {
	if target == nil {
		return ErrUnavailable
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(target) != nil || !validExternalPushConfiguration(*target, productID, kind) {
		return ErrUnavailable
	}
	canonical, err := json.Marshal(*target)
	if err != nil || (!jsonEquivalent(canonical, raw) && !commerceExternalPushLegacySnapshotEquivalent(raw, *target)) {
		return ErrUnavailable
	}
	return nil
}

// commerceExternalPushLegacySnapshotEquivalent accepts exactly the completed
// main@8ec5072 configuration shape. It does not normalize damaged snapshots
// or accept a partial version of a later business-parameter snapshot.
func commerceExternalPushLegacySnapshotEquivalent(raw json.RawMessage, value productport.ExternalPushConfiguration) bool {
	if value.Revision != 0 || value.PushType != "" || value.Day != nil || value.Frequency != nil || value.ExpiresAtTS != nil || value.Remark != "" || value.CustomParams != nil {
		return false
	}
	legacy, err := json.Marshal(struct {
		ProductID              productport.ID                      `json:"product_id"`
		ProductKind            productport.ExternalPushProductKind `json:"product_kind"`
		Enabled                bool                                `json:"enabled"`
		ConfigurationReference string                              `json:"configuration_reference,omitempty"`
		UpdatedAt              time.Time                           `json:"updated_at"`
	}{value.ProductID, value.ProductKind, value.Enabled, value.ConfigurationReference, value.UpdatedAt})
	return err == nil && jsonEquivalent(legacy, raw)
}

func decodeCommerceExternalPushTestSnapshot(raw json.RawMessage, target *productport.ExternalPushTest, productID productport.ID, kind productport.ExternalPushProductKind) error {
	if target == nil || json.Unmarshal(raw, target) != nil || !validExternalPushTest(*target, productID, kind) {
		return ErrUnavailable
	}
	canonical, err := json.Marshal(*target)
	if err != nil || !jsonEquivalent(canonical, raw) {
		return ErrUnavailable
	}
	return nil
}

func commerceExternalPushReady(service *CommerceExternalPushService) bool {
	return service != nil && service.uow != nil && service.store != nil && service.events != nil && service.now != nil
}

func classifyCommerceExternalPush(err error) error {
	switch {
	case errors.Is(err, outboundport.ErrCommercePushEndpointInvalid):
		return ErrInvalidProduct
	case errors.Is(err, ErrInvalidProduct), errors.Is(err, ErrNotFound), errors.Is(err, ErrConflict), errors.Is(err, ErrExternalPushNotConfigured):
		return err
	case errors.Is(err, productport.ErrProductReadNotFound):
		return ErrNotFound
	case errors.Is(err, productport.ErrProductConflict):
		return ErrConflict
	default:
		return ErrUnavailable
	}
}

func fieldMappingUpdate(command productport.SaveExternalPushConfigurationCommand) any {
	if !command.FieldMappingSet {
		return nil
	}
	return struct {
		Mapping *productport.FieldMapping `json:"mapping"`
	}{command.FieldMapping}
}

// PreviewLegacyExternalPushConfiguration reads only a synthetic legacy payload.
// The Outbound owner resolves its own protocol and performs no external call.
func (service *CommerceExternalPushService) PreviewLegacyExternalPushConfiguration(ctx context.Context, productID productport.ID) (json.RawMessage, error) {
	previewer, ok := service.effects.(outboundport.CommercePushLegacyPreviewer)
	if !ok || ctx == nil || productID < 1 {
		return nil, ErrUnavailable
	}
	var payload json.RawMessage
	err := service.uow.Within(ctx, func(tx context.Context) error {
		var e error
		payload, e = previewer.PreviewLegacyCommercePushWithin(tx, int64(productID))
		return e
	})
	return payload, err
}
