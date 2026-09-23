package port

import "context"

// StandardPurchaseQuery is trusted Owner-to-Owner input, never a public body.
// ExcludedPendingOrderIDs contains only Payment-reviewed restart permissions.
type StandardPurchaseQuery struct {
	CustomerIDs             []int64
	ProductID               int64
	ProductCode             string
	ExcludedPendingOrderIDs []int64
	CurrentOrderID          int64
	Lock                    bool
}
type StandardPurchaseState struct {
	PaidOrderID     int64
	MerchantOrderNo string
	Owned           bool
	Pending         bool
}
type StandardPurchaseReader interface {
	ReadStandardPurchaseWithin(context.Context, StandardPurchaseQuery) (StandardPurchaseState, error)
}
