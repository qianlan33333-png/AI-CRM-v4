package port

import "context"

// InvoicePaymentReader is a same-UoW read of an exact, confirmed native WeChat
// transaction. It reveals only internal ownership handles, never an OpenID.
type InvoicePaymentReader interface {
	InvoicePaymentWithin(context.Context, string) (InvoicePayment, error)
}

type InvoicePayment struct {
	OrderID, CustomerID, IdentityID, Amount int64
	OrderNo, TransactionID, Currency        string
}
