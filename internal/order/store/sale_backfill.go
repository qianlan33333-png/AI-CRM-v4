package store

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
)

// ListPaidSaleOrderIDsWithin pages native orders with any first-paid evidence,
// including ones missing the durable paid event. The latter become explicit
// backfill exceptions when an attribution exists.
func (r *Repository) ListPaidSaleOrderIDsWithin(ctx context.Context, afterID int64, limit int32) ([]int64, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, err
	}
	if afterID < 0 || limit < 1 || limit > 500 {
		return nil, ErrInvalid
	}
	rows, err := tx.Query(ctx, `SELECT o.id FROM orders o
WHERE o.id>$1 AND o.record_origin='native' AND o.effect_eligible
AND (EXISTS(SELECT 1 FROM order_paid_events pe WHERE pe.order_id=o.id)
 OR EXISTS(SELECT 1 FROM order_status_history h WHERE h.order_id=o.id AND h.to_status='paid'))
ORDER BY o.id LIMIT $2`, afterID, limit)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	result := make([]int64, 0, limit)
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return nil, mapError(err)
		}
		result = append(result, id)
	}
	return result, mapError(rows.Err())
}

func (r *Repository) ReadPaidSaleBackfillFactWithin(ctx context.Context, orderID int64) (orderport.PaidSaleBackfillFact, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return orderport.PaidSaleBackfillFact{}, err
	}
	if orderID < 1 {
		return orderport.PaidSaleBackfillFact{}, ErrInvalid
	}
	order, err := r.Get(ctx, orderID, false)
	if err != nil {
		return orderport.PaidSaleBackfillFact{}, err
	}
	current := order.Snapshot()
	if current.RecordOrigin != domain.RecordOriginNative || !current.EffectEligible {
		return orderport.PaidSaleBackfillFact{}, orderport.ErrConflict
	}
	var event orderport.PaidEvent
	var source []byte
	err = tx.QueryRow(ctx, `SELECT pe.id,pe.order_version,pe.source_digest,pe.occurred_at,ob.id
FROM order_paid_events pe JOIN order_outbox ob
ON ob.aggregate_id=pe.order_id AND ob.event_type='order.paid.v1'
AND ob.idempotency_key='order.paid.v1:'||pe.id::text
WHERE pe.order_id=$1`, orderID).Scan(&event.ID, &event.OrderVersion, &source, &event.OccurredAt, &event.DomainEventOutboxID)
	if errors.Is(err, pgx.ErrNoRows) {
		return orderport.PaidSaleBackfillFact{}, orderport.ErrNotFound
	}
	if err != nil {
		return orderport.PaidSaleBackfillFact{}, mapError(err)
	}
	event.OrderID = orderID
	expectedSource := orderport.NewPaidEventSourceDigest(orderID, event.OrderVersion)
	if len(source) != len(event.SourceDigest) || string(source) != string(expectedSource[:]) {
		return orderport.PaidSaleBackfillFact{}, orderport.ErrConflict
	}
	copy(event.SourceDigest[:], source)
	var paidStatus domain.Status
	var paidRefunded int64
	var paidAt = event.OccurredAt
	err = tx.QueryRow(ctx, `SELECT to_status,refunded_minor,occurred_at FROM order_status_history
WHERE order_id=$1 AND order_version=$2`, orderID, event.OrderVersion).Scan(&paidStatus, &paidRefunded, &paidAt)
	if err != nil || paidStatus != domain.StatusPaid || paidRefunded != 0 || !paidAt.Equal(event.OccurredAt) {
		return orderport.PaidSaleBackfillFact{}, orderport.ErrConflict
	}
	current.Status, current.Version, current.UpdatedAt, current.RefundedMinor = domain.StatusPaid, event.OrderVersion, event.OccurredAt, 0
	event.Order = current
	checkout, err := r.ReadCheckoutSnapshot(ctx, orderID)
	if err != nil {
		return orderport.PaidSaleBackfillFact{}, err
	}
	event.CheckoutProductID = checkout.ProductID
	event.CheckoutProductType = checkout.ProductType
	event.CheckoutGrossAmountMinor = checkout.GrossAmountMinor
	event.CheckoutPayableAmountMinor = checkout.PayableAmountMinor
	event.ReferralActivityContextDigest = checkout.ReferralActivityContextDigest
	event.PromotionContextDigest = checkout.PromotionContextDigest
	if !event.Valid() {
		return orderport.PaidSaleBackfillFact{}, orderport.ErrConflict
	}
	rows, err := tx.Query(ctx, `SELECT order_version,to_status,refunded_minor,actor_scope,occurred_at
FROM order_status_history WHERE order_id=$1 AND order_version>$2
ORDER BY order_version`, orderID, event.OrderVersion)
	if err != nil {
		return orderport.PaidSaleBackfillFact{}, mapError(err)
	}
	defer rows.Close()
	fact := orderport.PaidSaleBackfillFact{Paid: event, Refunds: []orderport.RefundSettlementEvent{}}
	lastRefunded := int64(0)
	lastVersion, lastAt := event.OrderVersion, event.OccurredAt
	for rows.Next() {
		var version, cumulative int64
		var status domain.Status
		var actor string
		var at = event.OccurredAt
		if err = rows.Scan(&version, &status, &cumulative, &actor, &at); err != nil {
			return orderport.PaidSaleBackfillFact{}, mapError(err)
		}
		if version <= lastVersion || at.Before(lastAt) || (status != domain.StatusPaid && status != domain.StatusPartiallyRefunded && status != domain.StatusRefunded) {
			return orderport.PaidSaleBackfillFact{}, orderport.ErrConflict
		}
		lastVersion, lastAt = version, at
		if cumulative == lastRefunded {
			continue
		}
		if cumulative < lastRefunded || cumulative > current.Amount.AmountMinor || (status != domain.StatusPartiallyRefunded && status != domain.StatusRefunded) || !strings.HasPrefix(actor, "payment:") || len(actor) <= len("payment:") {
			return orderport.PaidSaleBackfillFact{}, orderport.ErrConflict
		}
		refund := orderport.RefundSettlementEvent{Order: current, RefundedDelta: cumulative - lastRefunded,
			CheckoutProductID: checkout.ProductID, CheckoutProductType: checkout.ProductType,
			OccurredAt: at, ReceiptKey: strings.TrimPrefix(actor, "payment:")}
		refund.Order.Version, refund.Order.Status, refund.Order.RefundedMinor, refund.Order.UpdatedAt = version, status, cumulative, at
		if !refund.Valid() {
			return orderport.PaidSaleBackfillFact{}, orderport.ErrConflict
		}
		fact.Refunds = append(fact.Refunds, refund)
		lastRefunded = cumulative
	}
	if err = rows.Err(); err != nil {
		return orderport.PaidSaleBackfillFact{}, mapError(err)
	}
	if lastRefunded != order.Snapshot().RefundedMinor {
		return orderport.PaidSaleBackfillFact{}, orderport.ErrConflict
	}
	return fact, nil
}

var _ orderport.PaidSaleBackfillReader = (*Repository)(nil)
