package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func TestPostgreSQLHistoricalQualificationEvidenceRejectsDriftAndPreservesExactReplay(t *testing.T) {
	native, cleanup := orderIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()

	wrapper, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}

	confirmedAt := time.Date(2026, 9, 14, 9, 30, 0, 0, time.UTC)
	orderDigest := sha256.Sum256([]byte("historical-qualification-order"))
	var orderID int64
	if err = native.QueryRow(ctx, `INSERT INTO orders(
	provider,source_system,source_key,merchant_order_no,payer_customer_id,beneficiary_customer_id,
	amount_minor,refunded_minor,currency,status,record_origin,effect_eligible,source_row_digest,
	version,created_at,updated_at
) VALUES('wechat_pay','legacy-qualified','qualification-order-1','M-qualification-order-1',101,101,
	990,0,'CNY','paid','history',false,$1,1,$2,$2) RETURNING id`, orderDigest[:], confirmedAt).Scan(&orderID); err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, `INSERT INTO order_items(
	order_id,line_no,product_id,product_code,product_name,unit_amount_minor,quantity,line_amount_minor
) VALUES($1,1,77,'course-77','可信历史购买',990,1,990)`, orderID); err != nil {
		t.Fatal(err)
	}

	sourceDigest := sha256.Sum256([]byte("historical-qualification-evidence"))
	evidence := orderport.HistoricalQualificationEvidence{
		OrderID:               orderID,
		OrderItemLine:         1,
		ProductID:             77,
		ProductType:           "standard_product",
		SourceProductCode:     "course-77",
		PayerCustomerID:       101,
		BeneficiaryCustomerID: 101,
		ItemPaidMinor:         990,
		PaymentConfirmedAt:    confirmedAt,
		SourceOrderDigest:     orderDigest,
		SourceDigest:          sourceDigest,
	}
	importEvidence := func(value orderport.HistoricalQualificationEvidence) error {
		return uow.Within(ctx, func(tx context.Context) error {
			return repository.ImportHistoricalQualificationEvidenceWithin(tx, value)
		})
	}
	if err = importEvidence(evidence); err != nil {
		t.Fatalf("initial import: %v", err)
	}
	if err = importEvidence(evidence); err != nil {
		t.Fatalf("exact replay: %v", err)
	}
	var refundEvidence []orderport.HistoricalQualificationRefundEvidence
	if err = uow.Within(ctx, func(tx context.Context) error {
		refundEvidence, err = repository.ListHistoricalQualificationEvidenceByOrderWithin(tx, orderID)
		return err
	}); err != nil || len(refundEvidence) != 1 || refundEvidence[0].OrderID != evidence.OrderID ||
		refundEvidence[0].OrderItemLine != evidence.OrderItemLine || refundEvidence[0].ProductID != evidence.ProductID ||
		refundEvidence[0].ProductType != evidence.ProductType || refundEvidence[0].PayerCustomerID != evidence.PayerCustomerID ||
		refundEvidence[0].BeneficiaryCustomerID != evidence.BeneficiaryCustomerID ||
		refundEvidence[0].ItemPaidMinor != evidence.ItemPaidMinor || !refundEvidence[0].PaymentConfirmedAt.Equal(evidence.PaymentConfirmedAt) {
		t.Fatalf("refund evidence=%+v err=%v", refundEvidence, err)
	}

	var count int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM order_distribution_qualification_evidence WHERE order_id=$1 AND order_item_line=1`, orderID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("evidence rows=%d err=%v", count, err)
	}

	cases := []struct {
		name   string
		mutate func(*orderport.HistoricalQualificationEvidence)
	}{
		{name: "product id", mutate: func(v *orderport.HistoricalQualificationEvidence) { v.ProductID = 78 }},
		{name: "product type", mutate: func(v *orderport.HistoricalQualificationEvidence) { v.ProductType = "service_period" }},
		{name: "source product code", mutate: func(v *orderport.HistoricalQualificationEvidence) { v.SourceProductCode = "other-course" }},
		{name: "payer", mutate: func(v *orderport.HistoricalQualificationEvidence) { v.PayerCustomerID = 102 }},
		{name: "beneficiary", mutate: func(v *orderport.HistoricalQualificationEvidence) { v.BeneficiaryCustomerID = 102 }},
		{name: "amount", mutate: func(v *orderport.HistoricalQualificationEvidence) { v.ItemPaidMinor = 989 }},
		{name: "payment time", mutate: func(v *orderport.HistoricalQualificationEvidence) {
			v.PaymentConfirmedAt = v.PaymentConfirmedAt.Add(time.Second)
		}},
		{name: "source digest", mutate: func(v *orderport.HistoricalQualificationEvidence) { v.SourceDigest[0] ^= 0xff }},
		{name: "source order digest", mutate: func(v *orderport.HistoricalQualificationEvidence) { v.SourceOrderDigest[0] ^= 0xff }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			changed := evidence
			test.mutate(&changed)
			if err := importEvidence(changed); !errors.Is(err, orderport.ErrConflict) {
				t.Fatalf("drift err=%v, want conflict", err)
			}
		})
	}
}

func TestPostgreSQLQualificationRefundEvidenceUnionUsesOnlyFrozenNativeAndVerifiedHistory(t *testing.T) {
	native, cleanup := orderIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()

	wrapper, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}

	confirmedAt := time.Date(2026, 9, 14, 11, 0, 0, 0, time.UTC)
	var nativeOrderID int64
	if err = native.QueryRow(ctx, `INSERT INTO orders(
	provider,source_system,source_key,merchant_order_no,payer_customer_id,beneficiary_customer_id,
	amount_minor,refunded_minor,currency,status,record_origin,effect_eligible,version,created_at,updated_at
) VALUES('wechat_pay','v3-checkout','native-union-1','M-native-union-1',301,301,
	880,0,'CNY','paid','native',true,2,$1,$1) RETURNING id`, confirmedAt).Scan(&nativeOrderID); err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, `INSERT INTO order_items(order_id,line_no,product_id,product_version,product_code,product_name,unit_amount_minor,quantity,line_amount_minor)
