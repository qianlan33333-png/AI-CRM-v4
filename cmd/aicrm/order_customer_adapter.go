package main

import (
	"context"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

type orderCustomerDisplayNameAdapter struct {
	uow    platformport.UnitOfWork
	reader customerport.DirectoryDisplayNameReader
}

func (adapter orderCustomerDisplayNameAdapter) DisplayNames(ctx context.Context, ids []customerdomain.CustomerID) (map[customerdomain.CustomerID]string, error) {
	var result map[customerdomain.CustomerID]string
	err := adapter.uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		result, readErr = adapter.reader.DisplayNames(tx, ids)
		return readErr
	})
	return result, err
}

// referralCustomerProfileAdapter is a presentation-only Customer Port bridge.
// Referral calls it only after its own application has selected canonical
// customer IDs, so it cannot be used as a directory search or identity matcher.
type referralCustomerProfileAdapter struct {
	uow    platformport.UnitOfWork
	reader customerport.DirectoryPublicProfileReader
}

func (adapter referralCustomerProfileAdapter) PublicProfiles(ctx context.Context, ids []customerdomain.CustomerID) (map[customerdomain.CustomerID]customerport.DirectoryPublicProfile, error) {
	var result map[customerdomain.CustomerID]customerport.DirectoryPublicProfile
	err := adapter.uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		result, readErr = adapter.reader.PublicProfiles(tx, ids)
		return readErr
	})
	return result, err
}

// orderCustomerContactDisplayAdapter is the Order-specific read bridge for
// Customer's presentation-safe directory projection. It does not resolve an
// identity or read Customer tables from Order; the owning Customer Port does
// both inside the composition-provided Unit of Work.
type orderCustomerContactDisplayAdapter struct {
	numbers identityport.CustomerPublicNumbers
	uow     platformport.UnitOfWork
	reader  customerport.DirectoryContactDisplayReader
}

func (adapter orderCustomerContactDisplayAdapter) ContactDisplays(ctx context.Context, ids []customerdomain.CustomerID) (map[customerdomain.CustomerID]customerport.DirectoryContactDisplay, error) {
	var result map[customerdomain.CustomerID]customerport.DirectoryContactDisplay
	err := adapter.uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		result, readErr = adapter.reader.ContactDisplays(tx, ids)
		if readErr != nil {
			return readErr
		}
		if adapter.numbers != nil {
			numbers, err := adapter.numbers.CustomerPublicNumbers(tx, ids)
			if err != nil {
				return err
			}
			for id, number := range numbers {
				v := result[id]
				v.CustomerNumber = number
				result[id] = v
			}
		}
		return nil
	})
	return result, err
}
