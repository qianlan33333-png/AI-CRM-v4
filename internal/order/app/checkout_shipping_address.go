package app

import (
	"context"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
)

func (s *Service) ReadCheckoutShippingAddressWithin(ctx context.Context, orderID int64) (orderport.ShippingAddress, bool, error) {
	if s == nil || orderID < 1 {
		return orderport.ShippingAddress{}, false, orderport.ErrConflict
	}
	reader, ok := s.store.(interface {
		ReadShippingAddressSnapshot(context.Context, int64) (orderport.ShippingAddress, bool, error)
	})
	if !ok {
		return orderport.ShippingAddress{}, false, orderport.ErrUnavailable
	}
	value, found, err := reader.ReadShippingAddressSnapshot(ctx, orderID)
	if err != nil {
		return orderport.ShippingAddress{}, false, orderport.ErrUnavailable
	}
	return value, found, nil
}
