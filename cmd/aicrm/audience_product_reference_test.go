package main

import (
	"context"
	"testing"

	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

type audienceProductOptionsStub struct {
	items []productport.ProductOption
	query productport.ProductOptionQuery
}

func (s *audienceProductOptionsStub) ListProductOptions(_ context.Context, query productport.ProductOptionQuery) (productport.ProductOptionPage, error) {
	s.query = query
	return productport.ProductOptionPage{Items: s.items, Total: int64(len(s.items)), Limit: query.Limit, Offset: query.Offset}, nil
}

func TestAudienceProductReferenceUsesExactCodeOrUniqueTitle(t *testing.T) {
	options := &audienceProductOptionsStub{items: []productport.ProductOption{
		{ID: 1, Code: "course-v3", Name: "中文商品"},
		{ID: 2, Code: "course-v4", Name: "另一商品"},
	}}
	resolver := audienceProductReferenceAdapter{products: options}
	for _, fixture := range []struct {
		value    string
		wantCode string
		wantOK   bool
	}{
		{"中文商品", "course-v3", true},
		{"course-v4", "course-v4", true},
		{"不存在商品", "", false},
	} {
		code, ok, err := resolver.ResolveAudienceProduct(context.Background(), fixture.value)
		if err != nil || code != fixture.wantCode || ok != fixture.wantOK {
			t.Fatalf("%q = (%q, %t, %v), want (%q, %t, nil)", fixture.value, code, ok, err, fixture.wantCode, fixture.wantOK)
		}
	}
	if options.query.Q != "不存在商品" || options.query.ProductType != productport.ProductOptionAll || options.query.Limit != productport.ProductOptionMaximumLimit {
		t.Fatalf("unexpected Product Port query: %#v", options.query)
	}
}

func TestAudienceProductReferenceRejectsAmbiguousExactTitle(t *testing.T) {
	resolver := audienceProductReferenceAdapter{products: &audienceProductOptionsStub{items: []productport.ProductOption{
		{ID: 1, Code: "course-a", Name: "同名商品"},
		{ID: 2, Code: "course-b", Name: "同名商品"},
	}}}
	code, ok, err := resolver.ResolveAudienceProduct(context.Background(), "同名商品")
	if err != nil || ok || code != "" {
		t.Fatalf("ambiguous title = (%q, %t, %v), want empty false nil", code, ok, err)
	}
}
