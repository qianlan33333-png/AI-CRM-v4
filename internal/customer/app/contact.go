package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
)

type ContactCipher interface {
	Encrypt(string) ([]byte, error)
	Decrypt([]byte) (string, error)
}
type ContactStore interface {
	WriteContactWithin(context.Context, customerport.ContactUpdate, []byte, []byte) error
	ReadContactCiphertextsWithin(context.Context, []customerdomain.CustomerID, bool) ([]byte, []byte, error)
}
type ContactService struct {
	store   ContactStore
	cipher  ContactCipher
	lineage identityport.CanonicalLineageReader
}

func NewContactService(store ContactStore, cipher ContactCipher, lineage identityport.CanonicalLineageReader) (*ContactService, error) {
	if store == nil || cipher == nil || lineage == nil {
		return nil, errors.New("contact dependencies required")
	}
	return &ContactService{store: store, cipher: cipher, lineage: lineage}, nil
}
func (s *ContactService) UpdateContactWithin(ctx context.Context, c customerport.ContactUpdate) error {
	if c.CustomerID < 1 || strings.TrimSpace(c.Source) == "" || (c.OnlyIfEmpty && c.SourceAt.IsZero()) {
		return ErrInvalidQuery
	}
	if c.MobileE164 == "" && c.Shipping == nil {
		return nil
	}
	var phone, address []byte
	var err error
	if c.MobileE164 != "" {
		var valid bool
		c.MobileE164, valid = customerport.NormalizeContactPhone(c.MobileE164)
		if !valid {
			return ErrInvalidQuery
		}
		phone, err = s.cipher.Encrypt(c.MobileE164)
		if err != nil {
			return err
		}
	}
	if c.Shipping != nil {
		v := *c.Shipping
		if !customerport.ValidShippingContact(v) {
			return ErrInvalidQuery
		}
		p, _ := customerport.NormalizeContactPhone(v.MobileE164)
		if c.MobileE164 != "" && c.MobileE164 != p {
			return ErrInvalidQuery
		}
		// The current phone has one owner; it is not duplicated in the address group.
		v.MobileE164 = ""
		raw, e := json.Marshal(v)
		if e != nil {
			return e
		}
		address, err = s.cipher.Encrypt(string(raw))
		if err != nil {
			return err
		}
	}
	return s.store.WriteContactWithin(ctx, c, phone, address)
}
func (s *ContactService) ReadContactWithin(ctx context.Context, id customerdomain.CustomerID, shipping bool) (customerport.ContactProfile, error) {
	var result customerport.ContactProfile
	if id < 1 {
		return result, ErrInvalidQuery
	}
	var ids []customerdomain.CustomerID
	var err error
	if locked, ok := s.lineage.(interface {
		LockedCanonicalLineage(context.Context, customerdomain.CustomerID) ([]customerdomain.CustomerID, error)
	}); ok {
		ids, err = locked.LockedCanonicalLineage(ctx, id)
	} else {
		ids, err = s.lineage.CanonicalLineage(ctx, id)
	}
	if err != nil {
		return result, err
	}
	if len(ids) == 0 {
		return result, ErrNotFound
	}
	phone, address, err := s.store.ReadContactCiphertextsWithin(ctx, ids, shipping)
	if err != nil {
		return result, err
	}
	if len(phone) > 0 {
		result.MobileE164, err = s.cipher.Decrypt(phone)
		if err != nil {
			return customerport.ContactProfile{}, err
		}
	}
	if result.MobileE164 != "" {
		var valid bool
		result.MobileE164, valid = customerport.NormalizeContactPhone(result.MobileE164)
		if !valid {
			return customerport.ContactProfile{}, ErrInvalidQuery
		}
	}
	if len(address) > 0 {
		raw, e := s.cipher.Decrypt(address)
		if e != nil {
			return customerport.ContactProfile{}, e
		}
		var v customerport.ShippingContact
		if json.Unmarshal([]byte(raw), &v) != nil {
			return customerport.ContactProfile{}, ErrInvalidQuery
		}
		v.MobileE164 = result.MobileE164
		result.Shipping = &v
	}
	return result, nil
}
