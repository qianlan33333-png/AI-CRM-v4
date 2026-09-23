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

// NewCanonicalCustomerOverview separates roots whose initial verified source
// is a declared runtime creator from receipt-backed historical roots and roots
// whose origin cannot be proven. Unknown roots intentionally make the caller
// report data_missing while retaining the known value for inspection.
type NewCanonicalCustomerOverview struct {
	KnownNewCanonicalCustomers int64
	HistoricalExcluded         int64
	UnknownSource              int64
	Evidence                   string
}

// CanonicalCustomerOverviewReader is a read-only Identity seam. It neither
// resolves/provisions identities nor exposes identity values.
type CanonicalCustomerOverviewReader interface {
	ReadNewCanonicalCustomerOverview(context.Context, OverviewWindow) (NewCanonicalCustomerOverview, error)
}
