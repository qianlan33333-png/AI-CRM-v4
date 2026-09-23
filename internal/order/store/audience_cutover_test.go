package store

import (
	"context"
	"testing"
	"time"

	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// Cutover imports payer attribution independently of fulfillment. A missing
// beneficiary must not hide paid buyers, and a beneficiary must never rescue an
// unknown payer. Both authorized commerce sources share this Owner contract.
func TestPostgreSQLPaidAudienceCutoverPayerOnlyAcrossProviders(t *testing.T) {
	pool, cleanup := orderIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	wrapper, err := platformpostgres.Wrap(pool, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := NewPostgreSQL(pool, uow)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 8, 12, 11, 30, 0, 0, time.UTC)
	for _, row := range []struct {
		key, provider      string
		payer, beneficiary int64
	}{
		{"pay-payer-only", "wechat_pay", 101, 0},
		{"shop-payer-only", "wechat_shop", 202, 0},
		{"unknown-payer", "wechat_shop", 0, 303},
	} {
		var id int64
		err = pool.QueryRow(ctx, `INSERT INTO orders(provider,source_system,source_key,merchant_order_no,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,record_origin,effect_eligible,source_row_digest,version,created_at,updated_at) VALUES($1,'commerce-history',$2,$2,NULLIF($3,0),NULLIF($4,0),100,'CNY','paid','history',false,$5,2,$6,$6) RETURNING id`, row.provider, row.key, row.payer, row.beneficiary, make([]byte, 32), at).Scan(&id)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, `INSERT INTO order_items(order_id,line_no,product_code,product_name,unit_amount_minor,quantity,line_amount_minor) VALUES($1,1,'202608121337','Course',100,1,100)`, id); err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, `INSERT INTO order_status_history(order_id,from_status,to_status,refunded_minor,order_version,actor_scope,occurred_at) VALUES($1,'pending_payment','paid',0,2,'history:verified',$2)`, id, at); err != nil {
			t.Fatal(err)
		}
	}
	var facts []orderport.PaidAudienceOrder
	err = uow.Within(ctx, func(tx context.Context) error {
		var e error
		facts, e = repository.PaidAudienceOrders(tx, at.Add(time.Hour))
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 2 {
		t.Fatalf("expected two attributed payers, got %d", len(facts))
	}
	for i, want := range []int64{101, 202} {
		if int64(facts[i].CustomerID) != want || facts[i].ProductCode != "202608121337" || facts[i].PaidAt == nil || !facts[i].PaidAt.Equal(at) {
			t.Fatal("payer/payment evidence mismatch")
		}
	}
}
