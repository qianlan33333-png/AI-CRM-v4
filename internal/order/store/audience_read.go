package store

import (
	"context"
	"strings"
	"time"

	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// PaidAudienceOrders exposes only payer facts. The
// timestamp is the first transition to paid, never a mutable order update.
func (s *Repository) PaidAudienceOrders(ctx context.Context, reference time.Time) ([]orderport.PaidAudienceOrder, error) {
	if reference.IsZero() {
		return nil, ErrInvalid
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT o.id,o.payer_customer_id,i.product_code,''::text,(SELECT MIN(h.occurred_at) FROM order_status_history h WHERE h.order_id=o.id AND h.to_status='paid')
		FROM orders o JOIN order_items i ON i.order_id=o.id
		WHERE o.status='paid' AND o.payer_customer_id IS NOT NULL
		ORDER BY o.id,i.line_no`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []orderport.PaidAudienceOrder{}
	for rows.Next() {
		var item orderport.PaidAudienceOrder
		if err = rows.Scan(&item.OrderID, &item.CustomerID, &item.ProductCode, &item.OwnerReference, &item.PaidAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

var _ orderport.PaidAudienceReader = (*Repository)(nil)

func (s *Repository) PaidAudiencePurchaseHistory(ctx context.Context, productCodes []string, reference time.Time) ([]orderport.PaidAudiencePurchase, error) {
	if reference.IsZero() || len(productCodes) == 0 || len(productCodes) > 100 {
		return nil, ErrInvalid
	}
	seen := map[string]bool{}
	for _, code := range productCodes {
		if code == "" || strings.TrimSpace(code) != code || len(code) > 200 || seen[code] {
			return nil, ErrInvalid
		}
		seen[code] = true
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT o.id,o.payer_customer_id,i.product_code,paid.first_paid_at
		FROM orders o JOIN order_items i ON i.order_id=o.id
		LEFT JOIN LATERAL (
			SELECT MIN(history.occurred_at) AS first_paid_at
			FROM order_status_history history
			WHERE history.order_id=o.id AND history.to_status='paid' AND history.occurred_at <= $2
		) paid ON true
		WHERE o.payer_customer_id IS NOT NULL AND i.product_code=ANY($1::text[])
			AND (o.status='paid' OR paid.first_paid_at IS NOT NULL)
		GROUP BY o.id,o.payer_customer_id,i.product_code,paid.first_paid_at
		ORDER BY o.payer_customer_id,i.product_code,paid.first_paid_at NULLS FIRST,o.id`, productCodes, reference.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []orderport.PaidAudiencePurchase{}
	for rows.Next() {
		var item orderport.PaidAudiencePurchase
		if err = rows.Scan(&item.OrderID, &item.CustomerID, &item.ProductCode, &item.PaidAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

var _ orderport.PaidAudiencePurchaseHistoryReader = (*Repository)(nil)

func (s *Repository) HistoricalAudienceProductCodeExists(ctx context.Context, code string) (bool, error) {
	if code == "" || strings.TrimSpace(code) != code || len(code) > 200 {
		return false, ErrInvalid
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return false, err
	}
	var found bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM order_items i JOIN orders o ON o.id=i.order_id WHERE i.product_code=$1 AND o.record_origin='history' AND o.source_system='commerce-history')`, code).Scan(&found)
	return found, err
}

var _ orderport.HistoricalAudienceProductReader = (*Repository)(nil)
