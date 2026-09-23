package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	orderdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
)

// ListExternalRead is separate from the admin list: its predicates are exact,
// its keyset is supplied by the public operation, and its paid time comes only
// from Order's immutable paid event (never an import/update timestamp).
func (r *Repository) ListExternalRead(ctx context.Context, q orderport.ExternalReadQuery) (orderport.ExternalReadPage, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return orderport.ExternalReadPage{}, err
	}
	// The public executor requests one private look-ahead row; 101 is never a
	// client-visible page size.
	if q.Limit < 1 || q.Limit > 101 || (q.AfterID != 0 && q.AfterCreatedAt.IsZero()) {
		return orderport.ExternalReadPage{}, ErrInvalid
	}
	args := []any{}
	where := []string{"TRUE"}
	add := func(clause string, value any) {
		args = append(args, value)
		where = append(where, clause+"$"+itoa(len(args)))
	}
	if q.Provider != "" {
		add("o.provider=", q.Provider)
	}
	if q.ProductCode != "" {
		add("EXISTS (SELECT 1 FROM order_items oi WHERE oi.order_id=o.id AND oi.product_code=", q.ProductCode)
		where[len(where)-1] += ")"
	}
	if q.MerchantOrderNo != "" {
		add("o.merchant_order_no=", q.MerchantOrderNo)
	}
	if q.ProviderTransactionNo != "" {
		add("o.provider_transaction_no=", q.ProviderTransactionNo)
	}
	if q.SourceSystem != "" {
		add("o.source_system=", q.SourceSystem)
		add("o.source_key=", q.SourceRecordID)
	}
	if len(q.CustomerIDs) > 0 {
		add("(o.payer_customer_id=ANY(", q.CustomerIDs)
		where[len(where)-1] += ") OR o.beneficiary_customer_id=ANY($" + itoa(len(args)) + ") )"
	}
	if q.CreatedFrom != nil {
		add("o.created_at>=", q.CreatedFrom.UTC())
	}
	if q.CreatedTo != nil {
		add("o.created_at<=", q.CreatedTo.UTC())
	}
	if q.PaidFrom != nil {
		add("pe.occurred_at>=", q.PaidFrom.UTC())
	}
	if q.PaidTo != nil {
		add("pe.occurred_at<=", q.PaidTo.UTC())
	}
	if q.IsPaid != nil {
		if *q.IsPaid {
			where = append(where, "EXISTS (SELECT 1 FROM order_status_history h WHERE h.order_id=o.id AND h.to_status IN ('paid','partially_refunded','refunded'))")
		} else {
			where = append(where, "NOT EXISTS (SELECT 1 FROM order_status_history h WHERE h.order_id=o.id AND h.to_status IN ('paid','partially_refunded','refunded'))")
		}
	}
	if q.IsRefunded != nil {
		if *q.IsRefunded && len(q.RefundedOrderIDs) == 0 {
			where = append(where, "FALSE")
		}
		if len(q.RefundedOrderIDs) > 0 {
			add("o.id", q.RefundedOrderIDs)
			if *q.IsRefunded {
				where[len(where)-1] = "o.id=ANY($" + itoa(len(args)) + ")"
			} else {
				where[len(where)-1] = "o.id<>ALL($" + itoa(len(args)) + ")"
			}
		}
		if !*q.IsRefunded {
			if len(q.RefundKnownOrderIDs) == 0 {
				where = append(where, "FALSE")
			} else {
				add("o.id", q.RefundKnownOrderIDs)
				where[len(where)-1] = "o.id=ANY($" + itoa(len(args)) + ")"
			}
		}
	}
	if q.AfterID != 0 {
		args = append(args, q.AfterCreatedAt.UTC(), q.AfterID)
		n := len(args)
		where = append(where, "(o.created_at,o.id)<($"+itoa(n-1)+",$"+itoa(n)+")")
	}
	args = append(args, q.Limit)
	rows, err := tx.Query(ctx, `SELECT o.id,o.provider,o.source_system,o.source_key,o.merchant_order_no,o.provider_transaction_no,o.payer_customer_id,o.beneficiary_customer_id,o.amount_minor,o.currency,o.status,o.created_at,pe.occurred_at,
	 (o.status IN ('paid','partially_refunded','refunded') OR EXISTS (SELECT 1 FROM order_status_history h WHERE h.order_id=o.id AND h.to_status IN ('paid','partially_refunded','refunded'))),
 COALESCE((SELECT array_agg(oi.product_code ORDER BY oi.line_no) FROM order_items oi WHERE oi.order_id=o.id),ARRAY[]::text[]),
 COALESCE((SELECT jsonb_agg(jsonb_build_object('line_no',oi.line_no,'product_code',oi.product_code,'product_name',oi.product_name,'unit_amount_minor',oi.unit_amount_minor,'quantity',oi.quantity,'line_amount_minor',oi.line_amount_minor) ORDER BY oi.line_no) FROM order_items oi WHERE oi.order_id=o.id),'[]'::jsonb)
 FROM orders o LEFT JOIN order_paid_events pe ON pe.order_id=o.id WHERE `+strings.Join(where, " AND ")+` ORDER BY o.created_at DESC,o.id DESC LIMIT $`+itoa(len(args)), args...)
	if err != nil {
		return orderport.ExternalReadPage{}, mapError(err)
	}
	defer rows.Close()
	page := orderport.ExternalReadPage{Items: make([]orderport.ExternalOrder, 0, q.Limit)}
	for rows.Next() {
		var x orderport.ExternalOrder
		var paid *time.Time
		var itemsRaw []byte
		if err = rows.Scan(&x.ID, &x.Provider, &x.SourceSystem, &x.SourceKey, &x.MerchantOrderNo, &x.ProviderTransactionNo, &x.PayerCustomerID, &x.BeneficiaryCustomerID, &x.Amount.AmountMinor, &x.Amount.Currency, &x.Status, &x.CreatedAt, &paid, &x.IsPaid, &x.ProductCodes, &itemsRaw); err != nil {
			return orderport.ExternalReadPage{}, mapError(err)
		}
		if x.Items, err = decodeExternalReadItems(itemsRaw); err != nil {
			return orderport.ExternalReadPage{}, err
		}
		x.PaidAt = paid
		page.Items = append(page.Items, x)
	}
	return page, mapError(rows.Err())
}

