package app

import (
	"context"
	"crypto/sha256"
	"errors"

	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	referralport "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/port"
)

// ConsumePaidEventWithin is the composition adapter for Order's existing
// durable PaidEvent. It resolves the Referral-owned checkout snapshot in the
// caller's transaction, then invokes the product sale consumer without a
// nested Unit of Work.
func (s *Service) ConsumePaidEventWithin(ctx context.Context, event orderport.PaidEvent) error {
	if s == nil || s.store == nil || !event.Valid() {
		return referralport.ErrConflict
	}
	store, ok := s.store.(productSalesStore)
	if !ok {
		return referralport.ErrUnavailable
	}
	checkout, err := store.ReadProductSaleCheckoutWithin(ctx, event.OrderID, true)
	if errors.Is(err, referralport.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	// The checkout snapshot records the Order version at checkout creation.
	// Payment settlement necessarily advances the native Order version before
	// this paid event is emitted, so equality would reject every legitimate
	// callback. A snapshot from a future version is the only impossible fact.
	if !checkoutOrderVersionCompatible(checkout.OrderVersion, event.OrderVersion) || checkout.ProductID != event.CheckoutProductID || checkout.ProductType != event.CheckoutProductType || checkout.ActivityContextDigest != event.ReferralActivityContextDigest || checkout.PromotionContextDigest != event.PromotionContextDigest {
		return referralport.ErrConflict
	}
	if event.Order.PayerCustomerID == nil || event.Order.BeneficiaryCustomerID == nil || *event.Order.PayerCustomerID < 1 || *event.Order.BeneficiaryCustomerID < 1 {
		return referralport.ErrConflict
	}
	return s.consumeProductSalePaidWithin(ctx, referralport.ProductSalePaidEvent{PaidEventID: event.ID, OrderID: event.OrderID, OrderVersion: event.OrderVersion, ProductID: checkout.ProductID, ProductType: checkout.ProductType, CampaignID: checkout.CampaignID, PromotionCustomerID: checkout.PromotionCustomerID, BuyerCustomerID: *event.Order.PayerCustomerID, BeneficiaryCustomerID: *event.Order.BeneficiaryCustomerID, PaidAmountMinor: event.Order.Amount.AmountMinor, Currency: event.Order.Amount.Currency, ActivityContextDigest: checkout.ActivityContextDigest, PromotionContextDigest: checkout.PromotionContextDigest, SourceDigest: event.SourceDigest, OccurredAt: event.OccurredAt})
}

func checkoutOrderVersionCompatible(checkoutVersion, paidEventVersion int64) bool {
	return checkoutVersion >= 1 && paidEventVersion >= checkoutVersion
}

// ConsumeRefundSettlementWithin adapts Order's confirmed cumulative refund
// delta. Payment's receipt key is retained as the immutable reversal key.
func (s *Service) ConsumeRefundSettlementWithin(ctx context.Context, event orderport.RefundSettlementEvent) error {
	if s == nil || s.store == nil || !event.Valid() {
		return referralport.ErrConflict
	}
	if event.CheckoutProductID < 1 || event.CheckoutProductType == "" {
		return nil
	}
	var buyer, beneficiary int64
	if event.Order.PayerCustomerID == nil || event.Order.BeneficiaryCustomerID == nil {
		return referralport.ErrConflict
	}
	buyer, beneficiary = *event.Order.PayerCustomerID, *event.Order.BeneficiaryCustomerID
	source := sha256.Sum256([]byte("order.refund.v1:" + event.ReceiptKey))
	return s.consumeProductSaleRefundWithin(ctx, referralport.ProductSaleRefundEvent{OrderID: event.Order.ID, ProductID: event.CheckoutProductID, ProductType: event.CheckoutProductType, RefundedAmountMinor: event.RefundedDelta, BuyerCustomerID: buyer, BeneficiaryCustomerID: beneficiary, ReceiptKey: event.ReceiptKey, SourceDigest: source, OccurredAt: event.OccurredAt})
}

var _ orderport.PaidEventConsumer = (*Service)(nil)
var _ orderport.RefundSettlementConsumer = (*Service)(nil)
