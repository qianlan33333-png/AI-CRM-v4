package port

import (
	"context"
	"time"
)

type OverviewWindow struct {
	Start time.Time
	End   time.Time
}

func (w OverviewWindow) Valid() bool {
	return !w.Start.IsZero() && !w.End.IsZero() && w.End.After(w.Start)
}

// Overview is a CNY-only Distribution read model. Period performance is
// based on Distribution's immutable paid_confirmed_at; settlement balances
// are explicitly current-state figures and are not constrained by period.
type Overview struct {
	PeriodPaidSalesMinor    int64
	PeriodInitialCommission int64
	PeriodCommissionCount   int64
	CurrentUnsettledMinor   int64
	CurrentSettledMinor     int64
	// CurrentExceptionOrderCount is the current number of distinct order IDs
	// with at least one Distribution exception in open or querying status. It
	// intentionally differs from OpenExceptionCount, which remains a count of
	// exception records for the operating to-do list.
	CurrentExceptionOrderCount int64
	OpenExceptionCount         int64
	Currency                   string
}

// OverviewReader is the stable Distribution seam for the admin overview. Its
// implementation may query only Distribution-owned tables.
type OverviewReader interface {
	ReadOverview(context.Context, OverviewWindow) (Overview, error)
}
