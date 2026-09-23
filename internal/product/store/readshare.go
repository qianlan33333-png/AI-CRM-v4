package store

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/readshare"
	productapp "github.com/qianlan33333-png/AI-CRM-v3/internal/product/app"
)

func (s *Repository) SaveReadShare(ctx context.Context, v readshare.Share) (readshare.Share, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return v, err
	}
	fields, _ := json.Marshal(v.Fields)
	if v.ID == 0 {
		err = tx.QueryRow(ctx, `INSERT INTO product_dashboard_shares(resource_id,config,fields,mode,token_digest) VALUES($1,$2,$3,$4,$5) RETURNING id,version,enabled`, v.ResourceID, v.Config, fields, v.Mode, v.Digest).Scan(&v.ID, &v.Version, &v.Enabled)
	} else {
		err = tx.QueryRow(ctx, `UPDATE product_dashboard_shares SET enabled=false,version=version+1 WHERE id=$1 AND resource_id=$2 AND version=$3 RETURNING version,enabled`, v.ID, v.ResourceID, v.Version).Scan(&v.Version, &v.Enabled)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		err = productapp.ErrConflict
	}
	return v, err
}
func (s *Repository) ReadShare(ctx context.Context, digest []byte) (readshare.Share, error) {
	var v readshare.Share
	var fields []byte
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return v, err
	}
	err = tx.QueryRow(ctx, `SELECT id,resource_id,config,fields,mode,enabled,version FROM product_dashboard_shares WHERE token_digest=$1 AND enabled=true`, digest).Scan(&v.ID, &v.ResourceID, &v.Config, &fields, &v.Mode, &v.Enabled, &v.Version)
	if err != nil {
		return v, err
	}
	err = json.Unmarshal(fields, &v.Fields)
	return v, err
}
func (s *Repository) ListReadShares(ctx context.Context, resource int64) ([]readshare.Share, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT id,resource_id,config,fields,mode,enabled,version FROM product_dashboard_shares WHERE resource_id=$1 ORDER BY id DESC`, resource)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []readshare.Share{}
	for rows.Next() {
		var v readshare.Share
		var fields []byte
		if err = rows.Scan(&v.ID, &v.ResourceID, &v.Config, &fields, &v.Mode, &v.Enabled, &v.Version); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(fields, &v.Fields); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Repository) ReadShareKey(ctx context.Context) ([]byte, error) {
	var key []byte
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	err = tx.QueryRow(ctx, `SELECT key_bytes FROM product_dashboard_share_key WHERE singleton=true`).Scan(&key)
	return key, err
}
