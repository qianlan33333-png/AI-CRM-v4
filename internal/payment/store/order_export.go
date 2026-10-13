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
	rows, err := t.Query(ctx, `SELECT p.order_id,p.paid_confirmed_at,COALESCE(p.provider_transaction_reference,''),p.amount_minor,
		COALESCE(s.shared_minor,0),COALESCE(s.processing,false),COALESCE(s.unknown,false),COALESCE(s.exception,false)
		FROM payments p
		LEFT JOIN LATERAL (
			SELECT SUM(i.amount_minor) FILTER (WHERE i.state='paid' AND i.receiver_confirmed_success AND i.outcome_known) AS shared_minor,
				BOOL_OR(i.state IN ('accepted','settling')) AS processing,
				BOOL_OR(i.state='outcome_unknown' OR (i.state='exception' AND NOT i.outcome_known)) AS unknown,
				BOOL_OR(i.state='exception') AS exception
			FROM payment_profit_sharing_instructions i WHERE i.payment_id=p.id
		) s ON true
		WHERE p.order_id=ANY($1) ORDER BY p.id`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var value paymentport.OrderExportPayment
		if err = rows.Scan(&id, &value.PaidAt, &value.TransactionNo, &value.AmountMinor, &value.SharedMinor, &value.SharingProcessing, &value.SharingUnknown, &value.SharingException); err != nil {
			return nil, err
		}
		if _, exists := out[id]; exists {
			return nil, paymentport.ErrConflict
		}
		out[id] = value
	}
	return out, rows.Err()
}
