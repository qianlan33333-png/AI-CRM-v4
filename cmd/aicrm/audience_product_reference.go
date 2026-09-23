package main

import (
	"context"
	"errors"
	"strings"

	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

// audienceProductReferenceAdapter keeps audience configuration on the
// Product-owned selection projection. A value is accepted only when it is an
// exact stable code or identifies exactly one Product display name.
type audienceProductReferenceAdapter struct {
	products   productport.ProductOptionReader
	historical orderport.HistoricalAudienceProductReader
	uow        interface {
		Within(context.Context, func(context.Context) error) error
	}
}

func (a audienceProductReferenceAdapter) ResolveAudienceProduct(ctx context.Context, value string) (string, bool, error) {
	if a.products == nil {
		return "", false, errors.New("product option reader is required")
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false, nil
	}
	page, err := a.products.ListProductOptions(ctx, productport.ProductOptionQuery{
		Q:           value,
		ProductType: productport.ProductOptionAll,
		Limit:       productport.ProductOptionMaximumLimit,
	})
	if err != nil {
		return "", false, err
	}
	// A bounded Product option page cannot prove title uniqueness if additional
	// matching rows were omitted. Fail closed rather than guessing a title.
	if page.Total > int64(len(page.Items)) {
		return "", false, nil
	}
	for _, item := range page.Items {
		if item.Code == value {
			return item.Code, true, nil
		}
	}
	if a.historical != nil {
		if a.uow == nil {
			return "", false, errors.New("historical product transaction unavailable")
		}
		var found bool
		if err := a.uow.Within(ctx, func(tx context.Context) error {
			var readErr error
			found, readErr = a.historical.HistoricalAudienceProductCodeExists(tx, value)
			return readErr
		}); err != nil {
			return "", false, err
		}
		if found {
			return value, true, nil
		}
	}
	titleCode := ""
	for _, item := range page.Items {
		if item.Name != value {
			continue
		}
		if titleCode != "" && titleCode != item.Code {
			return "", false, nil
		}
		titleCode = item.Code
	}
	return titleCode, titleCode != "", nil
}
