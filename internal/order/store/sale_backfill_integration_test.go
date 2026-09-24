package store

import (
	"context"
	"errors"
	"testing"
	"time"

	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func TestPostgreSQLPaidSaleBackfillReconstructsPaidAndConfirmedRefunds(t *testing.T) {
	native, cleanup := orderIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	wrapper, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapper.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	paidAt := time.Date(2026, 9, 23, 8, 12, 55, 0, time.UTC)
	var id, paidID int64
	if err := native.QueryRow(ctx, `INSERT INTO orders(provider,source_system,source_key,merchant_order_no,payer_customer_id,beneficiary_customer_id,amount_minor,refunded_minor,currency,status,record_origin,effect_eligible,version,created_at,updated_at)
VALUES('wechat_pay','test','sale-backfill-943','M-sale-backfill-943',12484,12484,990,990,'CNY','refunded','native',true,4,$1,$2) RETURNING id`, paidAt.Add(-time.Hour), paidAt.Add(2*time.Hour)).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := native.Exec(ctx, `INSERT INTO order_items(order_id,line_no,product_id,product_version,product_code,product_name,unit_amount_minor,quantity,line_amount_minor)
VALUES($1,1,51,1,'p-51','商品',990,1,990)`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := native.Exec(ctx, `INSERT INTO order_checkout_snapshots(order_id,product_type,product_id,product_code,product_name,product_version,service_period_duration_days,gross_amount_minor,discount_amount_minor,payable_amount_minor,currency,coupon_applied,coupon_reservation_ref,reserved_at,created_at)
VALUES($1,'standard_product',51,'p-51','商品',1,0,990,0,990,'CNY',false,'',$2,$2)`, id, paidAt.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	source := orderport.NewPaidEventSourceDigest(id, 2)
	if err := native.QueryRow(ctx, `INSERT INTO order_paid_events(order_id,order_version,source_digest,occurred_at) VALUES($1,2,$2,$3) RETURNING id`, id, source[:], paidAt).Scan(&paidID); err != nil {
		t.Fatal(err)
	}
	if _, err := native.Exec(ctx, `INSERT INTO order_outbox(event_type,idempotency_key,aggregate_id,payload,occurred_at)
VALUES('order.paid.v1',$1,$2,'{}'::jsonb,$3)`, "order.paid.v1:"+itoa(int(paidID)), id, paidAt); err != nil {
		t.Fatal(err)
	}
	for _, entry := range []struct {
		from, to, actor   string
		refunded, version int64
		at                time.Time
	}{
		{"pending_payment", "paid", "payment:paid-943", 0, 2, paidAt},
		{"paid", "partially_refunded", "payment:refund-943-1", 300, 3, paidAt.Add(time.Hour)},
		{"partially_refunded", "refunded", "payment:refund-943-2", 990, 4, paidAt.Add(2 * time.Hour)},
	} {
		if _, err := native.Exec(ctx, `INSERT INTO order_status_history(order_id,from_status,to_status,refunded_minor,order_version,actor_scope,occurred_at)
VALUES($1,$2,$3,$4,$5,$6,$7)`, id, entry.from, entry.to, entry.refunded, entry.version, entry.actor, entry.at); err != nil {
			t.Fatal(err)
		}
	}
	var missingEventID int64
	if err := native.QueryRow(ctx, `INSERT INTO orders(provider,source_system,source_key,merchant_order_no,payer_customer_id,beneficiary_customer_id,amount_minor,refunded_minor,currency,status,record_origin,effect_eligible,version,created_at,updated_at)
VALUES('wechat_pay','test','sale-backfill-missing-event','M-sale-backfill-missing-event',12485,12485,990,0,'CNY','paid','native',true,2,$1,$2) RETURNING id`, paidAt.Add(-time.Hour), paidAt).Scan(&missingEventID); err != nil {
		t.Fatal(err)
	}
	if _, err := native.Exec(ctx, `INSERT INTO order_items(order_id,line_no,product_id,product_version,product_code,product_name,unit_amount_minor,quantity,line_amount_minor)
VALUES($1,1,51,1,'p-51','商品',990,1,990)`, missingEventID); err != nil {
		t.Fatal(err)
	}
	if _, err := native.Exec(ctx, `INSERT INTO order_status_history(order_id,from_status,to_status,refunded_minor,order_version,actor_scope,occurred_at)
VALUES($1,'pending_payment','paid',0,2,'payment:missing-event',$2)`, missingEventID, paidAt); err != nil {
		t.Fatal(err)
	}
	if err := uow.Within(ctx, func(tx context.Context) error {
		ids, readErr := repository.ListPaidSaleOrderIDsWithin(tx, 0, 500)
		if readErr != nil {
			return readErr
		}
		if len(ids) != 2 || ids[0] != id || ids[1] != missingEventID {
			t.Fatalf("paid IDs=%v", ids)
		}
		if _, readErr := repository.ReadPaidSaleBackfillFactWithin(tx, missingEventID); !errors.Is(readErr, orderport.ErrNotFound) {
			t.Fatalf("missing durable paid event err=%v", readErr)
		}
		fact, readErr := repository.ReadPaidSaleBackfillFactWithin(tx, id)
		if readErr != nil {
			return readErr
		}
		if !fact.Paid.Valid() || fact.Paid.Order.Status != "paid" || fact.Paid.Order.Version != 2 || len(fact.Refunds) != 2 || fact.Refunds[0].RefundedDelta != 300 || fact.Refunds[1].RefundedDelta != 690 || fact.Refunds[1].ReceiptKey != "refund-943-2" {
			t.Fatalf("reconstructed sale fact=%+v", fact)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
