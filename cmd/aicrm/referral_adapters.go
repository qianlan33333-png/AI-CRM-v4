package main

import (
	"context"
	"errors"

	customerapp "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/app"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

// Read the Customer-owned trusted OAuth projection in its required UoW.
// The caller already holds the root verified by invitation issuance.
type referralWeChatNicknameAdapter struct {
	uow    platformport.UnitOfWork
	reader customerport.WeChatNicknameReader
}

func (a referralWeChatNicknameAdapter) ReadWeChatNickname(ctx context.Context, id customerdomain.CustomerID) (string, error) {
	var nickname string
	err := a.uow.Within(ctx, func(tx context.Context) error {
		var err error
		nickname, err = a.reader.ReadWeChatNickname(tx, id)
		return err
	})
	return nickname, err
}

// referralCanonicalCustomerVerifier is deliberately narrower than a customer
// picker. It verifies that an administrator-selected ID is an existing,
// canonical Customer root. It neither accepts a browser claim nor follows a
// merged alias, which keeps team captains anchored to a stable OneID customer.
type referralCanonicalCustomerVerifier struct {
	resolver   customerport.CanonicalCustomerResolver
	identities identityport.TrustedCanonicalCustomerReader
}

func (adapter referralCanonicalCustomerVerifier) VerifyCanonicalCustomer(ctx context.Context, raw int64) (bool, error) {
	if adapter.resolver == nil || adapter.identities == nil || raw < 1 {
		return false, nil
	}
	customerID := customerdomain.CustomerID(raw)
	resolved, err := adapter.resolver.ResolveCanonicalCustomer(ctx, customerID)
	if errors.Is(err, customerapp.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if resolved.Merged || resolved.RequestedCustomerID != customerID || resolved.CustomerID != customerID {
		return false, nil
	}
	return adapter.identities.HasActiveVerifiedIdentity(ctx, customerID)
}
