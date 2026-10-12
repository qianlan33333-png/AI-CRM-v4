package app

import (
	"context"
	"errors"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
	"testing"
)

type contactSubjectStub struct{ found bool }

func (s contactSubjectStub) VerifiedIdentitySubject(_ context.Context, id int64) (identityport.VerifiedIdentitySubject, bool, error) {
	if id != 4 {
		return identityport.VerifiedIdentitySubject{}, false, errors.New("wrong handle")
	}
	return identityport.VerifiedIdentitySubject{CustomerID: 22, Lineage: []customerdomain.CustomerID{22}}, s.found, nil
}

type contactReadStub struct {
	calls    int
	shipping bool
	customer customerdomain.CustomerID
}

func (s *contactReadStub) ReadContactWithin(_ context.Context, id customerdomain.CustomerID, shipping bool) (customerport.ContactProfile, error) {
	s.calls++
	s.shipping = shipping
	s.customer = id
	return customerport.ContactProfile{MobileE164: "+8613800000000"}, nil
}
func TestContactPrefillUsesLiveIdentityAndConfiguredCollection(t *testing.T) {
	for _, level := range []string{"none", "mobile", "shipping_address"} {
		s := NewService(uowStub{}, &storeStub{}, &checkoutOrderStub{}, sessionStub{actor: paymentport.SessionActor{PayerCustomerID: 11, PayerIdentityID: 4}}, &effectStub{})
		products := &checkoutProductStub{product: productport.CheckoutProduct{ID: 5, ContactCollectionLevel: level}}
		if e := s.SetCheckoutProductReader(products); e != nil {
			t.Fatal(e)
		}
		contacts := &contactReadStub{}
		s.SetContactProfiles(contacts, contactSubjectStub{true})
		v, e := s.CheckoutContactPrefill(context.Background(), "trusted-token", "standard", 5)
		if e != nil {
			t.Fatal(e)
		}
		if level == "none" {
			if contacts.calls != 0 || v.MobileE164 != "" {
				t.Fatal("none mode read contact")
			}
		} else if contacts.calls != 1 || contacts.customer != 22 || contacts.shipping != (level == "shipping_address") {
			t.Fatal("wrong prefill scope")
		}
		before := contacts.calls
		s.SetContactProfiles(contacts, contactSubjectStub{false})
		if _, e = s.CheckoutContactPrefill(context.Background(), "trusted-token", "standard", 5); !errors.Is(e, paymentport.ErrSessionRequired) || contacts.calls != before {
			t.Fatal("invalid identity disclosed contact")
		}
	}
}
