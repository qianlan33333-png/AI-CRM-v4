package app

import (
	"context"
	"fmt"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
)

func (s *Service) SetCollectedPhoneRecorder(recorder customerport.CollectedPhoneRecorder) error {
	if s == nil || recorder == nil {
		return orderport.ErrConflict
	}
	s.collectedPhones = recorder
	return nil
}

type collectedPhoneStore interface {
	ListCollectedPhoneOrdersWithin(context.Context, int64, int64) ([]int64, error)
}

// CollectedPhoneOrderIDsWithin is an internal maintenance cursor over source
// facts. No order/payment/product status is used as a filter.
func (s *Service) CollectedPhoneOrderIDsWithin(ctx context.Context, after, through int64) ([]int64, error) {
	store, ok := s.store.(collectedPhoneStore)
	if !ok {
		return nil, orderport.ErrUnavailable
	}
	return store.ListCollectedPhoneOrdersWithin(ctx, after, through)
}

func (s *Service) ReplayCollectedPhoneWithin(ctx context.Context, id int64) (identityport.DeclaredAttachResult, error) {
	if s == nil || s.collectedPhones == nil {
		return identityport.DeclaredAttachResult{}, orderport.ErrUnavailable
	}
	order, err := s.store.Get(ctx, id, false)
	if err != nil {
		return identityport.DeclaredAttachResult{}, err
	}
	snap := order.Snapshot()
	phone, found, err := s.ReadCheckoutMobileWithin(ctx, id)
	if err != nil {
		return identityport.DeclaredAttachResult{}, err
	}
	if !found || snap.PayerCustomerID == nil {
		return identityport.DeclaredAttachResult{Status: identityport.DeclaredInvalid}, nil
	}
	return s.collectedPhones.CollectPhoneWithin(ctx, customerport.CollectedPhoneCommand{DeclaredPhoneCommand: identityport.DeclaredPhoneCommand{
		CustomerID: customerdomain.CustomerID(*snap.PayerCustomerID), Phone: phone, Source: "order", SourceEventID: fmt.Sprintf("order:%d:contact", id), IdempotencyKey: fmt.Sprintf("order-phone:%d", id)}, CollectedAt: snap.CreatedAt, Historical: true})
}
