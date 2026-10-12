package app

import (
	"context"
	"fmt"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
)

func (s *Service) SetContactProfiles(w customerport.ContactWriter) { s.contacts = w }

// ContactHistoryWithin reads frozen order facts without replaying identities,
// payments, or fulfilment. The original payer is retained after merges.
func (s *Service) ContactHistoryWithin(ctx context.Context, after int64) ([]customerport.ContactHistory, error) {
	ids, err := s.CollectedPhoneOrderIDsWithin(ctx, after, 0)
	if err != nil {
		return nil, err
	}
	out := make([]customerport.ContactHistory, 0, len(ids))
	for _, id := range ids {
		fact := customerport.ContactHistory{Cursor: id}
		order, e := s.store.Get(ctx, id, false)
		if e != nil {
			return nil, e
		}
		snap := order.Snapshot()
		if snap.PayerCustomerID == nil {
			fact.SkipReason = "missing_customer"
			out = append(out, fact)
			continue
		}
		fact.Update = customerport.ContactUpdate{CustomerID: customerdomain.CustomerID(*snap.PayerCustomerID), Source: fmt.Sprintf("order:%d:contact", id), SourceAt: snap.CreatedAt, OnlyIfEmpty: true}
		phone, found, e := s.ReadCheckoutMobileWithin(ctx, id)
		if e != nil {
			fact.SkipReason = "unreadable"
		} else if !found {
			fact.SkipReason = "missing_phone"
		} else if p, ok := customerport.NormalizeContactPhone(phone); ok {
			fact.Update.MobileE164 = p
		} else {
			fact.SkipReason = "invalid_phone"
		}
		address, has, e := s.ReadCheckoutShippingAddressWithin(ctx, id)
		if e != nil {
			fact.SkipReason = "unreadable"
		} else if has {
			v := customerport.ShippingContact{MobileE164: phone, RecipientName: address.RecipientName, ProvinceCode: address.ProvinceCode, ProvinceName: address.ProvinceName, CityCode: address.CityCode, CityName: address.CityName, DistrictCode: address.DistrictCode, DistrictName: address.DistrictName, DetailAddress: address.DetailAddress}
			if customerport.ValidShippingContact(v) {
				fact.Update.Shipping = &v
			} else {
				fact.SkipReason = "invalid_shipping"
			}
		}
		out = append(out, fact)
	}
	return out, nil
}
