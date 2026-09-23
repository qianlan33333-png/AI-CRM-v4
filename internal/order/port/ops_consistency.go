package port

import "context"

// OpsPaymentOrderFact exposes only local settlement/fulfillment evidence. A
// missing checkout is a legitimate legacy evidence gap; standard products do
// not imply an external push or any other delivery obligation.
type OpsPaymentOrderFact struct {
	OrderID, AmountMinor, RefundedMinor, PayerCustomerID, BeneficiaryCustomerID      int64
	Provider, Currency, Status, RecordOrigin                                         string
	CheckoutPresent                                                                  bool
	CheckoutProductType, CheckoutCurrency                                            string
	CheckoutPayableMinor                                                             int64
	PaidEventPresent, ServiceGrantPresent, ServiceGrantMatches, ServiceRefundPresent bool
}

// OpsPaymentOrderReader joins the caller's readonly repeatable-read snapshot.
// It may read Order-owned tables only, never Payment or Outbound tables.
type OpsPaymentOrderReader interface {
	ReadOpsPaymentOrderFactsWithin(context.Context, []int64) ([]OpsPaymentOrderFact, error)
}
