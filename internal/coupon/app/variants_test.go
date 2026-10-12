package app

import (
	"context"
	"testing"

	couponport "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/port"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

type variantTarget struct {
	high    int64
	enabled bool
}

func (v variantTarget) ReadProductTarget(context.Context, productport.ProductOptionType, productport.ID) (productport.ProductOption, error) {
	return productport.ProductOption{ID: 5, ProductType: productport.ProductOptionStandard, Currency: "CNY", PriceMinor: 9900, VariantsEnabled: v.enabled, MaxPriceMinor: v.high}, nil
}
func TestVariantCouponAcceptsAnyEligiblePrice(t *testing.T) {
	for _, tc := range []struct {
		high        int64
		enabled, ok bool
	}{{99900, true, true}, {10000, true, false}, {0, true, false}, {99900, false, false}} {
		s := &Service{products: variantTarget{tc.high, tc.enabled}}
		err := s.validateProducts(context.Background(), couponport.Coupon{DiscountAmountTotal: 10000, TargetRefs: []string{"standard_product:5"}})
		if (err == nil) != tc.ok {
			t.Fatalf("%+v: %v", tc, err)
		}
	}
}
