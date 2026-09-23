package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/readshare"
)

func (s *PostgreSQL) SaveReadShare(ctx context.Context, v readshare.Share) (readshare.Share, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return v, err
	}
	fields, _ := json.Marshal(v.Fields)
	if v.ID == 0 {
		err = tx.QueryRow(ctx, `INSERT INTO hxc_dashboard_shares(resource_id,config,fields,mode,token_digest) VALUES($1,$2,$3,$4,$5) RETURNING id,version,enabled`, v.ResourceID, v.Config, fields, v.Mode, v.Digest).Scan(&v.ID, &v.Version, &v.Enabled)
	} else {
		err = tx.QueryRow(ctx, `UPDATE hxc_dashboard_shares SET enabled=false,version=version+1 WHERE id=$1 AND resource_id=$2 AND version=$3 RETURNING version,enabled`, v.ID, v.ResourceID, v.Version).Scan(&v.Version, &v.Enabled)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrViewConflict
	}
	return v, err
}
func (s *PostgreSQL) ReadShare(ctx context.Context, digest []byte) (readshare.Share, error) {
	var v readshare.Share
	var fields []byte
	err := s.pool.QueryRow(ctx, `SELECT id,resource_id,config,fields,mode,enabled,version FROM hxc_dashboard_shares WHERE token_digest=$1 AND enabled=true`, digest).Scan(&v.ID, &v.ResourceID, &v.Config, &fields, &v.Mode, &v.Enabled, &v.Version)
	if err != nil {
		return v, err
	}
	err = json.Unmarshal(fields, &v.Fields)
	return v, err
}
func (s *PostgreSQL) ListReadShares(ctx context.Context, resource int64) ([]readshare.Share, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,resource_id,config,fields,mode,enabled,version FROM hxc_dashboard_shares WHERE resource_id=$1 ORDER BY id DESC`, resource)
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

func (s *PostgreSQL) SaveReadShareReceipted(ctx context.Context, actor int64, key, digest []byte, v readshare.Share) (readshare.Share, bool, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return v, false, err
	}
	_, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, fmt.Sprintf("hxc-share:%d:%x", actor, key))
	if err != nil {
		return v, false, err
	}
	var old, raw []byte
	err = tx.QueryRow(ctx, `SELECT payload_digest,result FROM hxc_dashboard_view_receipts WHERE actor_id=$1 AND key_digest=$2`, actor, key).Scan(&old, &raw)
	if err == nil {
		if string(old) != string(digest) {
			return v, false, ErrViewConflict
		}
		err = json.Unmarshal(raw, &v)
		return v, true, err
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return v, false, err
	}
	v, err = s.SaveReadShare(ctx, v)
	if err != nil {
		return v, false, err
	}
	raw, err = json.Marshal(v)
	if err != nil {
		return v, false, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO hxc_dashboard_view_receipts(actor_id,key_digest,payload_digest,result) VALUES($1,$2,$3,$4)`, actor, key, digest, raw)
	return v, false, err
}

func (s *PostgreSQL) ReadShareKey(ctx context.Context) ([]byte, error) {
	var key []byte
	err := s.pool.QueryRow(ctx, `SELECT key_bytes FROM hxc_dashboard_share_key WHERE singleton=true`).Scan(&key)
	return key, err
}
