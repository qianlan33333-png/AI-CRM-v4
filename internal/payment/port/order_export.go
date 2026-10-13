package port

import (
	"context"
	"time"
)

// OrderExportPaymentReader exposes local payment facts only, to an already
// authorized Order export sharing its PostgreSQL transaction. No Provider call.
type OrderExportPaymentReader interface {
	ReadOrderExportPaymentsWithin(context.Context, []int64) (map[int64]OrderExportPayment, error)
}
type OrderExportPayment struct {
	PaidAt                                              *time.Time
	TransactionNo                                       string
	AmountMinor, SharedMinor                            int64
	SharingProcessing, SharingUnknown, SharingException bool
}
