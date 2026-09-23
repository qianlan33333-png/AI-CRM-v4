package query

import (
	"context"
	"errors"
	"strconv"

	"github.com/jackc/pgx/v5"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func (PostgreSQL) CustomerPublicNumbers(ctx context.Context, ids []customerdomain.CustomerID) (map[customerdomain.CustomerID]string, error) {
	result := make(map[customerdomain.CustomerID]string, len(ids))
	if len(ids) > 500 {
		return nil, identitydomain.ErrInvalidReference
	}
	keys := make([]int64, len(ids))
	for i, id := range ids {
		if id < 1 {
			return nil, identitydomain.ErrInvalidReference
		}
		keys[i] = int64(id)
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT id,public_number FROM customers WHERE id=ANY($1::bigint[])`, keys)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id customerdomain.CustomerID
		var number int64
		if err = rows.Scan(&id, &number); err != nil {
			return nil, err
		}
		result[id] = strconv.FormatInt(number, 10)
	}
	return result, rows.Err()
}

func (PostgreSQL) CustomerForPublicNumber(ctx context.Context, value string) (customerdomain.CustomerID, bool, error) {
	number, err := strconv.ParseInt(value, 10, 64)
	if err != nil || number < 1000000 || number > 9999999 || strconv.FormatInt(number, 10) != value {
		return 0, false, identitydomain.ErrInvalidReference
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return 0, false, err
	}
	var id customerdomain.CustomerID
	err = tx.QueryRow(ctx, `SELECT id FROM customers WHERE public_number=$1`, number).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	return id, err == nil, err
}
