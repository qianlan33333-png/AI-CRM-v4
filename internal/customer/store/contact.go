package store

import (
	"context"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func (PostgreSQL) WriteContactWithin(ctx context.Context, c customerport.ContactUpdate, phone, address []byte) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	// INSERT ON CONFLICT handles concurrent first submissions. Lock before
	// assigning the server time so waiting transactions cannot look older.
	if _, err = tx.Exec(ctx, `INSERT INTO customer_contact_profiles(customer_id) VALUES($1) ON CONFLICT DO NOTHING`, c.CustomerID); err != nil {
		return err
	}
	var locked int64
	if err = tx.QueryRow(ctx, `SELECT customer_id FROM customer_contact_profiles WHERE customer_id=$1 FOR UPDATE`, c.CustomerID).Scan(&locked); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE customer_contact_profiles SET
 phone_ciphertext=CASE WHEN $2::bytea IS NOT NULL AND (NOT $5 OR phone_ciphertext IS NULL) THEN $2 ELSE phone_ciphertext END,
 phone_source=CASE WHEN $2::bytea IS NOT NULL AND (NOT $5 OR phone_ciphertext IS NULL) THEN $4 ELSE phone_source END,
 phone_updated_at=CASE WHEN $2::bytea IS NOT NULL AND (NOT $5 OR phone_ciphertext IS NULL) THEN CASE WHEN $5 THEN $6::timestamptz ELSE clock_timestamp() END ELSE phone_updated_at END,
 shipping_ciphertext=CASE WHEN $3::bytea IS NOT NULL AND (NOT $5 OR shipping_ciphertext IS NULL) THEN $3 ELSE shipping_ciphertext END,
 shipping_source=CASE WHEN $3::bytea IS NOT NULL AND (NOT $5 OR shipping_ciphertext IS NULL) THEN $4 ELSE shipping_source END,
 shipping_updated_at=CASE WHEN $3::bytea IS NOT NULL AND (NOT $5 OR shipping_ciphertext IS NULL) THEN CASE WHEN $5 THEN $6::timestamptz ELSE clock_timestamp() END ELSE shipping_updated_at END
 WHERE customer_id=$1`, c.CustomerID, phone, address, c.Source, c.OnlyIfEmpty, c.SourceAt)
	return err
}
func (PostgreSQL) ReadContactCiphertextsWithin(ctx context.Context, ids []customerdomain.CustomerID, shipping bool) ([]byte, []byte, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, nil, err
	}
	values := make([]int64, len(ids))
	for i, id := range ids {
		values[i] = int64(id)
	}
	var phone, address []byte
	err = tx.QueryRow(ctx, `SELECT
 (SELECT phone_ciphertext FROM customer_contact_profiles WHERE customer_id=ANY($1) AND phone_ciphertext IS NOT NULL ORDER BY phone_updated_at DESC,customer_id DESC LIMIT 1),
 (SELECT shipping_ciphertext FROM customer_contact_profiles WHERE $2 AND customer_id=ANY($1) AND shipping_ciphertext IS NOT NULL ORDER BY shipping_updated_at DESC,customer_id DESC LIMIT 1)`, values, shipping).Scan(&phone, &address)
	return phone, address, err
}
