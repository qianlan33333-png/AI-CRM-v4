package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	"time"
)

type ShopSaved = orderport.ShopSaved

// UpsertShopOrderWithin keeps a separate immutable paid snapshot even if the
// first observed current state is fully refunded. It never enters settlement.
func (r *Repository) UpsertShopOrderWithin(ctx context.Context, source orderport.ShopOrder, snapshot domain.Snapshot, digest [32]byte, cutoff time.Time, ciphertext []byte, keyVersion int16) (ShopSaved, error) {
	tx, e := transaction(ctx)
	if e != nil {
		return ShopSaved{}, e
	}
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "shop-order:"+source.ID); e != nil {
		return ShopSaved{}, e
	}
	var id int64
	var origin string
	e = tx.QueryRow(ctx, `SELECT id,record_origin FROM orders WHERE provider='wechat_shop' AND merchant_order_no=$1 FOR UPDATE`, source.ID).Scan(&id, &origin)
	if e != nil && e != pgx.ErrNoRows {
		return ShopSaved{}, e
	}
	if e == nil && origin == "history" {
		return ShopSaved{}, nil
	} // historical imports can never become activation candidates
	if e == nil && origin != "external" {
		return ShopSaved{}, orderport.ErrConflict
	}
	if e == pgx.ErrNoRows {
		e = tx.QueryRow(ctx, `INSERT INTO orders(provider,source_system,source_key,merchant_order_no,provider_transaction_no,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,record_origin,effect_eligible,source_row_digest,version,created_at,updated_at) VALUES('wechat_shop',$1,$2,$2,$3,$4,$4,$5,'CNY','paid','external',false,$6,2,$7,$8) RETURNING id`, "wechat-shop:"+source.AppID, source.ID, source.TransactionID, snapshot.PayerCustomerID, source.PaidMinor, digest[:], source.CreatedAt, snapshot.UpdatedAt).Scan(&id)
		if e != nil {
			return ShopSaved{}, e
		}
		snapshot.ID = id
		for i, item := range snapshot.Items {
			_, e = tx.Exec(ctx, `INSERT INTO order_items(order_id,line_no,product_id,product_code,product_name,unit_amount_minor,quantity,line_amount_minor,amount_basis) VALUES($1,$2,$3,$4,$5,$6,$7,$8,'wechat_shop')`, id, item.LineNo, item.ProductID, item.ProductCode, item.ProductName, item.UnitAmountMinor, item.Quantity, item.LineAmountMinor)
			if e != nil {
				return ShopSaved{}, e
			}
			line := source.Lines[i]
			if _, e = tx.Exec(ctx, `INSERT INTO order_wechat_shop_lines VALUES($1,$2,$3,$4)`, id, item.LineNo, line.ProductID, line.SKUID); e != nil {
				return ShopSaved{}, e
			}
		}
		raw, _ := json.Marshal(snapshot)
		_, e = tx.Exec(ctx, `INSERT INTO order_wechat_shop_facts(order_id,app_id,paid_snapshot,paid_at,activation_eligible,contact_ciphertext,contact_key_version,source_updated_at,immutable_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, id, source.AppID, raw, source.PaidAt, !source.PaidAt.Before(cutoff), ciphertext, keyVersion, source.UpdatedAt, digest[:])
		if e != nil {
			return ShopSaved{}, e
		}
		sourceDigest := sha256.Sum256([]byte(fmt.Sprintf("order.external_paid.v1:%d", id)))
		var eventID int64
		if e = tx.QueryRow(ctx, `INSERT INTO order_paid_events(order_id,order_version,source_digest,occurred_at) VALUES($1,2,$2,$3) RETURNING id`, id, sourceDigest[:], source.PaidAt).Scan(&eventID); e != nil {
			return ShopSaved{}, e
		}
		key := fmt.Sprintf("order.external_paid.v1:%d", eventID)
		if _, e = tx.Exec(ctx, `INSERT INTO order_outbox(event_type,idempotency_key,aggregate_id,payload,occurred_at) VALUES('order.external_paid.v1',$1,$2,jsonb_build_object('order_id',$2::bigint,'paid_event_id',$3::bigint),$4)`, key, id, eventID, source.PaidAt); e != nil {
			return ShopSaved{}, e
		}
		if _, e = tx.Exec(ctx, `INSERT INTO order_status_history(order_id,to_status,refunded_minor,order_version,actor_scope,occurred_at) VALUES($1,'paid',0,2,'wechat_shop',$2)`, id, snapshot.UpdatedAt); e != nil {
			return ShopSaved{}, e
		}
		if _, e = tx.Exec(ctx, `INSERT INTO order_audit_events(event_type,order_id,actor_scope,payload,occurred_at) VALUES('order.external_imported',$1,'wechat_shop',jsonb_build_object('source','wechat_shop'),$2)`, id, snapshot.UpdatedAt); e != nil {
			return ShopSaved{}, e
		}
	}
	var prior []byte
	var app string
	var updated time.Time
	if e = tx.QueryRow(ctx, `SELECT immutable_digest,app_id,source_updated_at FROM order_wechat_shop_facts WHERE order_id=$1 FOR UPDATE`, id).Scan(&prior, &app, &updated); e != nil {
		return ShopSaved{}, e
	}
	if app != source.AppID || string(prior) != string(digest[:]) {
		return ShopSaved{}, orderport.ErrConflict
	}
	if !source.UpdatedAt.Before(updated) {
		var refunded, version int64
		var status string
		if e = tx.QueryRow(ctx, `SELECT refunded_minor,version,status FROM orders WHERE id=$1`, id).Scan(&refunded, &version, &status); e != nil {
			return ShopSaved{}, e
		}
		if source.RefundedMinor > refunded {
			next := "partially_refunded"
			if source.RefundedMinor == source.PaidMinor {
				next = "refunded"
			}
			if _, e = tx.Exec(ctx, `UPDATE orders SET refunded_minor=$2,status=$3,version=version+1,updated_at=GREATEST(updated_at,$4) WHERE id=$1`, id, source.RefundedMinor, next, source.UpdatedAt); e != nil {
				return ShopSaved{}, e
			}
			if _, e = tx.Exec(ctx, `INSERT INTO order_status_history(order_id,from_status,to_status,refunded_minor,order_version,actor_scope,occurred_at) VALUES($1,$2,$3,$4,$5,'wechat_shop',$6)`, id, status, next, source.RefundedMinor, version+1, source.UpdatedAt); e != nil {
				return ShopSaved{}, e
			}
			if _, e = tx.Exec(ctx, `INSERT INTO order_audit_events(event_type,order_id,actor_scope,payload,occurred_at) VALUES('order.external_refund_synced',$1,'wechat_shop',jsonb_build_object('refunded_minor',$2::bigint),$3)`, id, source.RefundedMinor, source.UpdatedAt); e != nil {
				return ShopSaved{}, e
			}
		}
		if _, e = tx.Exec(ctx, `UPDATE order_wechat_shop_facts SET source_updated_at=$2 WHERE order_id=$1`, id, source.UpdatedAt); e != nil {
			return ShopSaved{}, e
		}
	}
	return r.ReadShopSavedWithin(ctx, id)
}
func (r *Repository) ReadShopSavedWithin(ctx context.Context, id int64) (ShopSaved, error) {
	tx, e := transaction(ctx)
	if e != nil {
		return ShopSaved{}, e
	}
	var v ShopSaved
	var raw, digest []byte
	e = tx.QueryRow(ctx, `SELECT f.paid_snapshot,f.activation_eligible,f.contact_ciphertext,f.contact_key_version,p.id,p.order_version,p.source_digest,p.occurred_at,b.id FROM order_wechat_shop_facts f JOIN order_paid_events p ON p.order_id=f.order_id JOIN order_outbox b ON b.idempotency_key='order.external_paid.v1:'||p.id::text WHERE f.order_id=$1`, id).Scan(&raw, &v.Eligible, &v.Ciphertext, &v.KeyVersion, &v.Event.ID, &v.Event.OrderVersion, &digest, &v.Event.OccurredAt, &v.Event.DomainEventOutboxID)
	if e != nil {
		return v, e
	}
	if json.Unmarshal(raw, &v.Event.Order) != nil || len(digest) != 32 {
		return v, ErrInvalid
	}
	copy(v.Event.SourceDigest[:], digest)
	v.Event.OrderID = id
	rows, e := tx.Query(ctx, `SELECT source_product_id,source_sku_id FROM order_wechat_shop_lines WHERE order_id=$1 ORDER BY line_no`, id)
	if e != nil {
		return v, e
	}
	defer rows.Close()
	for rows.Next() {
		var l orderport.ShopOrderLine
		if e = rows.Scan(&l.ProductID, &l.SKUID); e != nil {
			return v, e
		}
		v.Lines = append(v.Lines, l)
	}
	return v, rows.Err()
}
func (r *Repository) MarkShopDispatchWithin(ctx context.Context, id int64) error {
	tx, e := transaction(ctx)
	if e != nil {
		return e
	}
	_, e = tx.Exec(ctx, `UPDATE order_wechat_shop_facts SET dispatch_complete=true WHERE order_id=$1`, id)
	return e
}
func (r *Repository) PendingShopOrdersWithin(ctx context.Context, app, product, sku string) ([]string, error) {
	tx, e := transaction(ctx)
	if e != nil {
		return nil, e
	}
	rows, e := tx.Query(ctx, `SELECT DISTINCT o.source_key FROM orders o JOIN order_wechat_shop_facts f ON f.order_id=o.id JOIN order_wechat_shop_lines l ON l.order_id=o.id WHERE f.app_id=$1 AND f.activation_eligible AND NOT f.dispatch_complete AND l.source_product_id=$2 AND l.source_sku_id=$3`, app, product, sku)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			return nil, e
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
