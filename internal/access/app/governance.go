package app

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/access/credential"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accessport "github.com/qianlan33333-png/AI-CRM-v3/internal/access/port"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

const (
	enterpriseDirectoryPageSize = 50
	enterpriseSearchMaxMembers  = 10000
	enterpriseSearchMaxPages    = 200
	enterpriseSearchDeadline    = 12 * time.Second
)

var ErrEnterpriseDirectoryUnavailable = errors.New("enterprise employee directory unavailable")

type EnterpriseEmployees interface {
	EnterpriseDirectoryReady() bool
	ListEnterpriseEmployees(context.Context) ([]wecomport.EnterpriseEmployee, error)
	ReadEnterpriseEmployee(context.Context, string) (wecomport.EnterpriseEmployee, error)
}

type GovernanceActions struct {
	SetLoginEnabled    bool `json:"set_login_enabled"`
	ChangeRole         bool `json:"change_role"`
	BindWeComUserID    bool `json:"bind_wecom_userid"`
	ResetPassword      bool `json:"reset_password"`
	TransferSuperAdmin bool `json:"transfer_super_admin"`
}

type GovernanceCapabilities struct {
	ProvisionAdmin     bool `json:"provision_admin"`
	ProvisionViewer    bool `json:"provision_viewer"`
	TransferSuperAdmin bool `json:"transfer_super_admin"`
}

type GovernanceActor struct {
	AdminUserID int64       `json:"admin_user_id"`
	Role        domain.Role `json:"role"`
}

type GovernanceUser struct {
	UserSummary
	// AdminUserID is explicit for the governance command routes. ID remains
	// present through UserSummary for the frozen-list compatibility readers.
	AdminUserID int64 `json:"admin_user_id"`
	// These fields are explicit in the canonical governance DTO. The embedded
	// UserSummary remains for frozen list readers, whose `active` spelling is
	// part of their existing response contract.
	IsActive        bool              `json:"is_active"`
	LastLoginAt     *time.Time        `json:"last_login_at"`
	AccessGrantedAt *time.Time        `json:"access_granted_at"`
	Role            domain.Role       `json:"role"`
	LoginEnabled    bool              `json:"login_enabled"`
	Actions         GovernanceActions `json:"actions"`
}

type GovernanceListing struct {
	Actor        GovernanceActor        `json:"actor"`
	Capabilities GovernanceCapabilities `json:"capabilities"`
	Users        []GovernanceUser       `json:"users"`
}

type EnterpriseEmployeeItem struct {
	WeComUserID       string       `json:"wecom_userid"`
	DisplayName       string       `json:"display_name"`
	AuthorizedAccount bool         `json:"authorized_account"`
	Role              *domain.Role `json:"role,omitempty"`
	LoginEnabled      *bool        `json:"login_enabled,omitempty"`
}

type EnterpriseEmployeeListing struct {
	Items      []EnterpriseEmployeeItem `json:"items"`
	NextCursor string                   `json:"next_cursor"`
	HasMore    bool                     `json:"has_more"`
}

type ProvisionEnterpriseEmployeeInput struct {
	WeComUserID    string
	Role           domain.Role
	IdempotencyKey string
}

type SetLoginEnabledInput struct {
	TargetID       int64
	LoginEnabled   bool
	IdempotencyKey string
}

type SetRoleInput struct {
	TargetID       int64
	Role           domain.Role
	IdempotencyKey string
}

type BindEnterpriseEmployeeInput struct {
	TargetID       int64
	WeComUserID    string
	IdempotencyKey string
}

type ResetPasswordInput struct {
	TargetID       int64
	Password       string
	IdempotencyKey string
}

type TransferSuperAdminInput struct {
	TargetID       int64
	IdempotencyKey string
}

// SetGovernanceSigningKey configures the domain-separated secret used for
// idempotency request fingerprints. It prevents a stored receipt digest from
// becoming an offline password verifier.
func (service *Management) SetGovernanceSigningKey(key []byte) error {
	if service == nil || len(key) < 32 {
		return ErrEnterpriseDirectoryUnavailable
	}
	derived := sha256.Sum256(append([]byte("access-governance-idempotency-v1\x00"), key...))
	service.governanceKey = derived[:]
	return nil
}

