package app

import (
	"context"
	"strings"
	"time"

	distributiondomain "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/domain"
	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

type policyStore interface {
	Within(context.Context, func(context.Context) error) error
	ReadProductPolicyWithin(context.Context, int64, distributiondomain.ProductType) (distributiondomain.Policy, error)
	ReadProductPolicyForUpdateWithin(context.Context, int64, distributiondomain.ProductType) (distributiondomain.Policy, error)
	InsertProductPolicyWithin(context.Context, distributiondomain.Policy) (distributiondomain.Policy, error)
	UpdateProductPolicyWithin(context.Context, distributiondomain.Policy, int64) (distributiondomain.Policy, error)
	ProductPolicyIDWithin(context.Context, int64, distributiondomain.ProductType) (int64, error)
	AppendPolicyVersionWithin(context.Context, int64, distributiondomain.Policy, string, time.Time) error
	AppendAuditWithin(context.Context, string, string, int64, string, any, time.Time) error
	AppendOutboxWithin(context.Context, string, string, int64, any, time.Time) error
}

// PolicyService is called by Product while Product's UoW is active. It never
// opens a second transaction, so Product snapshot and policy revision commit
// or roll back together.
type PolicyService struct {
	store policyStore
	now   func() time.Time
}

func NewPolicyService(store policyStore) *PolicyService {
	return &PolicyService{store: store, now: time.Now}
}

func (s *PolicyService) SaveProductPolicyWithin(ctx context.Context, command distributionport.PolicyCommand) (distributiondomain.Policy, error) {
	if s == nil || s.store == nil || command.ProductID < 1 || !command.ProductType.Valid() || command.CommissionRateBasisPoints < 0 || command.CommissionRateBasisPoints > distributiondomain.MaximumCommissionRateBasisPoints || command.WaitDays < 0 || command.WaitDays > distributiondomain.MaximumWaitDays || command.ExpectedVersion < 0 || strings.TrimSpace(command.ActorScope) == "" || len(command.ActorScope) > 200 || strings.TrimSpace(command.IdempotencyKey) == "" || len(command.IdempotencyKey) > 128 {
		return distributiondomain.Policy{}, distributionport.ErrConflict
	}
	if _, err := platformpostgres.RequireTransaction(ctx); err != nil {
		return distributiondomain.Policy{}, distributionport.ErrUnavailable
	}
	now := s.now().UTC()
	if now.IsZero() {
		return distributiondomain.Policy{}, distributionport.ErrUnavailable
	}
	current, err := s.store.ReadProductPolicyForUpdateWithin(ctx, command.ProductID, command.ProductType)
	if err != nil && err != distributionport.ErrNotFound {
		return distributiondomain.Policy{}, err
	}
	var saved distributiondomain.Policy
	if err == distributionport.ErrNotFound {
		if command.ExpectedVersion != 0 {
			return distributiondomain.Policy{}, distributionport.ErrConflict
		}
		saved, err = distributiondomain.NewPolicy(command.ProductID, command.ProductType, command.Enabled, command.CommissionRateBasisPoints, command.WaitDays, now)
		if err == nil {
			saved, err = s.store.InsertProductPolicyWithin(ctx, saved)
		}
	} else {
		if command.ExpectedVersion != current.Version {
			return distributiondomain.Policy{}, distributionport.ErrConflict
		}
		saved, err = current.Update(command.ExpectedVersion, command.Enabled, command.CommissionRateBasisPoints, command.WaitDays, now)
		if err == nil {
			saved, err = s.store.UpdateProductPolicyWithin(ctx, saved, command.ExpectedVersion)
		}
	}
	if err != nil {
		return distributiondomain.Policy{}, err
	}
	policyID, err := s.store.ProductPolicyIDWithin(ctx, command.ProductID, command.ProductType)
	if err != nil {
		return distributiondomain.Policy{}, err
	}
	if err = s.store.AppendPolicyVersionWithin(ctx, policyID, saved, command.ActorScope, now); err != nil {
		return distributiondomain.Policy{}, err
	}
	payload := struct {
		ProductID int64 `json:"product_id"`
		Version   int64 `json:"version"`
	}{saved.ProductID, saved.Version}
	if err = s.store.AppendAuditWithin(ctx, "distribution.policy_saved.v1", "policy", policyID, command.ActorScope, payload, now); err != nil {
		return distributiondomain.Policy{}, err
	}
	if err = s.store.AppendOutboxWithin(ctx, "distribution.policy_saved.v1", "distribution.policy:"+command.IdempotencyKey, policyID, payload, now); err != nil {
		return distributiondomain.Policy{}, err
	}
	return saved, nil
}

func (s *PolicyService) ReadProductPolicyWithin(ctx context.Context, productID int64, productType distributiondomain.ProductType) (distributiondomain.Policy, error) {
	if s == nil || s.store == nil {
		return distributiondomain.Policy{}, distributionport.ErrUnavailable
	}
	return s.store.ReadProductPolicyWithin(ctx, productID, productType)
}

// ReadProductPolicy is the public read facade for HTTP projections. Product
// uses ReadProductPolicyWithin while it holds its own mutation UoW; read-only
// callers use this method so the repository never receives a fake transaction.
func (s *PolicyService) ReadProductPolicy(ctx context.Context, productID int64, productType distributiondomain.ProductType) (distributiondomain.Policy, error) {
	if s == nil || s.store == nil || productID < 1 || !productType.Valid() {
		return distributiondomain.Policy{}, distributionport.ErrUnavailable
	}
	var policy distributiondomain.Policy
	err := s.store.Within(ctx, func(tx context.Context) error {
		var err error
		policy, err = s.store.ReadProductPolicyWithin(tx, productID, productType)
		return err
	})
	if err != nil {
		return distributiondomain.Policy{}, err
	}
	return policy, nil
}

var _ distributionport.ProductPolicyService = (*PolicyService)(nil)
