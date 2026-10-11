package store

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	productapp "github.com/qianlan33333-png/AI-CRM-v3/internal/product/app"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

func (r *Repository) ResolveShopSKUWithin(ctx context.Context, app, product, sku string) (productport.ID, bool, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return 0, false, err
	}
	var id productport.ID
	err = tx.QueryRow(ctx, `SELECT product_id FROM product_wechat_shop_skus WHERE app_id=$1 AND source_product_id=$2 AND source_sku_id=$3`, app, product, sku).Scan(&id)
	if err == pgx.ErrNoRows {
		return 0, false, nil
	}
	return id, err == nil, err
}

func (r *Repository) ImportShopSKUWithin(ctx context.Context, sku productport.ShopSKU, run string, at time.Time) (productport.ID, bool, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return 0, false, err
	}
	if sku.AppID == "" || sku.ProductID == "" || sku.SKUID == "" || strings.TrimSpace(sku.Name) == "" || len(sku.Name) > 200 || sku.PriceMinor < 0 || run == "" || at.IsZero() {
		return 0, false, ErrInvalid
	}
	code := fmt.Sprintf("wxshop-%x", sha256.Sum256([]byte(sku.AppID+"\x00"+sku.ProductID+"\x00"+sku.SKUID)))
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, code); err != nil {
		return 0, false, err
	}
	if id, found, e := r.ResolveShopSKUWithin(ctx, sku.AppID, sku.ProductID, sku.SKUID); e != nil || found {
		return id, false, e
	}
	projection, e := productapp.EnabledLegacyAdminProjectionForCreate(productapp.DefaultLegacyAdminProjection())
	if e != nil {
		return 0, false, e
	}
	var id productport.ID
	err = tx.QueryRow(ctx, `INSERT INTO products(product_code,name,price_minor,currency,stock_quantity,created_by,legacy_admin_projection,source_kind,created_at,updated_at) VALUES($1,$2,$3,'CNY',0,1,$4,'wechat_shop',$5,$5) RETURNING id`, code, sku.Name, sku.PriceMinor, projection, at).Scan(&id)
	if err != nil {
		return 0, false, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO product_wechat_shop_skus VALUES($1,$2,$3,$4,$5)`, sku.AppID, sku.ProductID, sku.SKUID, id, at)
	if err != nil {
		return 0, false, err
	}
	return id, true, nil
}

var _ productport.ShopCatalog = (*Repository)(nil)

func (r *Repository) ShopSKUCoordinatesWithin(ctx context.Context, id productport.ID) (productport.ShopSKU, bool, error) {
	tx, e := transaction(ctx)
	if e != nil {
		return productport.ShopSKU{}, false, e
	}
	var v productport.ShopSKU
	e = tx.QueryRow(ctx, `SELECT app_id,source_product_id,source_sku_id FROM product_wechat_shop_skus WHERE product_id=$1`, id).Scan(&v.AppID, &v.ProductID, &v.SKUID)
	if e == pgx.ErrNoRows {
		return v, false, nil
	}
	return v, e == nil, e
}
