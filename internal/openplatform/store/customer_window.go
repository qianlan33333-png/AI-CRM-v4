package store

import (
	"context"
	"errors"
	"time"

	openplatformport "github.com/qianlan33333-png/AI-CRM-v3/internal/openplatform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

type CustomerWindows struct{}

func (CustomerWindows) FreezeCustomerWindow(ctx context.Context, window openplatformport.CustomerWindow) error {
	if len(window.ID) != 32 || window.ClientID == "" || window.GrantDigest == "" || !window.From.Before(window.To) || window.ExpiresAt.Before(time.Now()) {
		return errors.New("invalid contact window")
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM openplatform_customer_windows WHERE expires_at<clock_timestamp()`); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO openplatform_customer_windows(id,client_id,grant_digest,from_time,to_time,item_count,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, window.ID, window.ClientID, window.GrantDigest, window.From, window.To, len(window.Items), window.ExpiresAt); err != nil {
		return err
	}
	for index, item := range window.Items {
		if item.Key == "" || item.ChangedAt.IsZero() || len(item.Data) == 0 {
			return errors.New("invalid contact window item")
		}
		if _, err = tx.Exec(ctx, `INSERT INTO openplatform_customer_window_items(window_id,ordinal,record_key,changed_at,projection) VALUES($1,$2,$3,$4,$5::jsonb)`, window.ID, index, item.Key, item.ChangedAt, []byte(item.Data)); err != nil {
			return err
		}
	}
	return nil
}

func (CustomerWindows) ReadCustomerWindow(ctx context.Context, id string, offset, limit int) (openplatformport.CustomerWindow, error) {
	if len(id) != 32 || offset < 0 || limit < 1 || limit > 100 {
		return openplatformport.CustomerWindow{}, errors.New("invalid contact window cursor")
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return openplatformport.CustomerWindow{}, err
	}
	result := openplatformport.CustomerWindow{ID: id}
	if err = tx.QueryRow(ctx, `SELECT client_id,grant_digest,from_time,to_time,item_count,expires_at FROM openplatform_customer_windows WHERE id=$1 AND expires_at>clock_timestamp()`, id).Scan(&result.ClientID, &result.GrantDigest, &result.From, &result.To, &result.ItemCount, &result.ExpiresAt); err != nil {
		return openplatformport.CustomerWindow{}, err
	}
	rows, err := tx.Query(ctx, `SELECT record_key,changed_at,projection FROM openplatform_customer_window_items WHERE window_id=$1 AND ordinal>=$2 ORDER BY ordinal LIMIT $3`, id, offset, limit)
	if err != nil {
		return openplatformport.CustomerWindow{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var item openplatformport.CustomerWindowItem
		if err = rows.Scan(&item.Key, &item.ChangedAt, &item.Data); err != nil {
			return openplatformport.CustomerWindow{}, err
		}
		result.Items = append(result.Items, item)
	}
	return result, rows.Err()
}

var _ openplatformport.CustomerWindowRepository = CustomerWindows{}
