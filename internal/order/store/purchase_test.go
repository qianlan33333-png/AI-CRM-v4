package store

import (
	"context"
	"fmt"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"sync"
	"testing"
	"time"
)

func TestStandardPurchaseHistoricalStatesAndConcurrentReservation(t *testing.T) {
	pool, cleanup := orderIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	wrapped, err := platformpostgres.Wrap(pool, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := NewPostgreSQL(pool, uow)
	if err != nil {
		t.Fatal(err)
	}
	insert := func(ctx context.Context, code, status string, payer int64) (int64, error) {
		tx, err := transaction(ctx)
		if err != nil {
			return 0, err
		}
		var id int64
		err = tx.QueryRow(ctx, `INSERT INTO orders(provider,source_system,source_key,merchant_order_no,payer_customer_id,amount_minor,refunded_minor,currency,status,record_origin,effect_eligible,source_row_digest,version,created_at,updated_at) VALUES('wechat_pay','test',$1,$1,$2,100,$3,'CNY',$4,'history',false,$5,1,now(),now()) RETURNING id`, fmt.Sprintf("%s-%d", code, time.Now().UnixNano()), payer, func() int64 {
			if status == "refunded" {
				return 100
			}
			if status == "partially_refunded" {
				return 10
			}
			return 0
		}(), status, make([]byte, 32)).Scan(&id)
		if err != nil {
			return 0, err
		}
		_, err = tx.Exec(ctx, `INSERT INTO order_items(order_id,line_no,product_code,product_name,unit_amount_minor,quantity,line_amount_minor) VALUES($1,1,$2,'Product',100,1,100)`, id, code)
		return id, err
	}
	for _, status := range []string{"paid", "partially_refunded", "refunded", "pending_payment", "payment_failed", "closed", "cancelled"} {
		var id int64
		if err = uow.Within(ctx, func(tx context.Context) error { var e error; id, e = insert(tx, status, status, 11); return e }); err != nil {
			t.Fatal(err)
		}
		var got orderport.StandardPurchaseState
		q := orderport.StandardPurchaseQuery{CustomerIDs: []int64{22, 11}, ProductID: 100, ProductCode: status}
		err = uow.Within(ctx, func(tx context.Context) error { var e error; got, e = repo.ReadStandardPurchaseWithin(tx, q); return e })
		if err != nil {
			t.Fatal(err)
		}
		owned := status == "paid" || status == "partially_refunded"
		if got.Owned != owned || got.Pending != (status == "pending_payment") || (owned && got.PaidOrderID != id) || (!owned && got.PaidOrderID != 0) {
			t.Fatalf("%s: %+v", status, got)
		}
		q.CustomerIDs = []int64{33}
		err = uow.Within(ctx, func(tx context.Context) error { var e error; got, e = repo.ReadStandardPurchaseWithin(tx, q); return e })
		if err != nil || got.Owned || got.Pending {
			t.Fatalf("other customer: %+v %v", got, err)
		}
		if status == "pending_payment" {
			q.CustomerIDs = []int64{11}
			q.ExcludedPendingOrderIDs = []int64{id}
			err = uow.Within(ctx, func(tx context.Context) error { var e error; got, e = repo.ReadStandardPurchaseWithin(tx, q); return e })
			if err != nil || got.Pending {
				t.Fatalf("reviewed exception: %+v %v", got, err)
			}
		}
	}
	// Two independent idempotency scopes cannot both reserve one standard course.
	var wg sync.WaitGroup
	results := make(chan bool, 2)
	errs := make(chan error, 2)
	for n := 0; n < 2; n++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			created := false
			e := uow.Within(ctx, func(tx context.Context) error {
				state, e := repo.ReadStandardPurchaseWithin(tx, orderport.StandardPurchaseQuery{CustomerIDs: []int64{22, 11}, ProductID: 999, ProductCode: "concurrent", Lock: true})
				if e != nil {
					return e
				}
				if state.Owned || state.Pending {
					return nil
				}
				_, e = insert(tx, "concurrent", "pending_payment", 11)
				created = e == nil
				return e
			})
			results <- created
			errs <- e
		}(n)
	}
	wg.Wait()
	close(results)
	close(errs)
	count := 0
	for created := range results {
		if created {
			count++
		}
	}
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	if count != 1 {
		t.Fatalf("created=%d", count)
	}
}
