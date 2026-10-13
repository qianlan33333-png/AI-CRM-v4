// Package maintenance owns server-only Access account recovery. It is not
// registered in any HTTP handler and never manufactures a human principal.
package maintenance

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"runtime"
	"time"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/access/credential"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accessport "github.com/qianlan33333-png/AI-CRM-v3/internal/access/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

type Authority struct {
	root     bool
	operator string
}

// HostAuthority cannot be populated from a request or CLI flag. Linux root
// controls the deployed application and database independently of CRM roles.
func HostAuthority(operator string) (Authority, error) {
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		return Authority{}, domain.ErrPermissionDenied
	}
	if operator == "" {
		operator = "root"
	}
	return Authority{root: true, operator: operator}, nil
}

type Passwords interface{ Hash(string) (string, error) }
type Authenticator interface {
	Authenticate(context.Context, string) (domain.Principal, error)
}
type Target struct {
	ID             int64  `json:"id"`
	Username       string `json:"username"`
	SessionVersion int64  `json:"session_version"`
}
type Input struct {
	Target                 Target
	SessionToken           string
	Password               string
	AuthorizationReference string
}
type Service struct {
	repository accessport.Repository
	uow        platformport.UnitOfWork
	passwords  Passwords
	auth       Authenticator
}

func New(repository accessport.Repository, uow platformport.UnitOfWork, passwords Passwords, auth Authenticator) (*Service, error) {
	if repository == nil || uow == nil || passwords == nil || auth == nil {
		return nil, errors.New("recovery dependencies required")
	}
	return &Service{repository, uow, passwords, auth}, nil
}

func (s *Service) target(ctx context.Context, id int64, username string) (domain.User, error) {
	control, err := s.repository.SuperAdminControl(ctx, true)
	if err != nil {
		return domain.User{}, err
	}
	if control.AdminUserID != id {
		return domain.User{}, domain.ErrPermissionDenied
	}
	user, err := s.repository.UserByID(ctx, id, true)
	if err != nil {
		return domain.User{}, err
	}
	role, err := domain.SingleRole(user.Roles)
	if err != nil || role != domain.RoleSuperAdmin || user.Username != username || !user.Active || !user.LoginEnabled || user.AccessGrantedAt == nil {
		return domain.User{}, domain.ErrPermissionDenied
	}
	return user, nil
}
func (s *Service) Inspect(ctx context.Context, authority Authority, id int64, username string) (Target, error) {
	if !authority.root {
		return Target{}, domain.ErrPermissionDenied
	}
	var result Target
	err := s.uow.Within(ctx, func(tx context.Context) error {
		user, err := s.target(tx, id, username)
		if err != nil {
			return err
		}
		result = Target{user.ID, user.Username, user.SessionVersion}
		return nil
	})
	return result, err
}
func (s *Service) Recover(ctx context.Context, authority Authority, input Input) (Target, error) {
	if !authority.root {
		return Target{}, domain.ErrPermissionDenied
	}
	if input.Target.ID < 1 || input.Target.SessionVersion < 1 || input.AuthorizationReference == "" {
		return Target{}, domain.ErrInvalidInput
	}
	normal, err := domain.NormalizeUsername(input.Target.Username)
	if err != nil || normal != input.Target.Username {
		return Target{}, domain.ErrInvalidInput
	}
	actor, err := s.auth.Authenticate(ctx, input.SessionToken)
	if err != nil {
		return Target{}, domain.ErrAuthentication
	}
	if actor.Kind != domain.KindAdmin {
		return Target{}, domain.ErrPermissionDenied
	}
	hash, err := s.passwords.Hash(input.Password)
	if err != nil {
		return Target{}, credential.ErrInvalidPassword
	}
	var result Target
	err = s.uow.Within(ctx, func(tx context.Context) error {
		// Serialize recovery against the existing owner-control row. Concurrent
		// governance may cause a PostgreSQL deadlock rollback, never a split write.
		user, err := s.target(tx, input.Target.ID, input.Target.Username)
		if err != nil {
			return err
		}
		if user.SessionVersion != input.Target.SessionVersion {
			return domain.ErrConflict
		}
		operator, err := s.repository.UserByID(tx, actor.InternalID, true)
		if err != nil {
			return err
		}
		role, err := domain.SingleRole(operator.Roles)
		if err != nil || !operator.Active || !operator.LoginEnabled || operator.AccessGrantedAt == nil || operator.SessionVersion != actor.SessionVersion || (role != domain.RoleAdmin && role != domain.RoleSuperAdmin) {
			return domain.ErrAuthentication
		}
		now := time.Now().UTC()
		if err = s.repository.SetPasswordHash(tx, user.ID, hash, now); err != nil {
			return err
		}
		details, err := json.Marshal(map[string]any{"authority": "linux_root", "operator": authority.operator, "authorization_reference": input.AuthorizationReference, "previous_session_version": user.SessionVersion, "session_version": user.SessionVersion + 1, "entrypoint": "recover-access-password"})
		if err != nil {
			return err
		}
		if err = s.repository.AppendAccessAudit(tx, domain.AccessAudit{ActorAdminUserID: operator.ID, TargetAdminUserID: user.ID, Action: "reset_password", Details: details, CreatedAt: now}); err != nil {
			return err
		}
		result = Target{user.ID, user.Username, user.SessionVersion + 1}
		return nil
	})
	return result, err
}
