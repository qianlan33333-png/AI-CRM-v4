package app

import (
	"context"
	"strings"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
)

type HistoricalPhoneDisplayProjection interface {
	ProjectHistoricalPhoneWithoutActivity(context.Context, customerdomain.CustomerID, string, string, time.Time) error
}

type EmptyCollectedPhoneProjection interface {
	PhoneDisplayEmptyWithin(context.Context, customerdomain.CustomerID) (bool, error)
}

type CollectedPhoneProjection interface {
	ProjectCollectedPhone(context.Context, customerdomain.CustomerID, string, string, time.Time, bool) error
}

type CollectedPhoneService struct {
	phones     identityport.DeclaredPhoneAttacher
	projection CollectedPhoneProjection
}

func NewCollectedPhoneService(phones identityport.DeclaredPhoneAttacher, projection CollectedPhoneProjection) (*CollectedPhoneService, error) {
	if phones == nil || projection == nil {
		return nil, ErrInvalidQuery
	}
	return &CollectedPhoneService{phones: phones, projection: projection}, nil
}

func (s *CollectedPhoneService) CollectPhoneWithin(ctx context.Context, c customerport.CollectedPhoneCommand) (identityport.DeclaredAttachResult, error) {
	if s == nil || s.phones == nil || s.projection == nil || c.CollectedAt.IsZero() {
		return identityport.DeclaredAttachResult{}, ErrInvalidQuery
	}
	if c.OnlyIfEmpty {
		locker, ok := s.phones.(identityport.CustomerIdentityLocker)
		if !ok {
			return identityport.DeclaredAttachResult{}, ErrInvalidQuery
		}
		if err := locker.LockActiveCustomerWithin(ctx, c.CustomerID); err != nil {
			return identityport.DeclaredAttachResult{}, err
		}
		projection, ok := s.projection.(EmptyCollectedPhoneProjection)
		if !ok {
			return identityport.DeclaredAttachResult{}, ErrInvalidQuery
		}
		empty, err := projection.PhoneDisplayEmptyWithin(ctx, c.CustomerID)
		if err != nil {
			return identityport.DeclaredAttachResult{}, err
		}
		if !empty {
			return identityport.DeclaredAttachResult{Status: identityport.DeclaredInvalid}, nil
		}
	}
	// Checkout freezes E.164; questionnaires collect CN11. Normalize once using
	// Identity's existing contract before calling its ownership boundary.
	c.Phone = strings.TrimPrefix(strings.TrimSpace(c.Phone), "+86")
	normalized, err := identitydomain.Normalize(identitydomain.Reference{Kind: identitydomain.KindPhone, Scope: "phone:cn11", Value: c.Phone, Assurance: identitydomain.AssuranceDeclared, Source: c.Source})
	if err != nil {
		return identityport.DeclaredAttachResult{Status: identityport.DeclaredInvalid}, nil
	}
	c.Phone = normalized.NormalizedValue
	result, err := s.phones.AttachDeclaredPhoneToCustomer(ctx, c.DeclaredPhoneCommand)
	if err != nil {
		return result, err
	}
	// A replay retains its original outcome. Never treat conflict replay as
	// success, and never overwrite a newer presentation with an old receipt.
	if result.Status == identityport.DeclaredAttached || result.Status == identityport.DeclaredAlreadyLinked {
		if result.CustomerID != c.CustomerID || result.IdentityID < 1 {
			return result, ErrInvalidQuery
		}
		phone := normalized.NormalizedValue
		masked := phone[:3] + "****" + phone[7:]
		if c.OnlyIfEmpty && c.Historical {
			projection, ok := s.projection.(HistoricalPhoneDisplayProjection)
			if !ok {
				return result, ErrInvalidQuery
			}
			err = projection.ProjectHistoricalPhoneWithoutActivity(ctx, c.CustomerID, masked, c.Source, c.CollectedAt)
		} else {
			err = s.projection.ProjectCollectedPhone(ctx, c.CustomerID, masked, c.Source, c.CollectedAt, c.Historical)
		}
	}
	return result, err
}
