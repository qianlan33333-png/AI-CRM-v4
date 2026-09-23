package app

import (
	"context"
	"errors"

	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

type HistoricalTagStore interface {
	ApplyHistoricalTagRecords(context.Context, customerport.HistoricalTagBatch, []customerport.HistoricalTagRecord) (customerport.HistoricalTagImportResult, error)
	VerifyHistoricalTagRecords(context.Context, customerport.HistoricalTagBatch, []customerport.HistoricalTagRecord) (customerport.HistoricalTagImportResult, error)
}

type HistoricalTagImportService struct {
	UOW   platformport.UnitOfWork
	Store HistoricalTagStore
}

func (service HistoricalTagImportService) ApplyHistoricalTagRecords(ctx context.Context, batch customerport.HistoricalTagBatch, records []customerport.HistoricalTagRecord) (customerport.HistoricalTagImportResult, error) {
	if service.UOW == nil || service.Store == nil {
		return customerport.HistoricalTagImportResult{}, errors.New("customer tag history importer unavailable")
	}
	var result customerport.HistoricalTagImportResult
	err := service.UOW.Within(ctx, func(tx context.Context) error {
		var applyErr error
		result, applyErr = service.Store.ApplyHistoricalTagRecords(tx, batch, records)
		return applyErr
	})
	return result, err
}

func (service HistoricalTagImportService) VerifyHistoricalTagRecords(ctx context.Context, batch customerport.HistoricalTagBatch, records []customerport.HistoricalTagRecord) (customerport.HistoricalTagImportResult, error) {
	if service.UOW == nil || service.Store == nil {
		return customerport.HistoricalTagImportResult{}, errors.New("customer tag history importer unavailable")
	}
	var result customerport.HistoricalTagImportResult
	err := service.UOW.Within(ctx, func(tx context.Context) error {
		var verifyErr error
		result, verifyErr = service.Store.VerifyHistoricalTagRecords(tx, batch, records)
		return verifyErr
	})
	return result, err
}

var _ customerport.HistoricalTagImporter = HistoricalTagImportService{}
