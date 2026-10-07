package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	couponapp "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/app"
	couponport "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

func TestPostgreSQLCouponUnicodeDefinitionCreateCopyAndImport(t *testing.T) {
	native, cleanup := couponIntegrationPool(t)
	defer cleanup()
	wrapped, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	service := couponapp.NewService(uow, repository, productFacts{9: {ID: 9, ProductType: productport.ProductOptionStandard, Currency: "CNY", PriceMinor: 1000}}, repository)
	ctx := context.Background()
	now := time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC)
	days := int32(7)
	input := couponport.UpsertCommand{Coupon: couponport.Coupon{Name: strings.Repeat("券", 45), Instructions: strings.Repeat("说", 200), DiscountAmountTotal: 100, TotalIssueLimit: 10, PerUserIssueLimit: 1, ClaimStartsAt: now, ClaimEndsAt: now.Add(time.Hour), ValidityMode: couponport.ValidityRelativeDays, RelativeValidityDays: &days, TargetRefs: []string{"standard_product:9"}}, Actor: 7, IdempotencyKey: "coupon-unicode-pg-create-0001"}
	created, err := service.Create(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	read, err := service.Get(ctx, created.ID)
	if err != nil || read.Name != input.Name || read.Instructions != input.Instructions {
		t.Fatalf("read=%+v err=%v", read, err)
	}
	copyKey := "coupon-unicode-pg-copy-0001"
	copied, err := service.Copy(ctx, created.ID, 7, copyKey)
	if err != nil || copied.Name != strings.Repeat("券", 42)+" 副本" || copied.Instructions != input.Instructions {
		t.Fatalf("copy=%+v err=%v", copied, err)
	}
	replayed, err := service.Copy(ctx, created.ID, 7, copyKey)
	if err != nil || replayed.ID != copied.ID || replayed.Name != copied.Name {
		t.Fatalf("copy replay=%+v err=%v", replayed, err)
	}
	var imported couponport.Coupon
	definition := couponport.DefinitionImport{Coupon: input.Coupon, Actor: 7, CreatedAt: now, UpdatedAt: now}
	definition.Status, definition.Currency = "draft", "CNY"
	err = uow.Within(ctx, func(tx context.Context) error {
		var err error
		imported, err = repository.ImportDefinition(tx, definition)
		return err
	})
	if err != nil || imported.Name != input.Name || imported.Instructions != input.Instructions {
		t.Fatalf("import=%+v err=%v", imported, err)
	}
	for _, field := range []string{"name", "instructions"} {
		invalid := definition
		if field == "name" {
			invalid.Name += "券"
		} else {
			invalid.Instructions += "说"
		}
		err := uow.Within(ctx, func(tx context.Context) error {
			_, err := repository.ImportDefinition(tx, invalid)
			return err
		})
		if !errors.Is(err, couponapp.ErrInvalidCoupon) {
			t.Fatalf("%s overflow err=%v", field, err)
		}
	}
	var rules, receipts, audits, outbox int
	err = native.QueryRow(ctx, `SELECT (SELECT count(*) FROM coupon_rules),(SELECT count(*) FROM coupon_operation_receipts),(SELECT count(*) FROM coupon_audit_events),(SELECT count(*) FROM coupon_outbox)`).Scan(&rules, &receipts, &audits, &outbox)
	if err != nil || rules != 3 || receipts != 2 || audits != 2 || outbox != 2 {
		t.Fatalf("definition/replay counts=%d/%d/%d/%d err=%v", rules, receipts, audits, outbox, err)
	}
}
