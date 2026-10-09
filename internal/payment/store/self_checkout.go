package store

import (
	"context"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
)

func (r *Repository) SelfCheckoutWithin(ctx context.Context, orderID int64) (domain.Payment, error) {
	t, e := tx(ctx)
	if e != nil {
		return domain.Payment{}, e
	}
	var id int64
	e = t.QueryRow(ctx, `SELECT id FROM payments WHERE order_id=$1`, orderID).Scan(&id)
	if e != nil {
		return domain.Payment{}, mapError(e)
	}
	return r.GetPayment(ctx, id, false)
}
