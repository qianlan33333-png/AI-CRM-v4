package app

import (
	"context"
	"errors"
	"testing"

	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

type productOptionTestStore struct {
	*productTestStore
	page  productport.ProductOptionPage
	query productport.ProductOptionQuery
}

func (store *productOptionTestStore) ListProductOptions(_ context.Context, query productport.ProductOptionQuery) (productport.ProductOptionPage, error) {
	store.query = query
	return store.page, nil
}

func TestListProductOptionsUsesCanonicalBoundedProjection(t *testing.T) {
	store := &productOptionTestStore{
		productTestStore: &productTestStore{},
		page: productport.ProductOptionPage{Items: []productport.ProductOption{
			{ID: 4, Code: "standard-v3", ProductType: productport.ProductOptionStandard, Name: "标准商品", PriceMinor: 9900, Currency: "CNY"},
			{ID: 9, Code: "period-v3", ProductType: productport.ProductOptionServicePeriod, Name: "周期商品", PriceMinor: 19900, Currency: "CNY"},
		}, Total: 12, Limit: 20, Offset: 10},
	}
	service := NewService(&productTestUoW{}, store, &productTestEvents{})

	page, err := service.ListProductOptions(context.Background(), productport.ProductOptionQuery{Q: "  商品 ", Limit: 20, Offset: 10})
	if err != nil {
		t.Fatalf("ListProductOptions() error = %v", err)
	}
	if store.query.Q != "商品" || store.query.ProductType != productport.ProductOptionAll || store.query.Limit != 20 || store.query.Offset != 10 {
		t.Fatalf("normalized query = %+v", store.query)
	}
	if len(page.Items) != 2 || page.Items[0].ID != 4 || page.Items[1].ProductType != productport.ProductOptionServicePeriod {
		t.Fatalf("page = %+v", page)
	}
}

func TestListProductOptionsRejectsUnboundedOrUnknownQueries(t *testing.T) {
	store := &productOptionTestStore{productTestStore: &productTestStore{}, page: productport.ProductOptionPage{Items: []productport.ProductOption{}, Limit: 50, Offset: 0}}
	service := NewService(&productTestUoW{}, store, &productTestEvents{})
	for _, query := range []productport.ProductOptionQuery{
		{ProductType: "coupons"},
		{Q: "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"},
		{Limit: productport.ProductOptionMaximumLimit + 1},
		{Offset: productport.ProductOptionMaximumOffset + 1},
	} {
		if _, err := service.ListProductOptions(context.Background(), query); !errors.Is(err, productport.ErrInvalidProductOptionQuery) {
			t.Fatalf("query %+v error = %v, want ErrInvalidProductOptionQuery", query, err)
		}
	}
}

func TestListProductOptionsRejectsNonCNYOrWrongTypeProjection(t *testing.T) {
	store := &productOptionTestStore{
		productTestStore: &productTestStore{},
		page:             productport.ProductOptionPage{Items: []productport.ProductOption{{ID: 1, ProductType: productport.ProductOptionStandard, Name: "商品", PriceMinor: 1, Currency: "USD"}}, Total: 1, Limit: 50, Offset: 0},
	}
	service := NewService(&productTestUoW{}, store, &productTestEvents{})
	if _, err := service.ListProductOptions(context.Background(), productport.ProductOptionQuery{}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("invalid projection error = %v, want ErrUnavailable", err)
	}
}
