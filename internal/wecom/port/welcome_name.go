package port

import "context"

// CallbackCustomerNameReader reads presentation data only for the authenticated
// callback bound to this welcome grant. It never provisions a customer.
type CallbackCustomerNameReader interface {
	CallbackCustomerName(context.Context, string, string) (string, error)
}