// SetEnterpriseEmployeeDirectory is wired by composition after the read-only
// WeCom client is ready. Calling a governed employee operation without it
// fails closed instead of trusting a browser-supplied employee identifier.
func (service *Management) SetEnterpriseEmployeeDirectory(directory EnterpriseEmployees, cursorKey []byte, corpScope string) error {
	if service == nil || directory == nil || len(cursorKey) < 32 || strings.TrimSpace(corpScope) == "" {
		return ErrEnterpriseDirectoryUnavailable
	}
	// Domain-separated signing material prevents a cursor from being accepted
	// by another protocol that happens to use the same runtime key.
	derived := sha256.Sum256(append(append([]byte("access-enterprise-directory-v1\x00"), cursorKey...), []byte(corpScope)...))
	service.enterpriseDirectory, service.enterpriseCursorKey, service.enterpriseCorpScope = directory, derived[:], corpScope
	return nil
}

func (service *Management) ListGovernance(ctx context.Context, actor domain.Principal) (GovernanceListing, error) {
	var result GovernanceListing
	err := service.uow.Within(ctx, func(txContext context.Context) error {
		current, role, err := service.currentGovernanceActor(txContext, actor)
		if err != nil {
			return err
		}
		users, err := service.repository.ListUsers(txContext)
		if err != nil {
			return err
		}
		result.Actor = GovernanceActor{AdminUserID: current.ID, Role: role}
		result.Capabilities = capabilitiesFor(role)
		result.Users = make([]GovernanceUser, 0, len(users))
		for _, user := range users {
			if user.AccessGrantedAt == nil {
				continue
			}
			userRole, err := domain.SingleRole(user.Roles)
			if err != nil {
				return domain.ErrConflict
			}
			result.Users = append(result.Users, GovernanceUser{UserSummary: summarizeUser(user), AdminUserID: user.ID, IsActive: user.Active, LastLoginAt: user.LastLoginAt, AccessGrantedAt: user.AccessGrantedAt, Role: userRole, LoginEnabled: user.LoginEnabled, Actions: actionsFor(role, current.ID, user, userRole)})
		}
		return nil
	})
	return result, err
}

func (service *Management) ListEnterpriseEmployees(ctx context.Context, actor domain.Principal, cursor, query string, limit int) (EnterpriseEmployeeListing, error) {
	if limit < 1 || limit > enterpriseDirectoryPageSize || strings.TrimSpace(cursor) != cursor {
		return EnterpriseEmployeeListing{}, domain.ErrInvalidInput
	}
	if err := service.authorizeGovernanceRead(ctx, actor); err != nil {
		return EnterpriseEmployeeListing{}, err
	}
	if service.enterpriseDirectory == nil || !service.enterpriseDirectory.EnterpriseDirectoryReady() {
		return EnterpriseEmployeeListing{}, ErrEnterpriseDirectoryUnavailable
	}
	query = strings.TrimSpace(query)
	if len([]rune(query)) > 120 || strings.ContainsAny(query, "\x00\r\n") {
		return EnterpriseEmployeeListing{}, domain.ErrInvalidInput
	}
	if candidate, err := domain.NormalizeWeComUserID(query); query != "" && err == nil && candidate == query {
		employee, readErr := service.enterpriseDirectory.ReadEnterpriseEmployee(ctx, candidate)
		switch {
		case readErr == nil:
			if cursor != "" || employee.UserID != candidate || strings.TrimSpace(employee.DisplayName) == "" {
				return EnterpriseEmployeeListing{}, domain.ErrInvalidInput
			}
			items, itemErr := service.enterpriseItems(ctx, []wecomport.EnterpriseEmployee{employee})
			if itemErr != nil {
				return EnterpriseEmployeeListing{}, itemErr
			}
			return EnterpriseEmployeeListing{Items: items}, nil
		case !errors.Is(readErr, wecomport.ErrEnterpriseEmployeeNotFound):
			return EnterpriseEmployeeListing{}, ErrEnterpriseDirectoryUnavailable
		}
		// A syntactically valid value can also be a display name (for example,
		// "Alice"). Only a definitive exact-ID miss may fall back to the
		// complete server-side name search. Provider failures never become an
		// empty name result.
	}
	mode := enterpriseCursorBrowse
	if query != "" {
		mode = enterpriseCursorSearch
	}
	state, err := service.decodeEnterpriseCursor(cursor, actor, query, mode)
	if err != nil {
		return EnterpriseEmployeeListing{}, domain.ErrInvalidInput
	}
	employees, err := service.completeEnterpriseDirectory(ctx)
	if err != nil {
		return EnterpriseEmployeeListing{}, err
	}
	if query != "" {
		wanted := strings.ToLower(query)
		matches := make([]wecomport.EnterpriseEmployee, 0, len(employees))
		for _, employee := range employees {
			if strings.Contains(strings.ToLower(employee.DisplayName), wanted) {
				matches = append(matches, employee)
			}
		}
		employees = matches
	}
	if state.Offset > len(employees) {
		return EnterpriseEmployeeListing{}, domain.ErrInvalidInput
	}
	end := state.Offset + limit
	if end > len(employees) {
		end = len(employees)
	}
	items, err := service.enterpriseItems(ctx, employees[state.Offset:end])
	if err != nil {
		return EnterpriseEmployeeListing{}, err
	}
	next := ""
	if end < len(employees) {
		next = service.encodeEnterpriseCursor(enterpriseDirectoryCursor{Mode: mode, Offset: end}, actor, query)
	}
	return EnterpriseEmployeeListing{Items: items, NextCursor: next, HasMore: next != ""}, nil
}

