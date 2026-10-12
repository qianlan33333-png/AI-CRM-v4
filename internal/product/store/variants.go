package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

// The parent row lock serializes catalog edits and checkout selection. Removed
// rows remain immutable historical references and cannot be restored by stale IDs.
func (r *Repository) saveVariants(ctx context.Context, id productport.ID, input *[]productport.Variant, now time.Time) error {
	if input == nil {
		return nil
	}
	tx, err := transaction(ctx)
	if err != nil {
		return err
	}
	ids := []int64{}
	for i, v := range *input {
		attrs := v.Attributes
		if attrs == nil {
			attrs = []productport.VariantAttribute{}
		}
		raw, err := json.Marshal(attrs)
		if err != nil {
			return ErrInvalid
		}
		if v.ID == 0 {
			err = tx.QueryRow(ctx, `INSERT INTO product_variants(product_id,name,description,attributes,price_minor,position) VALUES($1,$2,$3,$4,$5,$6) RETURNING id`, id, v.Name, v.Description, raw, v.PriceMinor, i).Scan(&v.ID)
		} else {
			var updated int64
			err = tx.QueryRow(ctx, `UPDATE product_variants SET name=$3,description=$4,attributes=$5,price_minor=$6,position=$7,
    version=version+CASE WHEN (name,description,attributes,price_minor) IS DISTINCT FROM ($3::text,$4::text,$5::jsonb,$6::bigint) THEN 1 ELSE 0 END
    WHERE id=$1 AND product_id=$2 AND removed_at IS NULL RETURNING id`, v.ID, id, v.Name, v.Description, raw, v.PriceMinor, i).Scan(&updated)
		}
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return productport.ErrProductConflict
			}
			return mapDatabaseError(err)
		}
		ids = append(ids, v.ID)
	}
	_, err = tx.Exec(ctx, `UPDATE product_variants SET removed_at=$3,version=version+1 WHERE product_id=$1 AND removed_at IS NULL AND NOT(id=ANY($2::bigint[]))`, id, ids, now)
	return err
}

func variantsInput(input *[]productport.Variant) []productport.Variant {
	if input == nil {
		return nil
	}
	return *input
}
