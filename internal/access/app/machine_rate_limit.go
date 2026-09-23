package app

import (
	"context"
	"errors"
	"net/netip"
	"time"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/access/credential"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accessport "github.com/qianlan33333-png/AI-CRM-v3/internal/access/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

// MachineRequestRateLimitConfig deliberately keeps the two public pressure
// points separate. Credential issuance is kept lower than an authenticated
// machine request so a leaked client ID cannot consume an integration's normal
// API budget.
type MachineRequestRateLimitConfig struct {
	Window                    time.Duration
	MaxClientCredentialChecks int
	MaxMachineRequests        int
	Now                       func() time.Time
}

// MachineRequestRateLimiter reuses Access's locked, durable rate-limit
// repository. The existing storage contains only a hashed key and a window
// counter, which is sufficient here and keeps the machine host out of Access
// persistence.
type MachineRequestRateLimiter struct {
	repository accessport.Repository
	uow        platformport.UnitOfWork
	config     MachineRequestRateLimitConfig
}

var _ accessport.MachineRequestLimiter = (*MachineRequestRateLimiter)(nil)

func NewMachineRequestRateLimiter(repository accessport.Repository, uow platformport.UnitOfWork, config MachineRequestRateLimitConfig) (*MachineRequestRateLimiter, error) {
	if repository == nil || uow == nil {
		return nil, errors.New("machine request rate-limit dependencies are required")
	}
	if config.Window <= 0 {
		config.Window = time.Minute
	}
	if config.MaxClientCredentialChecks <= 0 {
		config.MaxClientCredentialChecks = 30
	}
	if config.MaxMachineRequests <= 0 {
		config.MaxMachineRequests = 300
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &MachineRequestRateLimiter{repository: repository, uow: uow, config: config}, nil
}

// AllowClientCredentials limits an attempt before Access knows whether the
// supplied client exists. It therefore uses one source-only bucket: an attacker
// cannot rotate syntactically valid, nonexistent IDs to reset the quota or
// create one durable row per guessed identifier. Only the bucket digest is
// persisted; client IDs are neither stored nor used in this pre-auth key.
func (limiter *MachineRequestRateLimiter) AllowClientCredentials(ctx context.Context, _ string, source netip.Addr) error {
	if limiter == nil || !source.IsValid() {
		return domain.ErrMachineCredential
	}
	return limiter.allow(ctx, "open-platform:oauth-token-source", source.String(), limiter.config.MaxClientCredentialChecks)
}

// AllowMachineRequest is called only after Access has authenticated and
// reloaded the client. It still keys by source address so a valid caller cannot
// consume another caller's quota by sharing only a credential identifier.
func (limiter *MachineRequestRateLimiter) AllowMachineRequest(ctx context.Context, principal domain.MachinePrincipal, source netip.Addr) error {
	if limiter == nil || principal.ClientRecord < 1 || !source.IsValid() {
		return domain.ErrMachineCredential
	}
	clientID, err := domain.NormalizeMachineClientID(principal.ClientID)
	if err != nil {
		return domain.ErrMachineCredential
	}
	return limiter.allow(ctx, "open-platform:machine-request", clientID+"\x00"+source.String(), limiter.config.MaxMachineRequests)
}

func (limiter *MachineRequestRateLimiter) allow(ctx context.Context, namespace, value string, maximum int) error {
	if maximum < 1 {
		return domain.ErrRateLimited
	}
	now := limiter.config.Now().UTC()
	key := credential.Digest(namespace + "\x00" + value)
	var decision error
	err := limiter.uow.Within(ctx, func(tx context.Context) error {
		limit, err := limiter.repository.LoginRateLimit(tx, key, true)
		if err != nil {
			return err
		}
		if now.Sub(limit.WindowStartedAt) >= limiter.config.Window {
			limit.WindowStartedAt = now
			limit.FailureCount = 0
			limit.BlockedUntil = nil
		}
		if limit.BlockedUntil != nil && now.Before(*limit.BlockedUntil) {
			decision = domain.ErrRateLimited
			return nil
		}
		if limit.FailureCount >= maximum {
			blockedUntil := now.Add(limiter.config.Window)
			limit.BlockedUntil = &blockedUntil
			limit.UpdatedAt = now
			if err = limiter.repository.SaveLoginRateLimit(tx, limit); err != nil {
				return err
			}
			decision = domain.ErrRateLimited
			return nil
		}
		limit.FailureCount++
		limit.UpdatedAt = now
		if limit.FailureCount >= maximum {
			blockedUntil := now.Add(limiter.config.Window)
			limit.BlockedUntil = &blockedUntil
		}
		return limiter.repository.SaveLoginRateLimit(tx, limit)
	})
	if err != nil {
		return err
	}
	return decision
}
