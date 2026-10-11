package app

import (
	"context"
	"encoding/json"
	"testing"

	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

func TestTargetReaderProjectsPersistedLifecycleForShareAndCheckout(t *testing.T) {
	for _, tc := range []struct {
		name, status     string
		enabled, allowed bool
		explicit         productport.LocalProductLifecycle
	}{
		{name: "persisted active", status: "active", enabled: true, allowed: true},
		{name: "persisted legacy enabled", status: "enabled", enabled: true, allowed: true},
		{name: "draft", status: "draft"},
		{name: "disabled", status: "disabled"},
		{name: "archived", status: "archived"},
		{name: "inconsistent explicit state", status: "active", enabled: true, explicit: productport.LocalProductDraft},
	} {
		t.Run(tc.name, func(t *testing.T) {
			product := validTestProduct(1)
			raw, _ := json.Marshal(map[string]any{"schema_version": 1, "status": tc.status, "enabled": tc.enabled})
			projection, err := CanonicalLegacyAdminProjection(raw)
			if err != nil {
				t.Fatal(err)
			}
			product.LegacyAdminProjection = projection
			product.LocalLifecycle = tc.explicit
			store := &productTestStore{products: []productport.Product{product}}
			reader := &TargetReader{ordinary: NewService(&productTestUoW{}, store, &productTestEvents{}), period: &ServicePeriodService{}}
			shared, shareErr := reader.ReadSidebarShareProduct(context.Background(), productport.ProductOptionStandard, 1)
			checkout, checkoutErr := reader.ReadCheckoutProductWithin(context.Background(), productport.ProductOptionStandard, 1)
			if tc.allowed {
				if shareErr != nil || checkoutErr != nil || shared.ID != 1 || checkout.ID != 1 {
					t.Fatalf("share=%+v err=%v checkout=%+v err=%v", shared, shareErr, checkout, checkoutErr)
				}
			} else if shareErr == nil || checkoutErr == nil {
				t.Fatalf("unavailable product allowed: share=%v checkout=%v", shareErr, checkoutErr)
			}
			if tc.status == "archived" {
				if _, err := reader.ReadProductTarget(context.Background(), productport.ProductOptionStandard, product.ID); err == nil {
					t.Fatal("archived product remained available to a new Product target selection")
				}
			}
		})
	}
}

func TestShopProductCannotCheckoutOrEnableDistribution(t *testing.T) {
	ctx := context.Background()
	product := validTestProduct(1)
	product.SourceKind = productport.SourceWeChatShop
	product.Images = []string{"https://example.test/existing-shop-media.jpg"}
	store := &productTestStore{products: []productport.Product{product}}
	svc := NewService(&productTestUoW{}, store, &productTestEvents{})
	reader := &TargetReader{ordinary: svc, period: &ServicePeriodService{}}
	if _, e := reader.ReadCheckoutProductWithin(ctx, productport.ProductOptionStandard, 1); e == nil {
		t.Fatal("shop product locally sold")
	}
	// The same ordinary edit service owns the distribution policy guard.
	command := productport.UpdateCommand{ID: 1, ExpectedVersion: product.Version, Name: product.Name, Currency: product.Currency, PriceMinor: product.PriceMinor, Actor: 1, IdempotencyKey: "shop-policy-0000001", DistributionPolicy: &productport.DistributionPolicy{Enabled: true, CommissionRateBasisPoints: 1000, WaitDays: 7}}

	if _, e := svc.Update(ctx, command); e == nil {
		t.Fatal("shop distribution enabled")
	}
	command.DistributionPolicy = nil
	command.IdempotencyKey = "shop-basic-00000001"
	command.Name = "更改基础名称"
	command.Images = []string{"https://example.test/not-shop-media.jpg"}
	command.LegacyAdminProjection = json.RawMessage(`{"schema_version":1,"status":"enabled","enabled":true,"buy_button_text":"不应保存的售卖设置"}`)
	updated, e := svc.Update(ctx, command)
	if e != nil || updated.Name != command.Name || len(updated.Images) != 1 || updated.Images[0] != product.Images[0] || !jsonEquivalent(updated.LegacyAdminProjection, product.LegacyAdminProjection) {
		t.Fatalf("Shop basic edit changed a non-basic capability: err=%v name=%q images=%v before=%s after=%s", e, updated.Name, updated.Images, product.LegacyAdminProjection, updated.LegacyAdminProjection)
	}
}
