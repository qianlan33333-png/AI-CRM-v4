package store

import "context"

func (r *Repository) RestartAllowedOrderIDs(ctx context.Context) ([]int64, error) {
	tx, err := tx(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT p.order_id FROM payment_checkout_restart_permissions r JOIN payments p ON p.id=r.payment_id WHERE p.status='awaiting_prepay' AND NOT EXISTS(SELECT 1 FROM payment_handoffs h WHERE h.payment_id=p.id) AND NOT EXISTS(SELECT 1 FROM payment_callback_receipts c WHERE c.payment_id=p.id)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
