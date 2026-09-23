package app

import (
	"context"

	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

// OverviewReader exposes the Customer-side overview contract while preserving
// Identity as the sole owner of canonical roots and their provenance. It has
// no Customer-store dependency and therefore cannot cross-read Identity
// tables from Customer persistence.
type OverviewReader struct {
	uow        platformport.UnitOfWork
	identities identityport.CanonicalCustomerOverviewReader
}

func NewOverviewReader(uow platformport.UnitOfWork, identities identityport.CanonicalCustomerOverviewReader) (*OverviewReader, error) {
	if uow == nil || identities == nil {
		return nil, customerport.ErrSectionUnavailable
	}
	return &OverviewReader{uow: uow, identities: identities}, nil
}

func (reader *OverviewReader) ReadNewCustomerOverview(ctx context.Context, window customerport.OverviewWindow) (customerport.NewCustomerOverview, error) {
	if reader == nil || reader.uow == nil || reader.identities == nil || !window.Valid() {
		return customerport.NewCustomerOverview{}, customerport.ErrSectionUnavailable
	}
	var result identityport.NewCanonicalCustomerOverview
	err := reader.uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		result, readErr = reader.identities.ReadNewCanonicalCustomerOverview(tx, identityport.OverviewWindow{Start: window.Start, End: window.End})
		return readErr
	})
	if err != nil {
		return customerport.NewCustomerOverview{}, err
	}
	return customerport.NewCustomerOverview{
		KnownNewCanonicalCustomers: result.KnownNewCanonicalCustomers,
		HistoricalExcluded:         result.HistoricalExcluded,
		UnknownSource:              result.UnknownSource,
		Evidence:                   result.Evidence,
	}, nil
}

var _ customerport.OverviewReader = (*OverviewReader)(nil)
