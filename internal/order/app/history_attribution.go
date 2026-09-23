package app

import (
	"context"

	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
)

type HistoricalAttributionStore interface {
	RecordHistoricalAttribution(context.Context, orderport.HistoricalAttributionCommand) (orderport.HistoricalAttributionResult, error)
}

type HistoricalAttributionService struct{ store HistoricalAttributionStore }

func NewHistoricalAttributionService(store HistoricalAttributionStore) *HistoricalAttributionService {
	return &HistoricalAttributionService{store: store}
}

func (service *HistoricalAttributionService) RecordHistoricalAttributionWithin(ctx context.Context, command orderport.HistoricalAttributionCommand) (orderport.HistoricalAttributionResult, error) {
	if service == nil || service.store == nil {
		return orderport.HistoricalAttributionResult{}, orderport.ErrUnavailable
	}
	result, err := service.store.RecordHistoricalAttribution(ctx, command)
	if err != nil {
		return orderport.HistoricalAttributionResult{}, classify(err)
	}
	return result, nil
}
