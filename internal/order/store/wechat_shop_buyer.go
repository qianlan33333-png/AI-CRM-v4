package store

import (
	"context"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	"slices"
	"time"
)

func (r *Repository) UpsertShopBuyerWithin(ctx context.Context, id int64, v orderport.ShopBuyerMaterial) error {
	tx, err := transaction(ctx)
	if err != nil {
		return err
	}
	var payer *int64
	if err = tx.QueryRow(ctx, `SELECT payer_customer_id FROM orders WHERE id=$1 AND provider='wechat_shop' AND record_origin='external' FOR UPDATE`, id).Scan(&payer); err != nil {
		return err
	}
	var scope string
	var digest []byte
	if err = tx.QueryRow(ctx, `SELECT buyer_union_scope,buyer_union_digest FROM order_wechat_shop_facts WHERE order_id=$1 FOR UPDATE`, id).Scan(&scope, &digest); err != nil {
		return err
	}
	if scope != "" && (v.UnionScope != scope || string(v.UnionDigest[:]) != string(digest)) {
		return orderport.ErrConflict
	}
	var lookup []byte
	if v.UnionScope != "" {
		lookup = v.UnionDigest[:]
	}
	state := v.State
	if payer != nil && state == "pending" {
		state = "linked"
	}
	_, err = tx.Exec(ctx, `UPDATE order_wechat_shop_facts SET buyer_details_ciphertext=$2,buyer_details_key_version=$3,buyer_union_scope=$4,buyer_union_digest=$5,buyer_association_state=$6 WHERE order_id=$1`, id, v.Ciphertext, v.KeyVersion, v.UnionScope, lookup, state)
	if err != nil {
		return err
	}
	if v.CustomerID != nil && v.State == "linked" {
		return r.AssociateShopBuyerWithin(ctx, id, *v.CustomerID, "linked", v.CanonicalLineage)
	}
	return nil
}

func (r *Repository) AssociateShopBuyerWithin(ctx context.Context, id, customer int64, state string, lineage []int64) error {
	tx, err := transaction(ctx)
	if err != nil {
		return err
	}
	var payer *int64
	if err = tx.QueryRow(ctx, `SELECT payer_customer_id FROM orders WHERE id=$1 AND provider='wechat_shop' AND record_origin='external' FOR UPDATE`, id).Scan(&payer); err != nil {
		return err
	}
	if state == "conflict" {
		_, err = tx.Exec(ctx, `UPDATE order_wechat_shop_facts SET buyer_association_state='conflict' WHERE order_id=$1`, id)
		return err
	}
	if customer < 1 || state != "linked" || payer != nil && *payer != customer && !slices.Contains(lineage, *payer) {
		return orderport.ErrConflict
	}
	if payer != nil && *payer == customer {
		_, err = tx.Exec(ctx, `UPDATE order_wechat_shop_facts SET buyer_association_state='linked' WHERE order_id=$1`, id)
		return err
	}
	now := time.Now().UTC()
	if _, err = tx.Exec(ctx, `UPDATE orders SET payer_customer_id=$2,beneficiary_customer_id=$2,version=version+1,updated_at=GREATEST(updated_at,$3) WHERE id=$1`, id, customer, now); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE order_wechat_shop_facts SET buyer_association_state='linked' WHERE order_id=$1`, id); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO order_audit_events(event_type,order_id,actor_scope,payload,occurred_at) VALUES('order.shop_buyer_linked',$1,'oneid',jsonb_build_object('customer_id',$2::bigint),$3)`, id, customer, now); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO order_outbox(event_type,idempotency_key,aggregate_id,payload,occurred_at) VALUES('order.shop_buyer_linked.v1','order.shop_buyer_linked.v1:'||$1::bigint::text||':'||$2::bigint::text,$1::bigint,jsonb_build_object('order_id',$1::bigint,'customer_id',$2::bigint),$3) ON CONFLICT(idempotency_key) DO NOTHING`, id, customer, now)
	return err
}

func (r *Repository) ReadShopBuyerCiphertexts(ctx context.Context, ids []int64) ([]orderport.ShopBuyerCiphertext, error) {
	var result []orderport.ShopBuyerCiphertext
	if len(ids) == 0 {
		return result, nil
	}
	read := func(ctx context.Context) error {
		tx, err := transaction(ctx)
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT f.order_id,f.app_id,o.merchant_order_no,f.buyer_details_ciphertext,f.buyer_details_key_version FROM order_wechat_shop_facts f JOIN orders o ON o.id=f.order_id WHERE f.order_id=ANY($1::bigint[]) AND f.buyer_details_ciphertext IS NOT NULL`, ids)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var row orderport.ShopBuyerCiphertext
			if err = rows.Scan(&row.OrderID, &row.AppID, &row.SourceOrderID, &row.Ciphertext, &row.KeyVersion); err != nil {
				return err
			}
			result = append(result, row)
		}
		return rows.Err()
	}
	if _, err := transaction(ctx); err == nil {
		err = read(ctx)
		return result, err
	}
	err := r.Within(ctx, read)
	return result, err
}

func (r *Repository) PendingShopBuyerCiphertextsWithin(ctx context.Context, scope string, digest [32]byte) ([]orderport.ShopBuyerCiphertext, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, err
	}
	// Root-first caller; lock Order rows before its facts, matching import order.
	rows, err := tx.Query(ctx, `SELECT f.order_id,f.app_id,o.merchant_order_no,f.buyer_details_ciphertext,f.buyer_details_key_version FROM orders o JOIN order_wechat_shop_facts f ON f.order_id=o.id WHERE o.provider='wechat_shop' AND o.record_origin='external' AND o.payer_customer_id IS NULL AND f.buyer_union_scope=$1 AND f.buyer_union_digest=$2 AND f.buyer_association_state='pending' AND f.buyer_details_ciphertext IS NOT NULL ORDER BY o.id FOR UPDATE OF o`, scope, digest[:])
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []orderport.ShopBuyerCiphertext{}
	for rows.Next() {
		var row orderport.ShopBuyerCiphertext
		if err = rows.Scan(&row.OrderID, &row.AppID, &row.SourceOrderID, &row.Ciphertext, &row.KeyVersion); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}
