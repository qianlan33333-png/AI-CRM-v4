package store

import (
	"context"
	"testing"
	"time"

	couponapp "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/app"
	couponport "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

func TestPostgreSQLCouponRuleMutationKeepsActorReceiptAndEventNamespacesAligned(t *testing.T) {
	for _, operation := range []string{"copy", "archive", "delete"} {
		t.Run(operation, func(t *testing.T) {
			native, cleanup := couponIntegrationPool(t)
			defer cleanup()
			ctx := context.Background()
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
			now := time.Now().UTC().Truncate(time.Microsecond)
			days := int32(7)
			input := couponport.UpsertCommand{Coupon: couponport.Coupon{Name: "Actor namespace", DiscountAmountTotal: 100, TotalIssueLimit: 10, PerUserIssueLimit: 1, ClaimStartsAt: now.Add(-time.Hour), ClaimEndsAt: now.Add(time.Hour), ValidityMode: couponport.ValidityRelativeDays, RelativeValidityDays: &days, TargetRefs: []string{"standard_product:9"}}, Actor: 7, IdempotencyKey: "coupon-actor-namespace-create-one"}
			first, err := service.Create(ctx, input)
			if err != nil {
				t.Fatal(err)
			}
			input.Actor, input.IdempotencyKey = 8, "coupon-actor-namespace-create-two"
			second, err := service.Create(ctx, input)
			if err != nil {
				t.Fatal(err)
			}
			key := "coupon-shared-browser-key-0001"
			apply := func(c couponport.Coupon, actor int64) (couponport.Coupon, error) {
				switch operation {
				case "copy":
					return service.Copy(ctx, c.ID, actor, key)
				case "archive":
					return service.Archive(ctx, c.ID, c.Version, actor, key)
				default:
					return service.Delete(ctx, c.ID, actor, key)
				}
			}
			one, err := apply(first, 7)
			if err != nil {
				t.Fatalf("first actor: %v", err)
			}
			two, err := apply(second, 8)
			if err != nil {
				t.Fatalf("second independent actor collided with the first event: %v", err)
			}
			if one.ID == two.ID {
				t.Fatalf("actors reused a result: first=%+v second=%+v", one, two)
			}
			for _, actor := range []int64{7, 8} {
				original := first
				want := one
				if actor == 8 {
					original, want = second, two
				}
				replayed, replayErr := apply(original, actor)
				if replayErr != nil || replayed.ID != want.ID || replayed.Version != want.Version || replayed.Status != want.Status {
					t.Fatalf("actor %d replay=%+v err=%v want=%+v", actor, replayed, replayErr, want)
				}
			}
			var receipts, audit, outbox int64
			event := map[string]string{"copy": couponport.EventCouponCopied, "archive": couponport.EventCouponArchived, "delete": couponport.EventCouponDeleted}[operation]
			if err = native.QueryRow(ctx, `SELECT (SELECT count(*) FROM coupon_operation_receipts WHERE operation=$1),(SELECT count(*) FROM coupon_audit_events WHERE event_type=$2),(SELECT count(*) FROM coupon_outbox WHERE event_type=$2)`, operation, event).Scan(&receipts, &audit, &outbox); err != nil || receipts != 2 || audit != 2 || outbox != 2 {
				t.Fatalf("operation=%s receipts=%d audit=%d outbox=%d err=%v", operation, receipts, audit, outbox, err)
			}
		})
	}
}
