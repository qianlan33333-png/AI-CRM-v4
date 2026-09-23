package app

import (
	"context"

	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

// ProductTargetBatchStore is Product-owned persistence for the historical
// Coupon target name projection. It receives a typed, bounded request and
// never exposes its tables to a consuming domain.
type ProductTargetBatchStore interface {
	ReadProductTargets(context.Context, []productport.ProductTargetReference) ([]productport.ProductTargetLookup, error)
}

// TargetBatchReader owns the Unit of Work around one typed Product batch read.
// It is intentionally separate from TargetReader, whose single-target methods
// are checkout and lifecycle reads with different invariants.
type TargetBatchReader struct {
	uow   platformport.UnitOfWork
	store ProductTargetBatchStore
}

func NewTargetBatchReader(uow platformport.UnitOfWork, store ProductTargetBatchStore) (*TargetBatchReader, error) {
	if uow == nil || store == nil {
		return nil, ErrUnavailable
	}
	return &TargetBatchReader{uow: uow, store: store}, nil
}

func (reader *TargetBatchReader) ReadProductTargets(ctx context.Context, references []productport.ProductTargetReference) ([]productport.ProductTargetLookup, error) {
	if reader == nil || reader.uow == nil || reader.store == nil || len(references) > productport.ProductTargetBatchMaximum {
		return nil, ErrUnavailable
	}
	seen := make(map[productport.ProductTargetReference]struct{}, len(references))
	for _, reference := range references {
		if reference.ID < 1 || (reference.ProductType != productport.ProductOptionStandard && reference.ProductType != productport.ProductOptionServicePeriod) {
			return nil, ErrUnavailable
		}
		if _, duplicate := seen[reference]; duplicate {
			return nil, ErrUnavailable
		}
		seen[reference] = struct{}{}
	}
	if len(references) == 0 {
		return []productport.ProductTargetLookup{}, nil
	}
	var result []productport.ProductTargetLookup
	if err := reader.uow.Within(ctx, func(tx context.Context) error {
		var err error
		result, err = reader.store.ReadProductTargets(tx, references)
		return err
	}); err != nil {
		return nil, ErrUnavailable
	}
	if len(result) != len(references) {
		return nil, ErrUnavailable
	}
	for index, lookup := range result {
		if lookup.Reference != references[index] || lookup.Found && lookup.Name == "" {
			return nil, ErrUnavailable
		}
	}
	return result, nil
}

var _ productport.ProductTargetBatchReader = (*TargetBatchReader)(nil)
