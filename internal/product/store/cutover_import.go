package store

import (
	"context"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

func (r *Repository) CheckCutoverDefinition(ctx context.Context, id productport.ID, input productport.DefinitionImport) error {
	tx, err := transaction(ctx)
	if err != nil {
		return err
	}
	var code, name, currency string
	var amount int64
	var days int32
	err = tx.QueryRow(ctx, `SELECT product_code,name,price_minor,currency,COALESCE((SELECT duration_days FROM product_imported_service_period_definitions WHERE product_id=products.id),0) FROM products WHERE id=$1`, id).Scan(&code, &name, &amount, &currency, &days)
	if err != nil {
		return err
	}
	if code != input.ProductCode || name != input.Name || amount != input.PriceMinor || currency != input.Currency || days != input.ServicePeriodDurationDays {
		return productport.ErrProductConflict
	}
	return nil
}