func (service *Management) completeEnterpriseDirectory(ctx context.Context) ([]wecomport.EnterpriseEmployee, error) {
	readCtx, cancel := context.WithTimeout(ctx, enterpriseSearchDeadline)
	defer cancel()
	employees, err := service.enterpriseDirectory.ListEnterpriseEmployees(readCtx)
	if err != nil || len(employees) > enterpriseSearchMaxMembers || readCtx.Err() != nil {
		return nil, ErrEnterpriseDirectoryUnavailable
	}
	seen := make(map[string]struct{}, len(employees))
	for _, employee := range employees {
		if _, normalizeErr := domain.NormalizeWeComUserID(employee.UserID); normalizeErr != nil || strings.TrimSpace(employee.UserID) != employee.UserID || strings.TrimSpace(employee.DisplayName) == "" {
			return nil, ErrEnterpriseDirectoryUnavailable
		}
		if _, duplicate := seen[employee.UserID]; duplicate {
			return nil, ErrEnterpriseDirectoryUnavailable
		}
		seen[employee.UserID] = struct{}{}
	}
	// The Provider projection is complete but does not promise an order. The
	// signed cursor is offset-based, so stabilize the snapshot here as well as
	// in the WeCom adapter; alternate conforming Port implementations cannot
	// make a subsequent page drift merely by returning another order.
	sort.Slice(employees, func(i, j int) bool { return employees[i].UserID < employees[j].UserID })
	return employees, nil
}

func (service *Management) enterpriseItems(ctx context.Context, employees []wecomport.EnterpriseEmployee) ([]EnterpriseEmployeeItem, error) {
	items := make([]EnterpriseEmployeeItem, len(employees))
	ids := make([]string, len(employees))
	for index, employee := range employees {
		if _, err := domain.NormalizeWeComUserID(employee.UserID); err != nil || strings.TrimSpace(employee.DisplayName) == "" {
			return nil, ErrEnterpriseDirectoryUnavailable
		}
		ids[index] = employee.UserID
		items[index] = EnterpriseEmployeeItem{WeComUserID: employee.UserID, DisplayName: employee.DisplayName}
	}
	var existing []domain.User
	if err := service.uow.Within(ctx, func(txContext context.Context) error {
		var readErr error
		existing, readErr = service.repository.UsersByWeComUserIDs(txContext, ids)
		return readErr
	}); err != nil {
		return nil, err
	}
	byUserID := make(map[string]domain.User, len(existing))
	rolesByUserID := make(map[string]domain.Role, len(existing))
	for _, user := range existing {
		role, err := domain.SingleRole(user.Roles)
		if err != nil {
			return nil, domain.ErrConflict
		}
		byUserID[user.WeComUserID] = user
		rolesByUserID[user.WeComUserID] = role
	}
	for index := range items {
		if user, ok := byUserID[items[index].WeComUserID]; ok && user.AccessGrantedAt != nil {
			role := rolesByUserID[user.WeComUserID]
			items[index].AuthorizedAccount, items[index].Role, items[index].LoginEnabled = true, &role, boolPointer(user.LoginEnabled)
		}
	}
	return items, nil
}

