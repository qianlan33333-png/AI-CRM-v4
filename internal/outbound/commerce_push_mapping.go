package outbound

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	orderdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
	"strconv"
	"strings"
	"time"
)

var ErrCommerceMappingSourceUnavailable = errors.New("commerce mapping source unavailable")

func (s *CommercePushService) SetFieldMappingReaders(mobile orderport.CheckoutMobileReader, names customerport.DirectoryDisplayNameReader) {
	s.checkoutMobile = mobile
	s.displayNames = names
}

func (s *CommercePushService) mappedPaidPayload(ctx context.Context, event orderport.PaidEvent, mapping *productport.FieldMapping) ([]byte, error) {
	variables := make(map[string]json.RawMessage)
	for _, field := range mapping.Fields {
		if field.Source != "variable" {
			continue
		}
		if _, exists := variables[field.Variable]; exists {
			continue
		}
		var value any
		switch field.Variable {
		case "order.paid_amount_minor":
			value = event.Order.Amount.AmountMinor
		case "order.mobile":
			if s.checkoutMobile == nil {
				return nil, ErrCommerceMappingSourceUnavailable
			}
			mobile, found, err := s.checkoutMobile.ReadCheckoutMobileWithin(ctx, event.OrderID)
			if err != nil {
				return nil, ErrCommerceMappingSourceUnavailable
			}
			if found {
				value = mobile
			}
		case "payer.nickname":
			if event.Order.PayerCustomerID != nil && *event.Order.PayerCustomerID > 0 {
				if s.displayNames == nil {
					return nil, ErrCommerceMappingSourceUnavailable
				}
				id := customerdomain.CustomerID(*event.Order.PayerCustomerID)
				names, err := s.displayNames.DisplayNames(ctx, []customerdomain.CustomerID{id})
				if err != nil {
					return nil, ErrCommerceMappingSourceUnavailable
				}
				if name, found := names[id]; found && name != "" {
					value = name
				}
			}
		default:
			return nil, productport.ErrFieldMappingVariableUnavailable
		}
		raw, err := json.Marshal(value)
		if err != nil {
			return nil, ErrCommerceMappingSourceUnavailable
		}
		variables[field.Variable] = raw
	}
	return productport.CompileFieldMapping(mapping, variables)
}

func commerceMappedSyntheticPayload(mapping *productport.FieldMapping) ([]byte, error) {
	// Synthetic values contain no live customer identity or order data. The body
	// uses the exact same compiler and includes no extra test/legacy fields.
	return productport.CompileFieldMapping(mapping, productport.SyntheticFieldMappingVariables())
}

func (s *CommercePushService) PreviewLegacyCommercePushWithin(ctx context.Context, productID int64) (json.RawMessage, error) {
	if s == nil || productID < 1 {
		return nil, ErrCommercePushInvalid
	}
	configuration, err := s.products.ReadExternalPushConfigurationForOrder(ctx, productport.ID(productID))
	if err != nil {
		return nil, err
	}
	target, found, err := s.targets.CommercePushTarget(ctx, configuration.ConfigurationReference)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, ErrCommercePushConflict
	}
	target = commercePushTargetWithProductBusiness(target, configuration)
	return commerceLegacyPaidPreviewPayload(productID, configuration.ProductName, target)
}

func commerceLegacyPaidPreviewPayload(productID int64, productName string, target CommercePushTarget) (json.RawMessage, error) {
	event := orderport.PaidEvent{OrderID: 1, OccurredAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	previewCustomerID := int64(1)
	event.Order = orderdomain.Snapshot{ID: 1, MerchantOrderNo: "sample-order", ProviderTransactionNo: "sample-transaction", PayerCustomerID: &previewCustomerID, BeneficiaryCustomerID: &previewCustomerID, Amount: orderdomain.Money{AmountMinor: 990, Currency: "CNY"}}
	item := orderdomain.ItemSnapshot{LineNo: 1, ProductID: &productID, ProductName: productName, UnitAmountMinor: 990}
	// The fixture reader supplies no identities and cannot invoke a Vault or
	// Provider. It preserves the legacy preview's payload shape without
	// weakening the real paid event's configured-phone gate.
	raw, _, err := (&CommercePushService{identities: commercePreviewIdentityReader{}}).paidPayload(context.Background(), event, item, target, "sample-delivery")
	return raw, err
}

type commercePreviewIdentityReader struct{}

func (commercePreviewIdentityReader) VerifiedExternalIdentityValue(context.Context, customerdomain.CustomerID, identitydomain.Kind, string) (string, bool, error) {
	return "", true, nil
}

func (commercePreviewIdentityReader) VerifiedOutboundPhone(context.Context, customerdomain.CustomerID, string) (string, bool, error) {
	return "", true, nil
}

func commerceMappingMode(mapping *productport.FieldMapping) string {
	if mapping == nil {
		return "legacy"
	}
	return "custom_fields_v1"
}

func commerceExecutionHeaderValues(execution CommercePushExecution, body []byte) (string, string, bool) {
	if execution.PayloadMode == "" || execution.PayloadMode == "legacy" {
		return commercePayloadHeaderValues(body)
	}
	if execution.PayloadMode != "custom_fields_v1" {
		return "", "", false
	}
	parts := strings.Split(execution.SourceReference, ":")
	if len(parts) == 4 && parts[0] == "order-paid" && parts[2] == "line" {
		eventID, e := strconv.ParseInt(parts[1], 10, 64)
		line, e2 := strconv.ParseInt(parts[3], 10, 32)
		if e != nil || e2 != nil || eventID < 1 || line < 1 {
			return "", "", false
		}
		return "transaction.paid", commerceDeliveryID(eventID, int32(line), execution.TargetSlot), true
	}
	if len(parts) == 2 && parts[0] == "synthetic" {
		raw, err := hex.DecodeString(parts[1])
		if err != nil || len(raw) != 32 {
			return "", "", false
		}
		var digest [32]byte
		copy(digest[:], raw)
		return "external_push.test", commerceDeliveryIDFromDigest(digest, execution.TargetSlot), true
	}
	return "", "", false
}
