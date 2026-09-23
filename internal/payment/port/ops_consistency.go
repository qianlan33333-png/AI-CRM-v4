package port

import (
	"context"
	"time"
)

// OpsPaidOrderFact is transient evidence for a composed invariant check. IDs
// are existing local references, never identity matching inputs. The caller
// persists aggregate counts only, not these per-order/customer references.
type OpsPaidOrderFact struct {
	PaymentID, OrderID, AmountMinor, CompletedRefundMinor int64
	PayerCustomerID, BeneficiaryCustomerID                int64
	Provider, Currency                                    string
	PaidConfirmedAt                                       *time.Time
}
type OpsPaidOrderPage struct {
	Items []OpsPaidOrderFact
	More  bool
}

// OpsPaidOrderReader reads native paid payments only. Historical imports and
// pending payments are outside these atomic settlement invariants. The caller
// must provide one readonly repeatable-read snapshot shared with Order.
type OpsPaidOrderReader interface {
	ReadOpsPaidOrderPageWithin(context.Context, int64, int32) (OpsPaidOrderPage, error)
}
