package app

import (
	"context"
	"strings"

	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

// ProductOptionStore is implemented by the Product-owned repository. It is
// kept separate from the mutation Store interface so existing Product
// command fakes cannot accidentally become cross-domain readers.
type ProductOptionStore interface {
	ListProductOptions(context.Context, productport.ProductOptionQuery) (productport.ProductOptionPage, error)
}

// ListProductOptions exposes only the bounded Product selection projection.
// The UnitOfWork boundary is owned here; callers never receive a repository
// or a transaction and therefore cannot read Product tables directly.
func (s *Service) ListProductOptions(ctx context.Context, query productport.ProductOptionQuery) (productport.ProductOptionPage, error) {
	query, err := normalizeProductOptionQuery(query)
	if err != nil {
		return productport.ProductOptionPage{}, err
	}
	if s == nil || s.uow == nil || s.store == nil {
		return productport.ProductOptionPage{}, ErrUnavailable
	}
	store, ok := s.store.(ProductOptionStore)
	if !ok || store == nil {
		return productport.ProductOptionPage{}, ErrUnavailable
	}
	var page productport.ProductOptionPage
	if err := s.uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		page, readErr = store.ListProductOptions(tx, query)
		return readErr
	}); err != nil {
		return productport.ProductOptionPage{}, classify(err)
	}
	if page.Limit != query.Limit || page.Offset != query.Offset || page.Total < 0 || len(page.Items) > int(query.Limit) {
		return productport.ProductOptionPage{}, ErrUnavailable
	}
	if page.Items == nil {
		page.Items = []productport.ProductOption{}
	}
	if int64(query.Offset) > page.Total && len(page.Items) != 0 {
		return productport.ProductOptionPage{}, ErrUnavailable
	}
	var previous productport.ID
	for _, item := range page.Items {
		if !validProductOption(item) || item.ID <= previous || !optionTypeMatches(query.ProductType, item.ProductType) {
			return productport.ProductOptionPage{}, ErrUnavailable
		}
		previous = item.ID
	}
	return page, nil
}

func normalizeProductOptionQuery(query productport.ProductOptionQuery) (productport.ProductOptionQuery, error) {
	query.Q = strings.TrimSpace(query.Q)
	if len(query.Q) > 80 {
		return productport.ProductOptionQuery{}, productport.ErrInvalidProductOptionQuery
	}
	if query.ProductType == "" {
		query.ProductType = productport.ProductOptionAll
	}
	if query.ProductType != productport.ProductOptionAll && query.ProductType != productport.ProductOptionStandard && query.ProductType != productport.ProductOptionServicePeriod {
		return productport.ProductOptionQuery{}, productport.ErrInvalidProductOptionQuery
	}
	if query.Limit == 0 {
		query.Limit = productport.ProductOptionDefaultLimit
	}
	if query.Limit < 1 || query.Limit > productport.ProductOptionMaximumLimit || query.Offset < 0 || query.Offset > productport.ProductOptionMaximumOffset {
		return productport.ProductOptionQuery{}, productport.ErrInvalidProductOptionQuery
	}
	return query, nil
}

func optionTypeMatches(query, value productport.ProductOptionType) bool {
	return query == productport.ProductOptionAll || query == value
}

func validProductOption(item productport.ProductOption) bool {
	return item.ID > 0 && len(item.Code) > 0 && len(item.Code) <= 200 && len(item.Name) > 0 && len(item.Name) <= 200 && item.PriceMinor >= 0 && item.Currency == "CNY" && (item.ProductType == productport.ProductOptionStandard || item.ProductType == productport.ProductOptionServicePeriod)
}

var _ productport.ProductOptionReader = (*Service)(nil)
