package app

import (
	"context"

	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
)

func (s *Service) ReadCheckoutVariantWithin(ctx context.Context, id int64) (*orderport.VariantSnapshot, error) {
	if !ready(s) || id < 1 {
		return nil, orderport.ErrNotFound
	}
	order, err := s.store.Get(ctx, id, false)
	if err != nil {
		return nil, err
	}
	items := order.Items()
	if len(items) != 1 {
		return nil, nil
	}
	return items[0].Variant.Clone(), nil
}
