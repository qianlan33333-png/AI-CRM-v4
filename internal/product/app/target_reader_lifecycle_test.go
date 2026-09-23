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