func (r *Repository) GetExternalRead(ctx context.Context, id int64, customerIDs []int64) (orderport.ExternalOrder, error) {
	if id < 1 {
		return orderport.ExternalOrder{}, orderport.ErrNotFound
	}
	// An ID lookup keeps the same scope predicate without exposing an unscoped row.
	tx, err := transaction(ctx)
	if err != nil {
		return orderport.ExternalOrder{}, err
	}
	args := []any{id}
	where := "o.id=$1"
	if len(customerIDs) > 0 {
		args = append(args, customerIDs)
		where += " AND (o.payer_customer_id=ANY($2) OR o.beneficiary_customer_id=ANY($2))"
	}
	row := tx.QueryRow(ctx, `SELECT o.id,o.provider,o.source_system,o.source_key,o.merchant_order_no,o.provider_transaction_no,o.payer_customer_id,o.beneficiary_customer_id,o.amount_minor,o.currency,o.status,o.created_at,pe.occurred_at,(o.status IN ('paid','partially_refunded','refunded') OR EXISTS (SELECT 1 FROM order_status_history h WHERE h.order_id=o.id AND h.to_status IN ('paid','partially_refunded','refunded'))),COALESCE((SELECT array_agg(oi.product_code ORDER BY oi.line_no) FROM order_items oi WHERE oi.order_id=o.id),ARRAY[]::text[]),COALESCE((SELECT jsonb_agg(jsonb_build_object('line_no',oi.line_no,'product_code',oi.product_code,'product_name',oi.product_name,'unit_amount_minor',oi.unit_amount_minor,'quantity',oi.quantity,'line_amount_minor',oi.line_amount_minor) ORDER BY oi.line_no) FROM order_items oi WHERE oi.order_id=o.id),'[]'::jsonb) FROM orders o LEFT JOIN order_paid_events pe ON pe.order_id=o.id WHERE `+where, args...)
	var x orderport.ExternalOrder
	var paid *time.Time
	var itemsRaw []byte
	if err = row.Scan(&x.ID, &x.Provider, &x.SourceSystem, &x.SourceKey, &x.MerchantOrderNo, &x.ProviderTransactionNo, &x.PayerCustomerID, &x.BeneficiaryCustomerID, &x.Amount.AmountMinor, &x.Amount.Currency, &x.Status, &x.CreatedAt, &paid, &x.IsPaid, &x.ProductCodes, &itemsRaw); errors.Is(err, pgx.ErrNoRows) {
		return orderport.ExternalOrder{}, orderport.ErrNotFound
	} else if err != nil {
		return orderport.ExternalOrder{}, mapError(err)
	}
	if x.Items, err = decodeExternalReadItems(itemsRaw); err != nil {
		return orderport.ExternalOrder{}, err
	}
	x.PaidAt = paid
	return x, nil
}

func decodeExternalReadItems(raw []byte) ([]orderport.ExternalOrderItem, error) {
	var encoded []struct {
		LineNo          int32  `json:"line_no"`
		ProductCode     string `json:"product_code"`
		ProductName     string `json:"product_name"`
		UnitAmountMinor int64  `json:"unit_amount_minor"`
		Quantity        int32  `json:"quantity"`
		LineAmountMinor int64  `json:"line_amount_minor"`
	}
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return nil, err
	}
	items := make([]orderport.ExternalOrderItem, 0, len(encoded))
	for _, item := range encoded {
		items = append(items, orderport.ExternalOrderItem{LineNo: item.LineNo, ProductCode: item.ProductCode, ProductName: item.ProductName, UnitAmountMinor: item.UnitAmountMinor, Quantity: item.Quantity, LineAmountMinor: item.LineAmountMinor})
	}
	return items, nil
}

func (r *Repository) ExternalOrderTimeline(ctx context.Context, id int64) ([]orderport.ExternalOrderTimelineEvent, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT to_status,refunded_minor,occurred_at FROM order_status_history WHERE order_id=$1 ORDER BY occurred_at,id`, id)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	items := []orderport.ExternalOrderTimelineEvent{}
	for rows.Next() {
		var item orderport.ExternalOrderTimelineEvent
		if err = rows.Scan(&item.Status, &item.RefundedMinor, &item.OccurredAt); err != nil {
			return nil, mapError(err)
		}
		items = append(items, item)
	}
	return items, mapError(rows.Err())
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	b := [20]byte{}
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	return string(b[i:])
}

var _ orderport.ExternalReadQueryService = (*Repository)(nil)
var _ orderport.ExternalOrderTimelineReader = (*Repository)(nil)
var _ = orderdomain.StatusPaid
