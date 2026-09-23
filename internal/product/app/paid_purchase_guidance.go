package app

import (
	"context"
	"errors"
	orderdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

func (s *PaidPurchaseActionService) SetPaidGuidanceOrderReader(orders orderport.Query) {
	s.guidanceOrders = orders
}

// ReadPaidPurchaseGuidance returns a presentation projection only. It always
// rechecks the current Order settlement before exposing a stored action: a
// full refund revokes every QR, redirect and URL Link result. Missing/legacy
// none snapshots may then use today's explicit configuration after rechecking
// the exact Product reference. It never stores a snapshot, consumes a paid
// event, or submits tag/push work.
func (s *PaidPurchaseActionService) ReadPaidPurchaseGuidance(ctx context.Context, orderID int64) (productport.PaidPurchaseAction, error) {
	original, err := s.ReadPaidPurchaseAction(ctx, orderID)
	if err != nil && !errors.Is(err, productport.ErrProductReadNotFound) && !errors.Is(err, ErrNotFound) {
		return productport.PaidPurchaseAction{}, err
	}
	if s.guidanceOrders == nil {
		return original, err
	}
	order, readErr := s.guidanceOrders.Get(ctx, orderID)
	if readErr != nil {
		return productport.PaidPurchaseAction{}, readErr
	}
	if order.ID != orderID || (order.Status != orderdomain.StatusPaid && order.Status != orderdomain.StatusPartiallyRefunded) || (order.RefundedMinor > 0 && order.RefundedMinor >= order.Amount.AmountMinor) || len(order.Items) != 1 {
		return productport.PaidPurchaseAction{}, productport.ErrProductReadUnavailable
	}
	if err == nil && (original.Mode != productport.PaidPurchaseActionNone || original.CheckoutSnapshot) {
		return original, nil
	}
	item := order.Items[0]
	if item.ProductCode == "" {
		return productport.PaidPurchaseAction{}, productport.ErrProductReadUnavailable
	}
	reader, ok := s.store.(interface {
		Get(context.Context, productport.ID) (productport.Product, error)
		GetByCode(context.Context, string) (productport.Product, error)
	})
	if !ok {
		return productport.PaidPurchaseAction{}, productport.ErrProductReadUnavailable
	}
	var result productport.PaidPurchaseAction
	readErr = s.uow.Within(ctx, func(tx context.Context) error {
		var product productport.Product
		var e error
		if item.ProductID != nil {
			product, e = reader.Get(tx, productport.ID(*item.ProductID))
		} else {
			product, e = reader.GetByCode(tx, item.ProductCode)
		}
		if e != nil {
			return e
		}
		if product.ProductCode != item.ProductCode || (item.ProductID != nil && int64(product.ID) != *item.ProductID) {
			return productport.ErrProductReadUnavailable
		}
		config, e := paidPurchaseConfigFromProjection(product.LegacyAdminProjection)
		if e != nil {
			return e
		}
		result = productport.PaidPurchaseAction{OrderID: orderID, ProductID: product.ID, ProductVersion: product.Version, Enabled: config.Enabled, Mode: config.Mode, LeadChannelID: config.LeadChannelID, LeadQRTitle: config.LeadQRTitle, LeadQRSubtitle: config.LeadQRSubtitle, RedirectURL: config.RedirectURL, CompletionTarget: config.CompletionTarget}
		return nil
	})
	return result, readErr
}
