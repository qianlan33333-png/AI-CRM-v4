package app

import (
	"context"
	"errors"

	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
)

var ErrReauthorizationRequired = errors.New("coupon trusted identity requires reauthorization")

type trustedCouponSubjects interface {
	identityport.VerifiedIdentitySubjectReader
	identityport.WriteLockedVerifiedIdentitySubjectReader
}

func (s *CheckoutService) BindIdentitySubjects(reader trustedCouponSubjects) error {
	if s == nil || reader == nil || s.subjects != nil {
		return ErrUnavailable
	}
	s.subjects = reader
	return nil
}

func (s *PublicCouponService) BindIdentitySubjects(reader trustedCouponSubjects) error {
	if s == nil || reader == nil || s.subjects != nil {
		return ErrUnavailable
	}
	s.subjects = reader
	return nil
}

// Identity owns handle validity, canonical roots and reversal. Keep its lock
// in the same UoW as the following Coupon read/write; a frozen session customer
// is not evidence of the handle's current owner.
func currentCouponSubjectWithin(ctx context.Context, reader trustedCouponSubjects, identityID int64, write bool) (int64, error) {
	if reader == nil {
		return 0, ErrUnavailable
	}
	var subject identityport.VerifiedIdentitySubject
	var found bool
	var err error
	if write {
		subject, found, err = reader.WriteLockedVerifiedIdentitySubject(ctx, identityID)
	} else {
		subject, found, err = reader.VerifiedIdentitySubject(ctx, identityID)
	}
	if err != nil {
		return 0, ErrUnavailable
	}
	if !found {
		return 0, ErrReauthorizationRequired
	}
	if subject.CustomerID < 1 || len(subject.Lineage) == 0 || subject.Lineage[0] != subject.CustomerID {
		return 0, ErrUnavailable
	}
	return int64(subject.CustomerID), nil
}
