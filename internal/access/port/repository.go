package port

import (
	"context"
	"time"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
)

// Repository owns only access tables. Mutating methods must reject contexts
// without the transaction installed by platform UnitOfWork.
type Repository interface {
	CountUsers(context.Context) (int64, error)
	UserByID(context.Context, int64, bool) (domain.User, error)
	UserByUsername(context.Context, string, bool) (domain.User, error)
	UserByWeComUserID(context.Context, string, bool) (domain.User, error)
	// UsersByWeComUserIDs is a bounded Access-owned batch projection for an
	// already provider-verified directory page. It never discovers employees.
	UsersByWeComUserIDs(context.Context, []string) ([]domain.User, error)
	ListUsers(context.Context) ([]domain.User, error)
	CreateUser(context.Context, domain.User) (domain.User, error)
	// CreateStaffProjection records a provider-verified business staff member
	// without granting any CRM login. It owns no customer identity.
	CreateStaffProjection(context.Context, domain.User) (domain.User, error)
	BootstrapUser(context.Context, domain.User) (domain.User, bool, error)
	SetLoginEnabled(context.Context, int64, bool, time.Time) error
	// GrantAccess atomically gives an existing, active staff projection its
	// first login grant and exactly one non-super role.
	GrantAccess(context.Context, int64, domain.Role, string, time.Time) error
	// ReserveLoginAccessRequest records the full desired state for the frozen
	// AdminOps compatibility route. It is scoped to the acting admin and must
	// share the transaction with access mutations; exact replays return false
	// and payload drift returns domain.ErrConflict.
	ReserveLoginAccessRequest(context.Context, int64, string, [32]byte, time.Time) (bool, error)
	SetWeComUserID(context.Context, int64, string, time.Time) error
	ReplaceRoles(context.Context, int64, []domain.Role, time.Time) error
	SetPasswordHash(context.Context, int64, string, time.Time) error
	SetLastLogin(context.Context, int64, time.Time) error

	CreateSession(context.Context, domain.Session) (domain.Session, error)
	SessionByTokenDigest(context.Context, [32]byte, bool) (domain.Session, error)
	TouchSession(context.Context, int64, time.Time) error
	RevokeSession(context.Context, [32]byte, string, time.Time) (bool, error)

	LoginRateLimit(context.Context, [32]byte, bool) (domain.LoginRateLimit, error)
	SaveLoginRateLimit(context.Context, domain.LoginRateLimit) error
	AppendLoginAudit(context.Context, domain.LoginAudit) error
	AppendAccessAudit(context.Context, domain.AccessAudit) error

	// Governance state is Access-owned. Every method below is invoked inside
	// the same UOW as role, session-version and audit changes.
	SuperAdminControl(context.Context, bool) (domain.SuperAdminControl, error)
	InitializeSuperAdminControl(context.Context, int64, time.Time) error
	SetSuperAdminControl(context.Context, int64, time.Time) error
	ReserveGovernanceMutation(context.Context, int64, string, string, int64, [32]byte, time.Time) (bool, error)
}