func (service *Management) ProvisionEnterpriseEmployee(ctx context.Context, actor domain.Principal, input ProvisionEnterpriseEmployeeInput) (domain.User, error) {
	if len(service.governanceKey) == 0 {
		return domain.User{}, ErrEnterpriseDirectoryUnavailable
	}
	if input.Role != domain.RoleAdmin && input.Role != domain.RoleViewer || !validGovernanceIdempotencyKey(input.IdempotencyKey) {
		return domain.User{}, domain.ErrInvalidInput
	}
	if err := service.authorizeGovernanceRead(ctx, actor); err != nil {
		return domain.User{}, err
	}
	employee, err := service.verifiedEnterpriseEmployee(ctx, input.WeComUserID)
	if err != nil {
		return domain.User{}, err
	}
	password, _, err := credential.IssueOpaque("access_")
	if err != nil {
		return domain.User{}, err
	}
	passwordHash, err := service.passwords.Hash(password)
	if err != nil {
		return domain.User{}, err
	}
	var result domain.User
	err = service.uow.Within(ctx, func(txContext context.Context) error {
		_, role, err := service.currentGovernanceActor(txContext, actor)
		if err != nil {
			return err
		}
		if !canProvision(role, input.Role) {
			return domain.ErrPermissionDenied
		}
		digest := service.governanceDigest("provision", employee.UserID, string(input.Role))
		reserved, err := service.repository.ReserveGovernanceMutation(txContext, actor.InternalID, input.IdempotencyKey, "provision_"+string(input.Role), actor.InternalID, digest, service.now().UTC())
		if err != nil {
			return err
		}
		if !reserved {
			result, err = service.repository.UserByWeComUserID(txContext, employee.UserID, false)
			return err
		}
		existing, lookupErr := service.repository.UserByWeComUserID(txContext, employee.UserID, true)
		if lookupErr == nil {
			if existing.AccessGrantedAt != nil {
				return domain.ErrConflict
			}
			if err = service.repository.GrantAccess(txContext, existing.ID, input.Role, employee.DisplayName, service.now().UTC()); err != nil {
				return err
			}
			result, err = service.repository.UserByID(txContext, existing.ID, false)
			if err != nil {
				return err
			}
			return service.audit(txContext, actor.InternalID, result.ID, "provision_"+string(input.Role), map[string]any{"role": input.Role, "wecom_verified": true, "existing_staff_projection": true})
		}
		if !errors.Is(lookupErr, domain.ErrNotFound) {
			return lookupErr
		}
		hash := sha256.Sum256([]byte(employee.UserID))
		result, err = service.repository.CreateUser(txContext, domain.User{Username: "wecom-staff-" + hex.EncodeToString(hash[:]), PasswordHash: passwordHash, DisplayName: employee.DisplayName, WeComUserID: employee.UserID, Active: true, Roles: []domain.Role{input.Role}})
		if err != nil {
			return err
		}
		return service.audit(txContext, actor.InternalID, result.ID, "provision_"+string(input.Role), map[string]any{"role": input.Role, "wecom_verified": true})
	})
	return result, err
}

func (service *Management) SetGovernanceLoginEnabled(ctx context.Context, actor domain.Principal, input SetLoginEnabledInput) error {
	if len(service.governanceKey) == 0 {
		return ErrEnterpriseDirectoryUnavailable
	}
	return service.withGovernanceTarget(ctx, actor, input.TargetID, input.IdempotencyKey, "set_login_enabled", service.governanceDigest("login", boolString(input.LoginEnabled)), func(txContext context.Context, actorRole domain.Role, target domain.User, targetRole domain.Role) error {
		if !canManageTarget(actorRole, targetRole) {
			return domain.ErrPermissionDenied
		}
		if (!target.Active && !(input.LoginEnabled && target.LegacyLoginReactivationPending)) || target.LoginEnabled == input.LoginEnabled {
			if !target.Active && !(input.LoginEnabled && target.LegacyLoginReactivationPending) {
				return domain.ErrConflict
			}
			return nil
		}
		if err := service.repository.SetLoginEnabled(txContext, target.ID, input.LoginEnabled, service.now().UTC()); err != nil {
			return err
		}
		action := "disable_login"
		if input.LoginEnabled {
			action = "enable_login"
		}
		return service.audit(txContext, actor.InternalID, target.ID, action, map[string]any{"login_enabled": input.LoginEnabled})
	})
}

