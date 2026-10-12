package app

import (
	"context"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

func (s *Service) SetContactProfiles(reader customerport.ContactReader, subjects identityport.VerifiedIdentitySubjectReader) {
	s.contacts = reader
	s.contactSubjects = subjects
}
func (s *Service) CheckoutContactPrefill(ctx context.Context, token, kind string, id int64) (customerport.ContactProfile, error) {
	var result customerport.ContactProfile
	if s == nil || s.contacts == nil || s.contactSubjects == nil || s.products == nil || id < 1 || (kind != "standard" && kind != "service_period") {
		return result, paymentport.ErrInvalid
	}
	err := s.uow.Within(ctx, func(tx context.Context) error {
		actor, e := s.sessions.LookupWithin(tx, token, s.now().UTC())
		if e != nil {
			return e
		}
		subject, found, e := s.contactSubjects.VerifiedIdentitySubject(tx, actor.PayerIdentityID)
		if e != nil || !found || subject.CustomerID < 1 {
			return paymentport.ErrSessionRequired
		}
		product, e := s.products.ReadCheckoutProductWithin(tx, productport.ProductOptionType(kind), productport.ID(id))
		if e != nil {
			return e
		}
		level := product.ContactCollectionLevel
		if level == "" && product.RequireMobile {
			level = "mobile"
		}
		if level == "" || level == "none" {
			return nil
		}
		result, e = s.contacts.ReadContactWithin(tx, subject.CustomerID, level == "shipping_address")
		return e
	})
	return result, err
}
