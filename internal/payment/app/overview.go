package app

import (
	"context"
	"errors"
	"fmt"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

type overviewStore interface {
	ReadPaidOverview(context.Context, paymentport.OverviewWindow) (paymentport.PaidOverview, error)
	ReadPaidOverviewPayerPage(context.Context, paymentport.OverviewWindow, customerdomain.CustomerID, int) (paymentport.PaidOverviewPayerPage, error)
	ReadPaidOverviewRecords(context.Context, paymentport.OverviewWindow, *paymentport.PaidOverviewRecordCursor, int) (paymentport.PaidOverviewRecordPage, error)
	ReadRefundOverview(context.Context, paymentport.OverviewWindow) (paymentport.RefundOverview, error)
}

const paidOverviewPayerPageLimit = 500

type OverviewReader struct {
	uow       platformport.UnitOfWork
	store     overviewStore
	canonical identityport.CanonicalCustomerRootsReader
}

func NewOverviewReader(uow platformport.UnitOfWork, store overviewStore, canonical identityport.CanonicalCustomerRootsReader) (*OverviewReader, error) {
	if uow == nil || store == nil || canonical == nil {
		return nil, paymentport.ErrUnavailable
	}
	return &OverviewReader{uow: uow, store: store, canonical: canonical}, nil
}

func (reader *OverviewReader) ReadPaidOverview(ctx context.Context, window paymentport.OverviewWindow) (paymentport.PaidOverview, error) {
	if reader == nil || reader.uow == nil || reader.store == nil || reader.canonical == nil || !window.Valid() {
		return paymentport.PaidOverview{}, paymentport.ErrUnavailable
	}
	var result paymentport.PaidOverview
	err := reader.uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		result, readErr = reader.store.ReadPaidOverview(tx, window)
		if readErr != nil {
			return readErr
		}
		result.DistinctCanonicalPayers, readErr = reader.readCanonicalPayerCount(tx, window)
		if readErr == nil || errors.Is(readErr, context.Canceled) || errors.Is(readErr, context.DeadlineExceeded) {
			return readErr
		}
		return fmt.Errorf("%w: %v", paymentport.ErrCanonicalPayerUnavailable, readErr)
	})
	return result, err
}

// ReadPaidOverviewRecords keeps the paid-detail page inside Payment's narrow
// repeatable-read reporting boundary. It neither resolves historical payers
// nor accesses Order: a provider/reference pair remains the safe locator for
// the separately owned Order detail Host.
func (reader *OverviewReader) ReadPaidOverviewRecords(ctx context.Context, window paymentport.OverviewWindow, after *paymentport.PaidOverviewRecordCursor, limit int) (paymentport.PaidOverviewRecordPage, error) {
	if reader == nil || reader.uow == nil || reader.store == nil || !window.Valid() || limit < 1 || limit > 100 || (after != nil && !after.Valid()) {
		return paymentport.PaidOverviewRecordPage{}, paymentport.ErrInvalid
	}
	var result paymentport.PaidOverviewRecordPage
	err := reader.uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		result, readErr = reader.store.ReadPaidOverviewRecords(tx, window, after, limit)
		return readErr
	})
	return result, err
}

func (reader *OverviewReader) readCanonicalPayerCount(ctx context.Context, window paymentport.OverviewWindow) (int64, error) {
	after := customerdomain.CustomerID(0)
	roots := map[customerdomain.CustomerID]struct{}{}
	for {
		page, err := reader.store.ReadPaidOverviewPayerPage(ctx, window, after, paidOverviewPayerPageLimit)
		if err != nil {
			return 0, err
		}
		if len(page.CustomerIDs) == 0 {
			return int64(len(roots)), nil
		}
		for _, customerID := range page.CustomerIDs {
			if customerID < 1 || customerID <= after {
				return 0, paymentport.ErrInvalid
			}
			after = customerID
		}
		resolved, err := reader.canonical.CanonicalCustomerRoots(ctx, page.CustomerIDs)
		if err != nil {
			return 0, err
		}
		for _, customerID := range page.CustomerIDs {
			root, exists := resolved[customerID]
			if !exists || root < 1 {
				return 0, paymentport.ErrUnavailable
			}
			roots[root] = struct{}{}
		}
		if len(page.CustomerIDs) < paidOverviewPayerPageLimit {
			return int64(len(roots)), nil
		}
	}
}

func (reader *OverviewReader) ReadRefundOverview(ctx context.Context, window paymentport.OverviewWindow) (paymentport.RefundOverview, error) {
	if reader == nil || reader.uow == nil || reader.store == nil || !window.Valid() {
		return paymentport.RefundOverview{}, paymentport.ErrUnavailable
	}
	var result paymentport.RefundOverview
	err := reader.uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		result, readErr = reader.store.ReadRefundOverview(tx, window)
		return readErr
	})
	return result, err
}

var _ paymentport.OverviewReader = (*OverviewReader)(nil)
