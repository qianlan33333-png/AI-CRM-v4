package app

import (
	"context"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
)

func (s *Service) ReadStandardPurchaseWithin(ctx context.Context, q orderport.StandardPurchaseQuery) (orderport.StandardPurchaseState, error) {
	r, ok := s.store.(orderport.StandardPurchaseReader)
	if !ok {
		return orderport.StandardPurchaseState{}, orderport.ErrUnavailable
	}
	return r.ReadStandardPurchaseWithin(ctx, q)
}