func (service *Management) SetGovernanceRole(ctx context.Context, actor domain.Principal, input SetRoleInput) error {
	if len(service.governanceKey) == 0 {
		return ErrEnterpriseDirectoryUnavailable
	}
	if input.Role != domain.RoleAdmin && input.Role != domain.RoleViewer {
		return domain.ErrInvalidInput
	}
	return service.withGovernanceTarget(ctx, actor, input.TargetID, input.IdempotencyKey, "set_role", service.governanceDigest("role", string(input.Role)), func(txContext context.Context, actorRole domain.Role, target domain.User, targetRole domain.Role) error {
		if !canManageTarget(actorRole, targetRole) || actorRole != domain.RoleSuperAdmin {
			return domain.ErrPermissionDenied
		}
		if targetRole == input.Role {
			return nil
		}
		if err := service.repository.ReplaceRoles(txContext, target.ID, []domain.Role{input.Role}, service.now().UTC()); err != nil {
			return err
		}
		return service.audit(txContext, actor.InternalID, target.ID, "set_role", map[string]any{"role": input.Role})
	})
}

func (service *Management) BindGovernanceWeComUserID(ctx context.Context, actor domain.Principal, input BindEnterpriseEmployeeInput) error {
	if len(service.governanceKey) == 0 {
		return ErrEnterpriseDirectoryUnavailable
	}
	if !validGovernanceIdempotencyKey(input.IdempotencyKey) || input.TargetID < 1 {
		return domain.ErrInvalidInput
	}
	if err := service.authorizeGovernanceRead(ctx, actor); err != nil {
		return err
	}
	var employee wecomport.EnterpriseEmployee
	var err error
	if input.WeComUserID != "" {
		employee, err = service.verifiedEnterpriseEmployee(ctx, input.WeComUserID)
		if err != nil {
			return err
		}
	}
	return service.withGovernanceTarget(ctx, actor, input.TargetID, input.IdempotencyKey, "bind_wecom_userid", service.governanceDigest("bind", employee.UserID), func(txContext context.Context, actorRole domain.Role, target domain.User, targetRole domain.Role) error {
		if actorRole != domain.RoleSuperAdmin || targetRole == domain.RoleSuperAdmin {
			return domain.ErrPermissionDenied
		}
		if target.WeComUserID == employee.UserID {
			return nil
		}
		if employee.UserID != "" {
			if existing, lookupErr := service.repository.UserByWeComUserID(txContext, employee.UserID, true); lookupErr == nil && existing.ID != target.ID {
				return domain.ErrConflict
			} else if lookupErr != nil && !errors.Is(lookupErr, domain.ErrNotFound) {
				return lookupErr
			}
		}
		if err := service.repository.SetWeComUserID(txContext, target.ID, employee.UserID, service.now().UTC()); err != nil {
			return err
		}
		return service.audit(txContext, actor.InternalID, target.ID, "bind_wecom_userid", map[string]any{"bound": employee.UserID != ""})
	})
}

func (service *Management) ResetGovernancePassword(ctx context.Context, actor domain.Principal, input ResetPasswordInput) error {
	if len(service.governanceKey) == 0 {
		return ErrEnterpriseDirectoryUnavailable
	}
	if !validGovernanceIdempotencyKey(input.IdempotencyKey) || input.TargetID < 1 {
		return domain.ErrInvalidInput
	}
	passwordHash, err := service.passwords.Hash(input.Password)
	if err != nil {
		return passwordInputError(err)
	}
	return service.withGovernanceTarget(ctx, actor, input.TargetID, input.IdempotencyKey, "reset_password", service.governanceDigest("password", input.Password), func(txContext context.Context, actorRole domain.Role, target domain.User, targetRole domain.Role) error {
		if actorRole != domain.RoleSuperAdmin || targetRole == domain.RoleSuperAdmin {
			return domain.ErrPermissionDenied
		}
		if err := service.repository.SetPasswordHash(txContext, target.ID, passwordHash, service.now().UTC()); err != nil {
			return err
		}
		return service.audit(txContext, actor.InternalID, target.ID, "reset_password", nil)
	})
}

