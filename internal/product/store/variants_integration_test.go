package store_test

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	productapp "github.com/qianlan33333-png/AI-CRM-v3/internal/product/app"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

func TestProductVariantsPostgreSQLAtomicCompatibilityAndVersion(t *testing.T) {
	ctx, pool, service, _, lifecycle := imageMutationServices(t)
	enabled := true
	variants := []productport.Variant{{Name: "单人版", PriceMinor: 9900}, {Name: "三人成行版", Description: "统一由购买人联系", PriceMinor: 99900, Attributes: []productport.VariantAttribute{{Key: "版本", Value: "三人成行"}}}}
	create := productport.CreateCommand{Images: []string{}, ProductCode: "variant-course", Name: "课程", Currency: "CNY", LegacyAdminProjection: productapp.DefaultLegacyAdminProjection(), PriceMinor: 5000, Actor: 1, IdempotencyKey: "variant-create-000001", VariantsEnabled: &enabled, Variants: &variants}
	p, err := service.Create(ctx, create)
	if err != nil {
		t.Fatal(err)
	}
	if !p.VariantsEnabled || len(p.Variants) != 2 || p.Variants[0].ID < 1 || p.Variants[0].Version != 1 {
		t.Fatalf("created=%+v", p)
	}
	old := p.Variants
	replay, err := service.Create(ctx, create)
	if err != nil || !reflect.DeepEqual(replay.Variants, old) {
		t.Fatalf("replay=%+v err=%v", replay, err)
	}
	update := productport.UpdateCommand{ID: p.ID, ExpectedVersion: p.Version, Name: p.Name, Currency: p.Currency, PriceMinor: p.PriceMinor, Actor: 1, IdempotencyKey: "variant-old-client-001"}
	p, err = service.Update(ctx, update)
	if err != nil || !reflect.DeepEqual(p.Variants, old) || !p.VariantsEnabled {
		t.Fatalf("legacy preserve=%+v err=%v", p, err)
	}
	next := append([]productport.Variant{}, p.Variants...)
	next[1].PriceMinor = 88800
	update.ExpectedVersion = p.Version
	update.IdempotencyKey = "variant-second-change-01"
	update.Variants = &next
	p, err = service.Update(ctx, update)
	if err != nil || p.Variants[0].Version != 1 || p.Variants[1].Version != 2 {
		t.Fatalf("chosen version isolation=%+v err=%v", p.Variants, err)
	}
	copied, err := lifecycle.CopyLocalProduct(ctx, productport.CopyLocalProductCommand{ID: p.ID, ExpectedVersion: p.Version, Actor: 1, IdempotencyKey: "variant-copy-00000001"})
	if err != nil {
		t.Fatal(err)
	}
	copy, err := service.Get(ctx, copied.ID)
	if err != nil || !copy.VariantsEnabled || len(copy.Variants) != 2 || copy.Variants[0].ID == p.Variants[0].ID || copy.Variants[0].Version != 1 || copied.Enabled {
		t.Fatalf("copy=%+v err=%v", copy, err)
	}
	// The first inserted row and parent update must roll back when a later ID
	// belongs to a different product. Receipt, events and versions roll back too.
	invalid := []productport.Variant{{Name: "不应留下", PriceMinor: 100}, copy.Variants[0]}
	update.ExpectedVersion = p.Version
	update.IdempotencyKey = "variant-cross-product-01"
	update.Variants = &invalid
	if _, err = service.Update(ctx, update); !errors.Is(err, productapp.ErrConflict) {
		t.Fatalf("foreign id=%v", err)
	}
	after, err := service.Get(ctx, p.ID)
	if err != nil || after.Version != p.Version || !reflect.DeepEqual(after.Variants, p.Variants) {
		t.Fatal("partial catalog saved", after, err)
	}
	var leaks int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM product_variants WHERE name='不应留下'`).Scan(&leaks); err != nil || leaks != 0 {
		t.Fatal("insert leaked", leaks, err)
	}
	// Two full-list editors using the same parent version: exactly one succeeds.
	updates := []productport.UpdateCommand{update, update}
	for i := range updates {
		updates[i].Variants = nil
		updates[i].IdempotencyKey = []string{"variant-concurrent-01", "variant-concurrent-02"}[i]
	}
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := range updates {
		wg.Add(1)
		go func(i int) { defer wg.Done(); _, errs[i] = service.Update(ctx, updates[i]) }(i)
	}
	wg.Wait()
	if (errs[0] == nil) == (errs[1] == nil) {
		t.Fatalf("CAS results=%v", errs)
	}
	p, err = service.Get(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	empty := []productport.Variant{}
	update.ExpectedVersion = p.Version
	update.IdempotencyKey = "variant-remove-all-001"
	update.Variants = &empty
	p, err = service.Update(ctx, update)
	if err != nil || len(p.Variants) != 0 || !p.VariantsEnabled {
		t.Fatal("remove", p, err)
	}
	var removed int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM product_variants WHERE product_id=$1 AND removed_at IS NOT NULL`, p.ID).Scan(&removed); err != nil || removed != 2 {
		t.Fatal("history lost", removed, err)
	}
	update.ExpectedVersion = p.Version
	update.IdempotencyKey = "variant-disable-00001"
	enabled = false
	update.VariantsEnabled = &enabled
	update.Variants = nil
	update.PriceMinor = 7777
	p, err = service.Update(ctx, update)
	if err != nil || p.VariantsEnabled || p.PriceMinor != 7777 {
		t.Fatal("disable", p, err)
	}
}
