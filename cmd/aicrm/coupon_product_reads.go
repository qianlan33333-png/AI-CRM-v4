package main

import (
	"context"
	"errors"

	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

// couponProductReads joins two Product-owned read ports only at the
// composition root. Coupon receives the narrow chooser and typed target batch
// interfaces, never a Product app, repository, or table.
type couponProductReads struct {
	options productport.ProductOptionReader
	targets productport.ProductTargetBatchReader
}

func newCouponProductReads(options productport.ProductOptionReader, targets productport.ProductTargetBatchReader) (couponProductReads, error) {
	if options == nil || targets == nil {
		return couponProductReads{}, errors.New("coupon product readers are required")
	}
	return couponProductReads{options: options, targets: targets}, nil
}

func (reads couponProductReads) ListProductOptions(ctx context.Context, query productport.ProductOptionQuery) (productport.ProductOptionPage, error) {
	if reads.options == nil {
		return productport.ProductOptionPage{}, errors.New("coupon product option reader is required")
	}
	return reads.options.ListProductOptions(ctx, query)
}

func (reads couponProductReads) ReadProductTargets(ctx context.Context, references []productport.ProductTargetReference) ([]productport.ProductTargetLookup, error) {
	if reads.targets == nil {
		return nil, errors.New("coupon product target reader is required")
	}
	return reads.targets.ReadProductTargets(ctx, references)
}

var _ productport.ProductOptionReader = couponProductReads{}
var _ productport.ProductTargetBatchReader = couponProductReads{}