func (service *Management) TransferGovernanceSuperAdmin(ctx context.Context, actor domain.Principal, input TransferSuperAdminInput) error {
	if len(service.governanceKey) == 0 {
		return ErrEnterpriseDirectoryUnavailable
	}
	if input.TargetID < 1 || !validGovernanceIdempotencyKey(input.IdempotencyKey) {
		return domain.ErrInvalidInput
	}
	return service.uow.Within(ctx, func(txContext context.Context) error {
		current, role, err := service.currentGovernanceActor(txContext, actor)
		if err != nil {
			return err
		}
		if role != domain.RoleSuperAdmin || current.ID == input.TargetID {
			return domain.ErrPermissionDenied
		}
		control, err := service.repository.SuperAdminControl(txContext, true)
		if err != nil {
			return err
		}
		if control.AdminUserID != current.ID {
			return domain.ErrAuthentication
		}
		target, err := service.repository.UserByID(txContext, input.TargetID, true)
		if err != nil {
			return err
		}
		targetRole, err := domain.SingleRole(target.Roles)
		if err != nil {
			return domain.ErrConflict
		}
		if target.AccessGrantedAt == nil {
			return domain.ErrNotFound
		}
		if !target.Active || !target.LoginEnabled || target.AccessGrantedAt == nil || targetRole != domain.RoleAdmin {
			return domain.ErrPermissionDenied
		}
		reserved, err := service.repository.ReserveGovernanceMutation(txContext, current.ID, input.IdempotencyKey, "transfer_super_admin", target.ID, service.governanceDigest("transfer", integerString(target.ID)), service.now().UTC())
		if err != nil {
			return err
		}
		if !reserved {
			return nil
		}
		if err = service.repository.ReplaceRoles(txContext, current.ID, []domain.Role{domain.RoleAdmin}, service.now().UTC()); err != nil {
			return err
		}
		if err = service.repository.ReplaceRoles(txContext, target.ID, []domain.Role{domain.RoleSuperAdmin}, service.now().UTC()); err != nil {
			return err
		}
		if err = service.repository.SetSuperAdminControl(txContext, target.ID, service.now().UTC()); err != nil {
			return err
		}
		return service.audit(txContext, current.ID, target.ID, "transfer_super_admin", map[string]any{"previous_super_admin_id": current.ID})
	})
}

func (service *Management) withGovernanceTarget(ctx context.Context, actor domain.Principal, targetID int64, key, action string, digest [32]byte, apply func(context.Context, domain.Role, domain.User, domain.Role) error) error {
	if targetID < 1 || !validGovernanceIdempotencyKey(key) {
		return domain.ErrInvalidInput
	}
	return service.uow.Within(ctx, func(txContext context.Context) error {
		_, actorRole, err := service.currentGovernanceActor(txContext, actor)
		if err != nil {
			return err
		}
		target, err := service.repository.UserByID(txContext, targetID, true)
		if err != nil {
			return err
		}
		targetRole, err := domain.SingleRole(target.Roles)
		if err != nil {
			return domain.ErrConflict
		}
		if target.AccessGrantedAt == nil {
			return domain.ErrNotFound
		}
		if !canAnyGovernanceMutation(actorRole, targetRole) {
			return domain.ErrPermissionDenied
		}
		reserved, err := service.repository.ReserveGovernanceMutation(txContext, actor.InternalID, key, action, targetID, digest, service.now().UTC())
		if err != nil || !reserved {
			return err
		}
		return apply(txContext, actorRole, target, targetRole)
	})
}

func (service *Management) currentGovernanceActor(ctx context.Context, actor domain.Principal) (domain.User, domain.Role, error) {
	if actor.Kind != domain.KindAdmin || actor.InternalID < 1 || actor.SessionVersion < 1 {
		return domain.User{}, "", domain.ErrAuthentication
	}
	user, err := service.repository.UserByID(ctx, actor.InternalID, true)
	if err != nil {
		return domain.User{}, "", err
	}
	if !user.Active || !user.LoginEnabled || user.AccessGrantedAt == nil || user.SessionVersion != actor.SessionVersion {
		return domain.User{}, "", domain.ErrAuthentication
	}
	role, err := domain.SingleRole(user.Roles)
	if err != nil {
		return domain.User{}, "", domain.ErrConflict
	}
	if role != domain.RoleSuperAdmin && role != domain.RoleAdmin {
		return domain.User{}, "", domain.ErrPermissionDenied
	}
	return user, role, nil
}

