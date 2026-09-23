package store

import (
	"context"
	"time"

	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

type OpsConsistencyReader struct{}

func NewOpsConsistencyReader() *OpsConsistencyReader { return &OpsConsistencyReader{} }
func (*OpsConsistencyReader) ReadOpsPaymentOrderFactsWithin(ctx context.Context, ids []int64) ([]orderport.OpsPaymentOrderFact, error) {
	if len(ids) < 1 || len(ids) > 500 {
		return nil, ErrInvalid
	}
	for _, id := range ids {
		if id < 1 {
			return nil, ErrInvalid
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	tx, e := platformpostgres.RequireTransaction(ctx)
	if e != nil {
		return nil, e
	}
	var snapshot bool
	if e = tx.QueryRow(ctx, `SELECT current_setting('transaction_read_only')='on' AND current_setting('transaction_isolation')='repeatable read'`).Scan(&snapshot); e != nil {
		return nil, e
	}
	if !snapshot {
		return nil, orderport.ErrUnavailable
	}
	if _, e = tx.Exec(ctx, `SET LOCAL statement_timeout='2500ms';SET LOCAL lock_timeout='250ms'`); e != nil {
		return nil, e
	}
	rows, e := tx.Query(ctx, `SELECT o.id,o.provider,o.amount_minor,o.currency,o.refunded_minor,COALESCE(o.payer_customer_id,0),COALESCE(o.beneficiary_customer_id,0),o.status,o.record_origin,
 c.order_id IS NOT NULL,COALESCE(c.product_type,''),COALESCE(c.payable_amount_minor,0),COALESCE(c.currency,''),
 EXISTS(SELECT 1 FROM order_paid_events pe WHERE pe.order_id=o.id),
 EXISTS(SELECT 1 FROM order_entitlement_fulfillment_receipts r WHERE r.source_order_id=o.id AND r.operation='grant'),
 EXISTS(SELECT 1 FROM order_entitlement_fulfillment_receipts r JOIN order_service_entitlements e ON e.id=r.entitlement_id WHERE r.source_order_id=o.id AND r.operation='grant' AND r.duration_days=c.service_period_duration_days AND e.customer_id=o.beneficiary_customer_id AND e.service_product_id=c.product_id),
 EXISTS(SELECT 1 FROM order_entitlement_fulfillment_receipts r WHERE r.source_order_id=o.id AND r.operation='refund')
 FROM orders o LEFT JOIN order_checkout_snapshots c ON c.order_id=o.id WHERE o.id=ANY($1::bigint[]) ORDER BY o.id`, ids)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []orderport.OpsPaymentOrderFact{}
	for rows.Next() {
		var v orderport.OpsPaymentOrderFact
		if e = rows.Scan(&v.OrderID, &v.Provider, &v.AmountMinor, &v.Currency, &v.RefundedMinor, &v.PayerCustomerID, &v.BeneficiaryCustomerID, &v.Status, &v.RecordOrigin, &v.CheckoutPresent, &v.CheckoutProductType, &v.CheckoutPayableMinor, &v.CheckoutCurrency, &v.PaidEventPresent, &v.ServiceGrantPresent, &v.ServiceGrantMatches, &v.ServiceRefundPresent); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

var _ orderport.OpsPaymentOrderReader = (*OpsConsistencyReader)(nil)
