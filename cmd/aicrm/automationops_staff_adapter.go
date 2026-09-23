package main

import (
	"context"
	"errors"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accessport "github.com/qianlan33333-png/AI-CRM-v3/internal/access/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

type automationOpsAccessRepository interface {
	UserByWeComUserID(context.Context, string, bool) (accessdomain.User, error)
	UserByID(context.Context, int64, bool) (accessdomain.User, error)
}
type automationOpsStaffAdapter struct {
	uow   platformport.UnitOfWork
	users automationOpsAccessRepository
}

func (a automationOpsStaffAdapter) ResolveAutomationSender(ctx context.Context, providerMember string) (accessport.StaffEligibility, bool, error) {
	if a.uow == nil || a.users == nil {
		return accessport.StaffEligibility{}, false, errors.New("staff projection unavailable")
	}
	var user accessdomain.User
	err := a.uow.Within(ctx, func(tx context.Context) error {
		var e error
		user, e = a.users.UserByWeComUserID(tx, providerMember, false)
		return e
	})
	if errors.Is(err, accessdomain.ErrNotFound) {
		return accessport.StaffEligibility{}, false, nil
	}
	if err != nil {
		return accessport.StaffEligibility{}, false, err
	}
	return staffEligibility(user), true, nil
}
func (a automationOpsStaffAdapter) AutomationSender(ctx context.Context, id accessport.StaffID) (accessport.StaffEligibility, bool, error) {
	if a.uow == nil || a.users == nil || id < 1 {
		return accessport.StaffEligibility{}, false, errors.New("staff projection unavailable")
	}
	var user accessdomain.User
	err := a.uow.Within(ctx, func(tx context.Context) error {
		var e error
		user, e = a.users.UserByID(tx, int64(id), false)
		return e
	})
	if errors.Is(err, accessdomain.ErrNotFound) {
		return accessport.StaffEligibility{}, false, nil
	}
	if err != nil {
		return accessport.StaffEligibility{}, false, err
	}
	return staffEligibility(user), true, nil
}
func staffEligibility(user accessdomain.User) accessport.StaffEligibility {
	return accessport.StaffEligibility{StaffID: accessport.StaffID(user.ID), DisplayName: user.DisplayName, Active: user.Active, Eligible: user.Active && user.WeComUserID != "", EligibilityVersion: user.SessionVersion, RefreshedAt: user.UpdatedAt}
}
func (a automationOpsStaffAdapter) OutboundProviderStaffID(ctx context.Context, id accessport.StaffID) (string, bool, error) {
	if a.uow == nil || a.users == nil || id < 1 {
		return "", false, errors.New("staff projection unavailable")
	}
	var user accessdomain.User
	err := a.uow.Within(ctx, func(tx context.Context) error {
		var e error
		user, e = a.users.UserByID(tx, int64(id), false)
		return e
	})
	if errors.Is(err, accessdomain.ErrNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if !user.Active || user.WeComUserID == "" {
		return "", false, nil
	}
	return user.WeComUserID, true, nil
}

func (a automationOpsStaffAdapter) ResolveAudienceOwner(ctx context.Context, providerUserID string) (accessport.StaffID, bool, error) {
	item, found, err := a.ResolveAutomationSender(ctx, providerUserID)
	return item.StaffID, found, err
}
func (a automationOpsStaffAdapter) AudienceOwnerUserID(ctx context.Context, id accessport.StaffID) (string, bool, error) {
	if a.uow == nil || a.users == nil || id < 1 {
		return "", false, errors.New("staff projection unavailable")
	}
	var user accessdomain.User
	read := func(tx context.Context) error {
		var e error
		user, e = a.users.UserByID(tx, int64(id), false)
		return e
	}
	var err error
	// Segment evaluates all Owner facts inside its existing UoW. Reuse that
	// transaction; standalone HTTP reference readback still opens its own UoW.
	if _, txErr := platformpostgres.RequireTransaction(ctx); txErr == nil {
		err = read(ctx)
	} else {
		err = a.uow.Within(ctx, read)
	}
	if errors.Is(err, accessdomain.ErrNotFound) {
		return "", false, nil
	}
	if err != nil || user.WeComUserID == "" {
		return "", false, err
	}
	return user.WeComUserID, true, nil
}

var _ accessport.AutomationOpsStaffReader = automationOpsStaffAdapter{}
var _ accessport.OutboundStaffIdentityReader = automationOpsStaffAdapter{}
var _ accessport.AudienceOwnerResolver = automationOpsStaffAdapter{}
var _ accessport.AudienceOwnerReferenceReader = automationOpsStaffAdapter{}