func (service *Management) authorizeGovernanceRead(ctx context.Context, actor domain.Principal) error {
	return service.uow.Within(ctx, func(txContext context.Context) error {
		_, _, err := service.currentGovernanceActor(txContext, actor)
		return err
	})
}

func (service *Management) verifiedEnterpriseEmployee(ctx context.Context, raw string) (wecomport.EnterpriseEmployee, error) {
	userID, err := domain.NormalizeWeComUserID(raw)
	if err != nil || userID != raw {
		return wecomport.EnterpriseEmployee{}, domain.ErrInvalidInput
	}
	if service.enterpriseDirectory == nil || !service.enterpriseDirectory.EnterpriseDirectoryReady() {
		return wecomport.EnterpriseEmployee{}, ErrEnterpriseDirectoryUnavailable
	}
	employee, err := service.enterpriseDirectory.ReadEnterpriseEmployee(ctx, userID)
	if errors.Is(err, wecomport.ErrEnterpriseEmployeeNotFound) {
		return wecomport.EnterpriseEmployee{}, domain.ErrNotFound
	}
	if err != nil {
		return wecomport.EnterpriseEmployee{}, ErrEnterpriseDirectoryUnavailable
	}
	if employee.UserID != userID || strings.TrimSpace(employee.DisplayName) == "" {
		return wecomport.EnterpriseEmployee{}, ErrEnterpriseDirectoryUnavailable
	}
	return employee, nil
}

func capabilitiesFor(role domain.Role) GovernanceCapabilities {
	return GovernanceCapabilities{ProvisionAdmin: role == domain.RoleSuperAdmin, ProvisionViewer: role == domain.RoleAdmin || role == domain.RoleSuperAdmin, TransferSuperAdmin: role == domain.RoleSuperAdmin}
}
func canProvision(actor, wanted domain.Role) bool {
	return (actor == domain.RoleSuperAdmin && (wanted == domain.RoleAdmin || wanted == domain.RoleViewer)) || (actor == domain.RoleAdmin && wanted == domain.RoleViewer)
}
func canManageTarget(actor, target domain.Role) bool {
	return (actor == domain.RoleSuperAdmin && (target == domain.RoleAdmin || target == domain.RoleViewer)) || (actor == domain.RoleAdmin && target == domain.RoleViewer)
}
func canAnyGovernanceMutation(actor, target domain.Role) bool {
	return canManageTarget(actor, target) || (actor == domain.RoleSuperAdmin && target == domain.RoleSuperAdmin)
}
func actionsFor(actorRole domain.Role, actorID int64, target domain.User, targetRole domain.Role) GovernanceActions {
	if target.ID == actorID || targetRole == domain.RoleSuperAdmin {
		return GovernanceActions{TransferSuperAdmin: actorRole == domain.RoleSuperAdmin && target.ID != actorID && target.Active && target.LoginEnabled && target.AccessGrantedAt != nil && targetRole == domain.RoleAdmin}
	}
	canManage := canManageTarget(actorRole, targetRole)
	return GovernanceActions{SetLoginEnabled: canManage, ChangeRole: actorRole == domain.RoleSuperAdmin && canManage, BindWeComUserID: actorRole == domain.RoleSuperAdmin && canManage, ResetPassword: actorRole == domain.RoleSuperAdmin && canManage, TransferSuperAdmin: actorRole == domain.RoleSuperAdmin && target.Active && target.LoginEnabled && target.AccessGrantedAt != nil && targetRole == domain.RoleAdmin}
}
func validGovernanceIdempotencyKey(key string) bool {
	return strings.TrimSpace(key) != "" && len(strings.TrimSpace(key)) <= 200 && !strings.ContainsAny(key, "\x00\r\n")
}
func (service *Management) governanceDigest(parts ...string) [32]byte {
	mac := hmac.New(sha256.New, service.governanceKey)
	_, _ = mac.Write([]byte(strings.Join(parts, "\x00")))
	var digest [32]byte
	copy(digest[:], mac.Sum(nil))
	return digest
}
func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}
func integerString(value int64) string { return strconv.FormatInt(value, 10) }
func boolPointer(value bool) *bool     { return &value }

