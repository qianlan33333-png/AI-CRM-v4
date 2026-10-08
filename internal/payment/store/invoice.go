package store

import (
	"context"
	p "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
)

func (r *Repository) InvoicePaymentWithin(ctx context.Context, transaction string) (p.InvoicePayment, error) {
	var out p.InvoicePayment
	if transaction == "" {
		return out, p.ErrInvalid
	}
	t, e := tx(ctx)
	if e != nil {
		return out, e
	}
	// Provider and historical predicates prevent imported/other-channel facts
	// from becoming authority for a native bill callback.
	rows, e := t.Query(ctx, `SELECT order_id,payer_customer_id,payer_identity_id,amount_minor,merchant_order_no,provider_transaction_reference,currency FROM payments WHERE provider='wechat_pay' AND NOT historical AND paid_confirmed_at IS NOT NULL AND payer_identity_id>0 AND payer_customer_id>0 AND provider_transaction_reference=$1`, transaction)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	if !rows.Next() {
		if e = rows.Err(); e != nil {
			return out, e
		}
		return out, p.ErrNotFound
	}
	if e = rows.Scan(&out.OrderID, &out.CustomerID, &out.IdentityID, &out.Amount, &out.OrderNo, &out.TransactionID, &out.Currency); e != nil {
		return out, e
	}
	if rows.Next() {
		return p.InvoicePayment{}, p.ErrConflict
	}
	if e = rows.Err(); e != nil {
		return out, e
	}
	return out, nil
}

var _ p.InvoicePaymentReader = (*Repository)(nil)
