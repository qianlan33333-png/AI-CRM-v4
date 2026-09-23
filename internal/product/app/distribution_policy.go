package app

import (
	"context"
	"errors"
	"fmt"

	distributiondomain "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/domain"
	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

// productPolicyWriter is the only Product-to-Distribution dependency. Product
// calls it with the transaction-bound context supplied by its own UnitOfWork.
type productPolicyWriter interface {
	SaveProductPolicyWithin(context.Context, distributionport.PolicyCommand) (distributiondomain.Policy, error)
}

func normalizedDistributionPolicy(value *productport.DistributionPolicy, creating bool) (*productport.DistributionPolicy, error) {
	if value == nil {
		if !creating {
			return nil, nil
		}
		defaultPolicy := productport.DefaultDistributionPolicy()
		return &defaultPolicy, nil
	}
	copy := *value
	if copy.CommissionRateBasisPoints < 0 || copy.CommissionRateBasisPoints > distributiondomain.MaximumCommissionRateBasisPoints || copy.WaitDays < 0 || copy.WaitDays > distributiondomain.MaximumWaitDays || copy.ExpectedVersion < 0 || (creating && copy.ExpectedVersion != 0) {
		return nil, ErrInvalidProduct
	}
	return &copy, nil
}

func saveDistributionPolicyWithin(ctx context.Context, writer productPolicyWriter, productID productport.ID, productType distributiondomain.ProductType, value *productport.DistributionPolicy, actor int64, idempotencyKey string) error {
	if value == nil {
		return nil
	}
	if writer == nil {
		return ErrUnavailable
	}
	_, err := writer.SaveProductPolicyWithin(ctx, distributionport.PolicyCommand{
		ProductID:                 int64(productID),
		ProductType:               productType,
		Enabled:                   value.Enabled,
		CommissionRateBasisPoints: value.CommissionRateBasisPoints,
		WaitDays:                  value.WaitDays,
		ExpectedVersion:           value.ExpectedVersion,
		ActorScope:                fmt.Sprintf("admin:%d", actor),
		IdempotencyKey:            idempotencyKey,
	})
	if err == nil {
		return nil
	}
	if errors.Is(err, distributionport.ErrConflict) {
		return ErrConflict
	}
	return ErrUnavailable
}