const (
	enterpriseCursorBrowse = "browse"
	enterpriseCursorSearch = "search"
)

type enterpriseDirectoryCursor struct {
	Mode        string `json:"m"`
	Offset      int    `json:"o,omitempty"`
	QueryDigest string `json:"q"`
	Corp        string `json:"c"`
	Actor       int64  `json:"a"`
	Version     int64  `json:"v"`
}

func (service *Management) encodeEnterpriseCursor(value enterpriseDirectoryCursor, actor domain.Principal, query string) string {
	value.QueryDigest = service.enterpriseQueryDigest(query)
	value.Corp, value.Actor, value.Version = service.enterpriseCorpScope, actor.InternalID, actor.SessionVersion
	payload, _ := json.Marshal(value)
	mac := hmac.New(sha256.New, service.enterpriseCursorKey)
	_, _ = mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(append(payload, mac.Sum(nil)...))
}

func (service *Management) decodeEnterpriseCursor(cursor string, actor domain.Principal, query, expectedMode string) (enterpriseDirectoryCursor, error) {
	if cursor == "" {
		return enterpriseDirectoryCursor{Mode: expectedMode}, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || len(raw) <= sha256.Size {
		return enterpriseDirectoryCursor{}, domain.ErrInvalidInput
	}
	payload, signature := raw[:len(raw)-sha256.Size], raw[len(raw)-sha256.Size:]
	mac := hmac.New(sha256.New, service.enterpriseCursorKey)
	_, _ = mac.Write(payload)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return enterpriseDirectoryCursor{}, domain.ErrInvalidInput
	}
	var value enterpriseDirectoryCursor
	if json.Unmarshal(payload, &value) != nil || value.Mode != expectedMode || value.Offset < 0 || value.QueryDigest != service.enterpriseQueryDigest(query) || value.Corp != service.enterpriseCorpScope || value.Actor != actor.InternalID || value.Version != actor.SessionVersion {
		return enterpriseDirectoryCursor{}, domain.ErrInvalidInput
	}
	return value, nil
}

func (service *Management) enterpriseQueryDigest(query string) string {
	mac := hmac.New(sha256.New, service.enterpriseCursorKey)
	_, _ = mac.Write([]byte("access-enterprise-query-v1\x00" + query))
	return hex.EncodeToString(mac.Sum(nil))
}

// RefreshStaffNames reads the provider before opening the update transaction.
// It updates existing bindings only and cannot grant login or create staff.
func (service *Management) RefreshStaffNames(ctx context.Context, actor domain.Principal) error {
	if err := service.authorizeGovernanceRead(ctx, actor); err != nil {
		return err
	}
	if service.enterpriseDirectory == nil || !service.enterpriseDirectory.EnterpriseDirectoryReady() {
		return ErrEnterpriseDirectoryUnavailable
	}
	writer, ok := service.repository.(accessport.StaffNameWriter)
	if !ok {
		return ErrEnterpriseDirectoryUnavailable
	}
	employees, err := service.completeEnterpriseDirectory(ctx)
	if err != nil {
		return err
	}
	names := make(map[string]string, len(employees))
	for _, employee := range employees {
		names[employee.UserID] = strings.TrimSpace(employee.DisplayName)
	}
	return service.uow.Within(ctx, func(tx context.Context) error {
		if _, _, err := service.currentGovernanceActor(tx, actor); err != nil {
			return err
		}
		users, err := service.repository.ListUsers(tx)
		if err != nil {
			return err
		}
		for _, user := range users {
			name := names[user.WeComUserID]
			if name == "" || name == user.DisplayName {
				continue
			}
			if err := writer.SetStaffDisplayName(tx, user.ID, user.WeComUserID, name, service.now().UTC()); err != nil {
				return err
			}
			if err := service.audit(tx, actor.InternalID, user.ID, "refresh_staff_name", nil); err != nil {
				return err
			}
		}
		return nil
	})
}