VALUES($1,1,88,1,'native-course-88','原生可信购买',880,1,880)`, nativeOrderID); err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, `INSERT INTO order_checkout_snapshots(
order_id,product_type,product_id,product_code,product_name,product_version,service_period_duration_days,
gross_amount_minor,discount_amount_minor,payable_amount_minor,currency,coupon_applied,coupon_reservation_ref,
reserved_at,created_at
) VALUES($1,'standard_product',88,'native-course-88','原生可信购买',1,0,880,0,880,'CNY',false,'',$2,$2)`, nativeOrderID, confirmedAt); err != nil {
		t.Fatal(err)
	}
	paidDigest := sha256.Sum256([]byte("native-union-paid"))
	if _, err = native.Exec(ctx, `INSERT INTO order_paid_events(order_id,order_version,source_digest,occurred_at) VALUES($1,2,$2,$3)`, nativeOrderID, paidDigest[:], confirmedAt); err != nil {
		t.Fatal(err)
	}

	var historyOrderID int64
	historyDigest := sha256.Sum256([]byte("history-union-order"))
	if err = native.QueryRow(ctx, `INSERT INTO orders(
	provider,source_system,source_key,merchant_order_no,payer_customer_id,beneficiary_customer_id,
	amount_minor,refunded_minor,currency,status,record_origin,effect_eligible,source_row_digest,
	version,created_at,updated_at
) VALUES('wechat_pay','legacy-qualified','history-union-1','M-history-union-1',302,302,
	990,0,'CNY','paid','history',false,$1,1,$2,$2) RETURNING id`, historyDigest[:], confirmedAt).Scan(&historyOrderID); err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, `INSERT INTO order_items(order_id,line_no,product_id,product_code,product_name,unit_amount_minor,quantity,line_amount_minor)
VALUES($1,1,89,'history-course-89','历史可信购买',990,1,990)`, historyOrderID); err != nil {
		t.Fatal(err)
	}
	historyEvidence := orderport.HistoricalQualificationEvidence{OrderID: historyOrderID, OrderItemLine: 1, ProductID: 89, ProductType: "service_period", SourceProductCode: "history-course-89", PayerCustomerID: 302, BeneficiaryCustomerID: 302, ItemPaidMinor: 990, PaymentConfirmedAt: confirmedAt, SourceOrderDigest: historyDigest, SourceDigest: sha256.Sum256([]byte("history-union-mapping"))}
	if err = uow.Within(ctx, func(tx context.Context) error {
		return repository.ImportHistoricalQualificationEvidenceWithin(tx, historyEvidence)
	}); err != nil {
		t.Fatal(err)
	}

	assertEvidence := func(orderID, productID int64, origin, productType string) {
		var rows []orderport.QualificationRefundEvidence
		err = uow.Within(ctx, func(tx context.Context) error {
			rows, err = repository.ListQualificationRefundEvidenceByOrderWithin(tx, orderID)
			return err
		})
		if err != nil || len(rows) != 1 || rows[0].OrderID != orderID || rows[0].ProductID != productID || rows[0].RecordOrigin != origin || rows[0].ProductType != productType || !rows[0].PaymentConfirmedAt.Equal(confirmedAt) {
			t.Fatalf("order=%d rows=%+v err=%v", orderID, rows, err)
		}
	}
	assertEvidence(nativeOrderID, 88, "native", "standard_product")
	assertEvidence(historyOrderID, 89, "history", "service_period")
}
