package app

import (
	"context"

	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
)

// ReadCheckoutContact reads both immutable snapshots in one Order transaction.
// The Within readers remain available to consumers already inside a UoW.
func (s *Service) ReadCheckoutContact(ctx context.Context, orderID int64) (orderport.CheckoutContact, error) {
	if !ready(s) || orderID < 1 {
		return orderport.CheckoutContact{}, orderport.ErrConflict
	}
	var contact orderport.CheckoutContact
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		contact.ShippingAddress, contact.AddressCollected, readErr = s.ReadCheckoutShippingAddressWithin(tx, orderID)
		if readErr != nil {
			return readErr
		}
		contact.MobileE164, contact.MobileCollected, readErr = s.ReadCheckoutMobileWithin(tx, orderID)
		return readErr
	})
	if err != nil {
		return orderport.CheckoutContact{}, orderport.ErrUnavailable
	}
	return contact, nil
}
