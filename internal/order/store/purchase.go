package store

import (
	"context"
	"fmt"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	"sort"
)

func (r *Repository) ReadStandardPurchaseWithin(ctx context.Context, q orderport.StandardPurchaseQuery) (orderport.StandardPurchaseState, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return orderport.StandardPurchaseState{}, err
	}
	if q.ProductID < 1 || q.ProductCode == "" || len(q.CustomerIDs) == 0 {
		return orderport.StandardPurchaseState{}, ErrInvalid
	}
	ids := append([]int64(nil), q.CustomerIDs...)
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		if id < 1 {
			return orderport.StandardPurchaseState{}, ErrInvalid
		}
		if q.Lock {
			if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, fmt.Sprintf("order.standard-purchase:%d:%d", id, q.ProductID)); err != nil {
				return orderport.StandardPurchaseState{}, mapError(err)
			}
		}
	}
	if q.CurrentOrderID > 0 {
		var periodic bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM order_checkout_snapshots WHERE order_id=$1 AND product_type='service_period')`, q.CurrentOrderID).Scan(&periodic); err != nil {
			return orderport.StandardPurchaseState{}, mapError(err)
		}
		if periodic {
			return orderport.StandardPurchaseState{}, nil
		}
	}
	var result orderport.StandardPurchaseState
	err = tx.QueryRow(ctx, `SELECT COALESCE(bool_or((o.status IN ('paid','partially_refunded') OR EXISTS(SELECT 1 FROM order_status_history h WHERE h.order_id=o.id AND h.to_status IN ('paid','partially_refunded','refunded'))) AND o.status<>'refunded' AND o.refunded_minor<o.amount_minor),false),COALESCE(bool_or(o.status='pending_payment' AND o.id<>$4 AND NOT(o.id=ANY($5::bigint[]))),false),COALESCE(max(CASE WHEN (o.status IN ('paid','partially_refunded') OR EXISTS(SELECT 1 FROM order_status_history h WHERE h.order_id=o.id AND h.to_status IN ('paid','partially_refunded','refunded'))) AND o.status<>'refunded' AND o.refunded_minor<o.amount_minor THEN o.id END),0),COALESCE((array_agg(o.merchant_order_no ORDER BY o.id DESC) FILTER (WHERE (o.status IN ('paid','partially_refunded') OR EXISTS(SELECT 1 FROM order_status_history h WHERE h.order_id=o.id AND h.to_status IN ('paid','partially_refunded','refunded'))) AND o.status<>'refunded' AND o.refunded_minor<o.amount_minor))[1],'')
 FROM orders o JOIN order_items i ON i.order_id=o.id
 LEFT JOIN order_checkout_snapshots c ON c.order_id=o.id
 WHERE COALESCE(o.beneficiary_customer_id,o.payer_customer_id)=ANY($1::bigint[])
 AND (i.product_id=$2 OR (i.product_id IS NULL AND i.product_code=$3))
 AND (c.product_type IS NULL OR c.product_type='standard_product')`, ids, q.ProductID, q.ProductCode, q.CurrentOrderID, append([]int64{}, q.ExcludedPendingOrderIDs...)).Scan(&result.Owned, &result.Pending, &result.PaidOrderID, &result.MerchantOrderNo)
	return result, mapError(err)
}
