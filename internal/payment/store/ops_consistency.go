package store

import (
	"context"
	"time"

	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

type OpsConsistencyReader struct{}

func NewOpsConsistencyReader() *OpsConsistencyReader { return &OpsConsistencyReader{} }
func (*OpsConsistencyReader) ReadOpsPaidOrderPageWithin(ctx context.Context, afterOrderID int64, limit int32) (paymentport.OpsPaidOrderPage, error) {
	out := paymentport.OpsPaidOrderPage{Items: []paymentport.OpsPaidOrderFact{}}
	if afterOrderID < 0 || limit < 1 || limit > 500 {
		return out, paymentport.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	tx, e := platformpostgres.RequireTransaction(ctx)
	if e != nil {
		return out, e
	}
	var snapshot bool
	if e = tx.QueryRow(ctx, `SELECT current_setting('transaction_read_only')='on' AND current_setting('transaction_isolation')='repeatable read'`).Scan(&snapshot); e != nil {
		return out, e
	}
	if !snapshot {
		return out, paymentport.ErrUnavailable
	}
	if _, e = tx.Exec(ctx, `SET LOCAL statement_timeout='2500ms';SET LOCAL lock_timeout='250ms'`); e != nil {
		return out, e
	}
	rows, e := tx.Query(ctx, `SELECT p.id,p.order_id,p.provider,p.amount_minor,p.currency,p.payer_customer_id,p.beneficiary_customer_id,p.paid_confirmed_at,
 COALESCE((SELECT sum(r.amount_minor) FROM payment_refunds r WHERE r.payment_id=p.id AND r.status='completed'),0)::bigint
 FROM payments p WHERE p.status='paid' AND NOT p.historical AND p.order_id>$1 ORDER BY p.order_id LIMIT $2`, afterOrderID, limit+1)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		var v paymentport.OpsPaidOrderFact
		if e = rows.Scan(&v.PaymentID, &v.OrderID, &v.Provider, &v.AmountMinor, &v.Currency, &v.PayerCustomerID, &v.BeneficiaryCustomerID, &v.PaidConfirmedAt, &v.CompletedRefundMinor); e != nil {
			return out, e
		}
		out.Items = append(out.Items, v)
	}
	if e = rows.Err(); e != nil {
		return out, e
	}
	if len(out.Items) > int(limit) {
		out.More = true
		out.Items = out.Items[:limit]
	}
	return out, nil
}

var _ paymentport.OpsPaidOrderReader = (*OpsConsistencyReader)(nil)
