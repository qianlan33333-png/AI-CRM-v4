package main

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	paymentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	paymentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/store"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// A fixed trusted paid fact enters the actual composed Owner UoW/fanout.
// No callback verifier, reconciler, Provider binding, or effect worker runs.
func TestPostgreSQLReferralSol61NativePaidFanoutWithoutWeChatCapability(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()
	dataKey := base64.RawStdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	cfg := platformconfig.Runtime{Role: platformconfig.RoleAPI, DatabaseURL: databaseURL, PublicOrigin: "https://sol61-referral.example.test", ReleaseSHA: "sol61-nominal-referral-fanout", WorkerOwner: "sol61-nominal-referral-fanout", WorkerLimit: 1, Referral: platformconfig.Referral{TokenDataKey: dataKey}, GroupOps: platformconfig.GroupOps{WebhookSecret: "sol61-nominal-referral-webhook"}, Survey: platformconfig.Survey{DataKey: dataKey, IdentityPhoneDataKey: dataKey}}
	application, err := compose(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer application.Close()
	pool := application.pool.Native()
	at := time.Now().UTC().Truncate(time.Second)
	customer := refundWorkerCustomer(t, ctx, pool)
	var orderID, paymentID, productID int64
	const merchant = "sol61-nominal-native-alipay"
	// The retired stock column remains required by the legacy table schema.
	// A zero fixture value enables no inventory behavior.
	if err = pool.QueryRow(ctx, `INSERT INTO products(product_code,name,description,price_minor,currency,stock_quantity,created_by,created_at,updated_at,legacy_admin_projection) VALUES('sol61-nominal-product','Synthetic nominal product','',1000,'CNY',0,1,$1,$1,'{}'::jsonb) RETURNING id`, at.Add(-time.Minute)).Scan(&productID); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `INSERT INTO orders(provider,source_system,source_key,merchant_order_no,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,record_origin,effect_eligible,version,created_at,updated_at) VALUES('alipay','sol61-nominal',$1,$1,$2,$2,1000,'CNY','pending_payment','native',true,1,$3,$3) RETURNING id`, merchant, customer, at.Add(-time.Minute)).Scan(&orderID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO order_items(order_id,line_no,product_id,product_version,product_code,product_name,unit_amount_minor,quantity,line_amount_minor) VALUES($1,1,$2,1,'sol61-nominal-product','Synthetic nominal product',1000,1,1000)`, orderID, productID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO order_checkout_snapshots(order_id,product_type,product_id,product_code,product_name,product_version,service_period_duration_days,gross_amount_minor,discount_amount_minor,payable_amount_minor,currency,coupon_applied,coupon_reservation_ref,reserved_at,created_at) VALUES($1,'standard_product',$3,'sol61-nominal-product','Synthetic nominal product',1,0,1000,0,1000,'CNY',false,'',$2,$2)`, orderID, at.Add(-time.Minute), productID); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `INSERT INTO payments(order_id,provider,payment_channel,merchant_order_no,payer_identity_id,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,profit_sharing_marked,version,created_at,updated_at) VALUES($1,'alipay','alipay_wap',$2,1,$3,$3,1000,'CNY','awaiting_prepay',false,1,$4,$4) RETURNING id`, orderID, merchant, customer, at.Add(-time.Minute)).Scan(&paymentID); err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(application.pool)
	if err != nil {
		t.Fatal(err)
	}
	const trade = "sol61-nominal-paid-trade"
	command := orderport.PaymentSettlementCommand{OrderID: orderID, ProviderTransactionNo: trade, OccurredAt: at, ReceiptKey: "sol61-nominal-trusted-payment"}
	err = uow.Within(ctx, func(tx context.Context) error {
		payments := paymentstore.NewPostgreSQL()
		payment, e := payments.GetPayment(tx, paymentID, true)
		if e != nil {
			return e
		}
		payment, e = payment.SettleVerifiedAlipayPayment(payment.Version, at)
		if e != nil {
			return e
		}
		payment.ProviderTransactionReference = trade
		if _, e = payments.UpdatePaymentSettlement(tx, payment, string(effectport.Hash("alipay.transaction", trade)), command.ReceiptKey); e != nil {
			return e
		}
		_, e = application.nativeOrders.SettlePaymentWithin(tx, command)
		return e
	})
	if err != nil {
		t.Fatalf("nominal native payment blocked by optional Referral fanout: %v", err)
	}
	if err = uow.Within(ctx, func(tx context.Context) error {
		_, e := application.nativeOrders.SettlePaymentWithin(tx, command)
		return e
	}); !errors.Is(err, orderport.ErrConflict) {
		t.Fatalf("duplicate native transition should preserve original facts: %v", err)
	}
	paid, err := application.paymentReconciliation.GetPayment(ctx, paymentID)
	if err != nil || paid.Status != paymentdomain.StatusPaid {
		t.Fatalf("atomic native payment status=%s err=%v", paid.Status, err)
	}
	var paidEvents, saleEvents int
	var status string
	if err = pool.QueryRow(ctx, `SELECT status,(SELECT count(*) FROM order_paid_events WHERE order_id=$1),(SELECT count(*) FROM referral_product_sale_events WHERE order_id=$1) FROM orders WHERE id=$1`, orderID).Scan(&status, &paidEvents, &saleEvents); err != nil || status != "paid" || paidEvents != 1 || saleEvents != 0 {
		t.Fatalf("nominal replay status=%s paid_events=%d attributed_sales=%d err=%v", status, paidEvents, saleEvents, err)
	}
}
