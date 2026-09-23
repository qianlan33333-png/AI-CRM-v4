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

// NewCustomerOverview is the Customer-facing, canonical-root-only contract
// bridged from Identity in composition. Customer stores never inspect
// Identity-owned customer or identity tables for this aggregate.
type NewCustomerOverview struct {
	KnownNewCanonicalCustomers int64
	HistoricalExcluded         int64
	UnknownSource              int64
	Evidence                   string
}

type OverviewReader interface {
	ReadNewCustomerOverview(context.Context, OverviewWindow) (NewCustomerOverview, error)
}
