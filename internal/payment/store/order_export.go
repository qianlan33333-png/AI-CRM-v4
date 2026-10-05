package store

import (
	"context"

	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
)

var _ paymentport.OrderExportPaymentReader = (*Repository)(nil)

func (r *Repository) ReadOrderExportPaymentsWithin(ctx context.Context, ids []int64) (map[int64]paymentport.OrderExportPayment, error) {
	t, err := tx(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[int64]paymentport.OrderExportPayment)
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := t.Query(ctx, `SELECT order_id,paid_confirmed_at,COALESCE(provider_transaction_reference,'') FROM payments WHERE order_id=ANY($1) ORDER BY id`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var value paymentport.OrderExportPayment
		if err = rows.Scan(&id, &value.PaidAt, &value.TransactionNo); err != nil {
			return nil, err
		}
		if _, exists := out[id]; exists {
			return nil, paymentport.ErrConflict
		}
		out[id] = value
	}
	return out, rows.Err()
}
