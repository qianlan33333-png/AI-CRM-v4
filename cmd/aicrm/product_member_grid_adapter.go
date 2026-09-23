package main

import (
	"context"
	"errors"
	"sort"
	"strings"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accessport "github.com/qianlan33333-png/AI-CRM-v3/internal/access/port"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

// productMemberGridStaffDirectory is the composition-only bridge to Access.
// It verifies a collaborator target is an active staff record before Product
// persists its local metadata; Product never queries access tables itself.
type productMemberGridStaffDirectory struct{ users accessport.Repository }

func (adapter productMemberGridStaffDirectory) ActiveMemberGridStaff(ctx context.Context, id int64) (bool, error) {
	if adapter.users == nil || id < 1 {
		return false, nil
	}
	user, err := adapter.users.UserByID(ctx, id, false)
	if errors.Is(err, accessdomain.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return user.Active, nil
}

func (adapter productMemberGridStaffDirectory) MemberGridStaffByWeComUserID(ctx context.Context, value string) (productport.MemberGridStaff, bool, error) {
	if adapter.users == nil || strings.TrimSpace(value) == "" {
		return productport.MemberGridStaff{}, false, nil
	}
	user, err := adapter.users.UserByWeComUserID(ctx, strings.TrimSpace(value), false)
	if errors.Is(err, accessdomain.ErrNotFound) {
		return productport.MemberGridStaff{}, false, nil
	}
	if err != nil {
		return productport.MemberGridStaff{}, false, err
	}
	return productport.MemberGridStaff{AdminUserID: user.ID, WeComUserID: user.WeComUserID, DisplayName: user.DisplayName, Active: user.Active}, true, nil
}

func (adapter productMemberGridStaffDirectory) MemberGridStaffByID(ctx context.Context, id int64) (productport.MemberGridStaff, bool, error) {
	if adapter.users == nil || id < 1 {
		return productport.MemberGridStaff{}, false, nil
	}
	user, err := adapter.users.UserByID(ctx, id, false)
	if errors.Is(err, accessdomain.ErrNotFound) {
		return productport.MemberGridStaff{}, false, nil
	}
	if err != nil {
		return productport.MemberGridStaff{}, false, err
	}
	return productport.MemberGridStaff{AdminUserID: user.ID, WeComUserID: user.WeComUserID, DisplayName: user.DisplayName, Active: user.Active}, true, nil
}

func (adapter productMemberGridStaffDirectory) ListActiveMemberGridStaff(ctx context.Context) ([]productport.MemberGridStaff, error) {
	if adapter.users == nil {
		return nil, errors.New("member-grid staff directory unavailable")
	}
	users, err := adapter.users.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]productport.MemberGridStaff, 0, len(users))
	for _, user := range users {
		if user.Active && strings.TrimSpace(user.WeComUserID) != "" {
			out = append(out, productport.MemberGridStaff{AdminUserID: user.ID, WeComUserID: user.WeComUserID, DisplayName: user.DisplayName, Active: true})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AdminUserID < out[j].AdminUserID })
	return out, nil
}
