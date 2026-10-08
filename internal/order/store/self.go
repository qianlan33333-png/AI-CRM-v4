package store

import (
	"context"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	"strconv"
	"strings"
)

func selfPredicate(q orderport.SelfQuery) (string, []any, error) {
	if len(q.CustomerIDs) == 0 || q.Limit < 1 || q.Limit > 51 || q.AfterID < 0 || (q.AfterID > 0 && q.AfterAt.IsZero()) {
		return "", nil, ErrInvalid
	}
	for _, id := range q.CustomerIDs {
		if id < 1 {
			return "", nil, ErrInvalid
		}
	}
	args := []any{q.CustomerIDs}
	where := []string{`(payer_customer_id=ANY($1) OR beneficiary_customer_id=ANY($1))`}
	add := func(clause string, value any) {
		args = append(args, value)
		where = append(where, strings.ReplaceAll(clause, "?", "$"+strconv.Itoa(len(args))))
	}
	switch q.State {
	case "", "all":
	case "paid":
		where = append(where, `status IN ('paid','partially_refunded','refunded')`)
	case "pending":
		where = append(where, `status='pending_payment'`)
	case "closed":
		where = append(where, `status IN ('closed','cancelled','payment_failed')`)
	default:
		return "", nil, ErrInvalid
	}
	if q.Keyword != "" {
		literal := strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(q.Keyword)
		add(`EXISTS (SELECT 1 FROM order_items oi WHERE oi.order_id=orders.id AND oi.product_name ILIKE ?)`, "%"+literal+"%")
	}
	if q.OrderNo != "" {
		add(`merchant_order_no=?`, q.OrderNo)
	}
	// Imported payments have no original order timestamp. Their known payment
	// time can be filtered and displayed as payment time, never as order time.
	timeSQL := `(CASE WHEN provider='external_history' THEN (SELECT source_paid_at FROM order_external_history_facts f WHERE f.order_id=orders.id) ELSE created_at END)`
	if q.From != nil {
		add(timeSQL+">=?", q.From.UTC())
	}
	if q.Until != nil {
		add(timeSQL+"<?", q.Until.UTC())
	}
	if q.AfterID > 0 {
		args = append(args, q.AfterAt.UTC(), q.AfterID)
		where = append(where, `(created_at,id)<($`+strconv.Itoa(len(args)-1)+`,$`+strconv.Itoa(len(args))+`)`)
	}
	return strings.Join(where, " AND "), args, nil
}

func (r *Repository) ListSelfWithin(ctx context.Context, q orderport.SelfQuery) ([]orderport.SelfOrder, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, err
	}
	where, args, err := selfPredicate(q)
	if err != nil {
		return nil, err
	}
	args = append(args, q.Limit)
	rows, err := tx.Query(ctx, `SELECT `+orderColumns+` FROM orders WHERE `+where+` ORDER BY created_at DESC,id DESC LIMIT $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		return nil, mapError(err)
	}
	snapshots := []domain.Snapshot{}
	for rows.Next() {
		x, e := scanOrder(rows)
		if e != nil {
			rows.Close()
			return nil, e
		}
		snapshots = append(snapshots, x)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	out := make([]orderport.SelfOrder, 0, len(snapshots))
	for _, x := range snapshots {
		v, e := r.selfFacts(ctx, x)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, nil
}
func (r *Repository) GetSelfWithin(ctx context.Context, id int64, ids []int64, lock bool) (orderport.SelfOrder, error) {
	if id < 1 || len(ids) == 0 {
		return orderport.SelfOrder{}, orderport.ErrNotFound
	}
	for _, v := range ids {
		if v < 1 {
			return orderport.SelfOrder{}, ErrInvalid
		}
	}
	tx, e := transaction(ctx)
	if e != nil {
		return orderport.SelfOrder{}, e
	}
	sql := `SELECT ` + orderColumns + ` FROM orders WHERE id=$1 AND (payer_customer_id=ANY($2) OR beneficiary_customer_id=ANY($2))`
	if lock {
		sql += ` FOR UPDATE`
	}
	x, e := scanOrder(tx.QueryRow(ctx, sql, id, ids))
	if e != nil {
		return orderport.SelfOrder{}, e
	}
	return r.selfFacts(ctx, x)
}
func (r *Repository) selfFacts(ctx context.Context, x domain.Snapshot) (orderport.SelfOrder, error) {
	tx, e := transaction(ctx)
	if e != nil {
		return orderport.SelfOrder{}, e
	}
	x.Items, e = loadItems(ctx, tx, x.ID)
	if e != nil {
		return orderport.SelfOrder{}, e
	}
	x.ExternalHistory, e = readExternalHistory(ctx, tx, x)
	if e != nil {
		return orderport.SelfOrder{}, e
	}
	out := orderport.SelfOrder{Order: x}
	e = tx.QueryRow(ctx, `SELECT (SELECT occurred_at FROM order_paid_events WHERE order_id=$1)`, x.ID).Scan(&out.ConfirmedPaidAt)
	return out, e
}

var _ orderport.SelfReader = (*Repository)(nil)
